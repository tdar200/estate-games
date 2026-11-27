// Package valuenet is a learned evaluation of Estate positions: a small
// multilayer perceptron that scores a position from each player's point of
// view; a softmax over the active players' scores gives each player's
// probability of winning the game.
//
// The network is trained by cmd/estate-ml on positions from self-play games
// (the label is the eventual winner) and shipped embedded (model.bin). It
// sees only public information: cash, positions, ownership, buildings,
// mortgages, jail state, cards held, loans and whose turn it is. Never the
// dice to come or the order of the decks.
package valuenet

import (
	"encoding/binary"
	"errors"

	"github.com/tdar200/estate-games/internal/engine"
)

// MaxSlots is the number of player slots of a Position, and so of the
// network's input (FeatureVersion 1 and the embedded model were trained with
// 6). It is deliberately not engine.MaxPlayers: games may have more players
// (up to 8) than the network can read. Such a game fits only while at most
// MaxSlots players are still in it (see Seats); callers must check Fits
// before evaluating one. A network for 8 players needs new features and
// retraining (docs/ai/EXPERT.md, future work).
const MaxSlots = 6

// NumOwnable is the number of ownable spaces (engine.OwnableIndexes).
const NumOwnable = 28

// Rule flags (Position.Rules).
const (
	RuleLoans      = 1 << iota // loans house rule
	RuleJackpot                // City Park jackpot (freeParking)
	RuleNoAuctions             // auctions switched off
)

// Player flags.
const (
	flagBankrupt = 1 << iota
	flagInJail
)

// PlayerState is one seat of a Position.
type PlayerState struct {
	Cash      int32
	Pos       uint8
	Flags     uint8
	JailTurns uint8
	JailCards uint8
	Owed      uint16 // total still to repay on active loans as borrower
	Lent      uint16 // total still to be repaid to this player as lender
}

// Bankrupt reports whether the player is out.
func (p *PlayerState) Bankrupt() bool { return p.Flags&flagBankrupt != 0 }

// InJail reports whether the player is in jail.
func (p *PlayerState) InJail() bool { return p.Flags&flagInJail != 0 }

// PropState is one ownable space of a Position.
type PropState struct {
	Owner     int8 // player index (turn order), -1 = bank
	Houses    uint8
	Mortgaged bool
}

// Position is a compact, public game state. Players are in turn order (the
// order of engine.Game.Players once the opening order is rolled).
type Position struct {
	NPlayers uint8
	ToMove   uint8  // index of the player whose turn it is
	Rules    uint8  // Rule* flags
	Turns    uint16 // completed turns, all players
	Pot      uint16 // City Park pot (freeParking)
	Players  [MaxSlots]PlayerState
	Props    [NumOwnable]PropState // engine.OwnableIndexes order
}

// ownableSlot maps a board index to its Props slot (-1 if not ownable).
var ownableSlot [engine.BoardSize]int

func init() {
	for i := range ownableSlot {
		ownableSlot[i] = -1
	}
	for k, index := range engine.OwnableIndexes {
		ownableSlot[index] = k
	}
	if len(engine.OwnableIndexes) != NumOwnable {
		panic("valuenet: unexpected board")
	}
	initBoard()
}

// Seats maps the players of g to Position slots: seats[s] is the index in
// g.Players of the player in slot s, for s < n. A game of up to MaxSlots
// players maps one to one. A larger game (7 or 8 players) fits once enough
// players have gone bankrupt: the bankrupt players are left out (they own
// nothing and never act again) and the others keep their turn order. ok is
// false while more than MaxSlots players are still in; seats then holds the
// first MaxSlots players (so a caller that ignores ok never indexes out of
// range, but the evaluation would be wrong: check ok or Fits).
func Seats(g *engine.Game) (seats [MaxSlots]int, n int, ok bool) {
	if len(g.Players) <= MaxSlots {
		for i := range g.Players {
			seats[i] = i
		}
		return seats, len(g.Players), true
	}
	for i, p := range g.Players {
		if p.Bankrupt {
			continue
		}
		if n == MaxSlots {
			for s := range seats {
				seats[s] = s
			}
			return seats, MaxSlots, false
		}
		seats[n] = i
		n++
	}
	return seats, n, true
}

// Fits reports whether the network can read g: at most MaxSlots players, or
// at most MaxSlots still in.
func Fits(g *engine.Game) bool {
	_, _, ok := Seats(g)
	return ok
}

// SlotOf returns the Position slot of playerID in g, or -1 when the player
// is not represented (unknown, left out as bankrupt, or g does not fit).
func SlotOf(g *engine.Game, playerID string) int {
	seats, n, ok := Seats(g)
	if !ok {
		return -1
	}
	for s := 0; s < n; s++ {
		if g.Players[seats[s]].ID == playerID {
			return s
		}
	}
	return -1
}

// FromGame reads the public state of g.
func FromGame(g *engine.Game) Position {
	var pos Position
	FillFromGame(&pos, g)
	return pos
}

