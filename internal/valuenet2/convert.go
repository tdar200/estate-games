package valuenet2

import "github.com/tdar200/estate-games/internal/valuenet"

// FromV1 converts a record of internal/valuenet (Expert's 6-slot format) to
// this format, so the datasets recorded for Expert train this network too
// (they hold games of up to 6 players; 7 and 8 come from new data).
func FromV1(r *valuenet.Record) Record {
	out := Record{Game: r.Game, Winner: r.Winner}
	p, q := &r.Pos, &out.Pos
	q.NPlayers, q.ToMove, q.Rules, q.Turns, q.Pot = p.NPlayers, p.ToMove, p.Rules, p.Turns, p.Pot
	for i := range p.Players {
		a := &p.Players[i]
		q.Players[i] = PlayerState{Cash: a.Cash, Pos: a.Pos, Flags: a.Flags, JailTurns: a.JailTurns, JailCards: a.JailCards, Owed: a.Owed, Lent: a.Lent}
	}
	for k := range p.Props {
		q.Props[k] = PropState{Owner: p.Props[k].Owner, Houses: p.Props[k].Houses, Mortgaged: p.Props[k].Mortgaged}
	}
	return out
}