// FillFromGame is FromGame into an existing Position (no allocation). Slots
// follow Seats; check Fits first for games of more than MaxSlots players.
func FillFromGame(pos *Position, g *engine.Game) {
	*pos = Position{}
	seats, n, _ := Seats(g)
	pos.NPlayers = uint8(n)
	for s := 0; s < n; s++ {
		if seats[s] == g.CurrentIndex {
			pos.ToMove = uint8(s)
		}
	}
	if g.Settings.Rules.Loans {
		pos.Rules |= RuleLoans
	}
	if g.Settings.Rules.FreeParkingJackpot {
		pos.Rules |= RuleJackpot
	}
	if g.Settings.Rules.Auctions != nil && !*g.Settings.Rules.Auctions {
		pos.Rules |= RuleNoAuctions
	}
	turns := 0
	for _, c := range g.TurnCounts {
		turns += c
	}
	pos.Turns = uint16(min(turns, 65535))
	pos.Pot = uint16(max(0, min(g.FreeParking, 65535)))
	var ids [MaxSlots]string
	for i := 0; i < n; i++ {
		p := g.Players[seats[i]]
		ids[i] = p.ID
		ps := &pos.Players[i]
		ps.Cash = int32(p.Cash)
		ps.Pos = uint8(p.Position)
		if p.Bankrupt {
			ps.Flags |= flagBankrupt
		}
		if p.InJail {
			ps.Flags |= flagInJail
		}
		ps.JailTurns = uint8(min(p.JailTurns, 255))
		ps.JailCards = uint8(min(p.JailCards, 255))
	}
	index := func(id string) int8 {
		if id == "" {
			return -1
		}
		for i := 0; i < n; i++ {
			if ids[i] == id {
				return int8(i)
			}
		}
		return -1
	}
	for k, sq := range engine.OwnableIndexes {
		st := g.Properties[sq]
		if st == nil {
			pos.Props[k].Owner = -1
			continue
		}
		pos.Props[k] = PropState{Owner: index(st.OwnerID), Houses: uint8(st.Houses), Mortgaged: st.Mortgaged}
	}
	for _, loan := range g.Loans {
		if loan == nil || loan.Status != engine.LoanActive {
			continue
		}
		if b := index(loan.BorrowerID); b >= 0 {
			pos.Players[b].Owed = uint16(min(int(pos.Players[b].Owed)+loan.Repay, 65535))
		}
		if l := index(loan.LenderID); l >= 0 {
			pos.Players[l].Lent = uint16(min(int(pos.Players[l].Lent)+loan.Repay, 65535))
		}
	}
}

// Record is a training example: a position, its game and the game's winner.
type Record struct {
	Game   uint32
	Winner int8 // player index, -1 if unknown
	Pos    Position
}

// RecordSize is the encoded size of a Record.
const RecordSize = 12 + MaxSlots*12 + NumOwnable

// Encode writes r into b (len >= RecordSize).
func (r *Record) Encode(b []byte) {
	le := binary.LittleEndian
	p := &r.Pos
	le.PutUint32(b[0:], r.Game)
	b[4] = byte(r.Winner)
	b[5] = p.NPlayers
	b[6] = p.ToMove
	b[7] = p.Rules
	le.PutUint16(b[8:], p.Turns)
	le.PutUint16(b[10:], p.Pot)
	o := 12
	for i := range p.Players {
		ps := &p.Players[i]
		le.PutUint32(b[o:], uint32(ps.Cash))
		b[o+4] = ps.Pos
		b[o+5] = ps.Flags
		b[o+6] = ps.JailTurns
		b[o+7] = ps.JailCards
		le.PutUint16(b[o+8:], ps.Owed)
		le.PutUint16(b[o+10:], ps.Lent)
		o += 12
	}
	for k := range p.Props {
		ps := &p.Props[k]
		v := byte(ps.Owner+1) & 7
		v |= (ps.Houses & 7) << 3
		if ps.Mortgaged {
			v |= 1 << 6
		}
		b[o+k] = v
	}
}

// Decode reads a record written by Encode.
func (r *Record) Decode(b []byte) error {
	if len(b) < RecordSize {
		return errors.New("valuenet: short record")
	}
	le := binary.LittleEndian
	*r = Record{}
	p := &r.Pos
	r.Game = le.Uint32(b[0:])
	r.Winner = int8(b[4])
	p.NPlayers = b[5]
	p.ToMove = b[6]
	p.Rules = b[7]
	p.Turns = le.Uint16(b[8:])
	p.Pot = le.Uint16(b[10:])
	if p.NPlayers < 2 || int(p.NPlayers) > MaxSlots || p.ToMove >= p.NPlayers {
		return errors.New("valuenet: corrupt record")
	}
	o := 12
	for i := range p.Players {
		ps := &p.Players[i]
		ps.Cash = int32(le.Uint32(b[o:]))
		ps.Pos = b[o+4]
		ps.Flags = b[o+5]
		ps.JailTurns = b[o+6]
		ps.JailCards = b[o+7]
		ps.Owed = le.Uint16(b[o+8:])
		ps.Lent = le.Uint16(b[o+10:])
		o += 12
	}
	for k := range p.Props {
		v := b[o+k]
		p.Props[k] = PropState{Owner: int8(v&7) - 1, Houses: (v >> 3) & 7, Mortgaged: v&(1<<6) != 0}
	}
	return nil
}

// SetOwner gives board space index to player owner (-1 = the bank); a
// non-ownable index is ignored. Houses and mortgage state are kept.
func (p *Position) SetOwner(index, owner int) {
	if index < 0 || index >= len(ownableSlot) {
		return
	}
	if k := ownableSlot[index]; k >= 0 {
		p.Props[k].Owner = int8(owner)
	}
}
