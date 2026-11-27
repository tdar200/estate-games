package valuenet

import (
	"github.com/tdar200/estate-games/internal/engine"
)

// Features: a fixed-length vector describing a Position from one player's
// point of view ("me"). Other players are numbered by turn order after me
// (slot 1 moves next after me, ...), so the network shares its weights across
// seats and a softmax over the per-player scores gives win probabilities.
//
// Layout (FeatureVersion 1):
//
//	per slot (6 x slotWidth): present, active, cash, low-cash, net worth,
//	    in jail, jail turns, jail cards, to move, complete groups, buildings,
//	    expected rent collected per opponent roll, expected rent paid on its
//	    next roll, railroads, utilities, loans owed, loans lent, mortgaged
//	    spaces, groups with 3+ houses, then its square (one-hot of 40)
//	per ownable space (28 x 10): owner (bank or slot, one-hot of 7), houses,
//	    hotel, mortgaged
//	per colour group (8 x 6): my share, best opponent share, I own it, an
//	    opponent owns it, building level, unowned share
//	global: turns, houses and hotels left, players left, rules (3), pot
const FeatureVersion = 1

const (
	slotDense    = 19
	slotWidth    = slotDense + engine.BoardSize
	squareWidth  = 10
	groupWidth   = 6
	numGroups    = 8
	globalWidth  = 8
	squaresBase  = MaxSlots * slotWidth
	groupsBase   = squaresBase + NumOwnable*squareWidth
	globalBase   = groupsBase + numGroups*groupWidth
	NumFeatures  = globalBase + globalWidth
	maxNonZeroes = NumFeatures
)

// Sparse is a feature vector as (index, value) pairs (most inputs are zero).
type Sparse struct {
	Idx []int32
	Val []float32
}

func (s *Sparse) reset() {
	s.Idx = s.Idx[:0]
	s.Val = s.Val[:0]
}

func (s *Sparse) add(i int, v float32) {
	if v != 0 {
		s.Idx = append(s.Idx, int32(i))
		s.Val = append(s.Val, v)
	}
}

// Dense expands s into a vector of NumFeatures.
func (s *Sparse) Dense() []float32 {
	out := make([]float32, NumFeatures)
	for k, i := range s.Idx {
		out[i] += s.Val[k]
	}
	return out
}

// board tables
var (
	sqKind    [engine.BoardSize]int8 // 0 none, 1 property, 2 railroad, 3 utility
	sqGroup   [engine.BoardSize]int8 // colour group or -1
	sqPrice   [engine.BoardSize]int32
	sqBuild   [engine.BoardSize]int32
	sqRents   [engine.BoardSize][6]int32
	groupSqs  [numGroups][]int // ownable slots of each group
	landProb  [engine.BoardSize]float64
	diceProb  [13]float64
	railSlots []int
	utilSlots []int
)

func initBoard() {
	for i := range sqGroup {
		sqGroup[i] = -1
	}
	groupIndex := map[string]int{}
	for g, key := range engine.GroupOrder {
		groupIndex[key] = g
	}
	for _, s := range engine.Board {
		i := s.Index
		sqPrice[i] = int32(s.Price)
		sqBuild[i] = int32(s.BuildCost)
		for k := 0; k < len(s.Rents) && k < 6; k++ {
			sqRents[i][k] = int32(s.Rents[k])
		}
		switch s.Type {
		case engine.SpaceProperty:
			sqKind[i] = 1
			g := groupIndex[s.Group]
			sqGroup[i] = int8(g)
			groupSqs[g] = append(groupSqs[g], ownableSlot[i])
		case engine.SpaceRailroad:
			sqKind[i] = 2
			railSlots = append(railSlots, ownableSlot[i])
		case engine.SpaceUtility:
			sqKind[i] = 3
			utilSlots = append(utilSlots, ownableSlot[i])
		}
	}
	for a := 1; a <= 6; a++ {
		for b := 1; b <= 6; b++ {
			diceProb[a+b] += 1.0 / 36
		}
	}
	landProb = stationary()
}

// stationary approximates where a token ends its moves: a Markov chain over
// the 40 squares with two dice, Busted (sent to jail), and the movement cards of both
// decks (doubles and jail stays are ignored). Used to weigh rents.
func stationary() [engine.BoardSize]float64 {
	const n = engine.BoardSize
	jail, goToJail := 10, 30
	var chance, chest []bool = make([]bool, n), make([]bool, n)
	for _, s := range engine.Board {
		switch s.Type {
		case "chance":
			chance[s.Index] = true
		case "chest":
			chest[s.Index] = true
		case "goToJail":
			goToJail = s.Index
		case "jail":
			jail = s.Index
		}
	}
	nearest := func(from, kind int) int {
		for k := 1; k <= n; k++ {
			if sqKind[(from+k)%n] == int8(kind) {
				return (from + k) % n
			}
		}
		return from
	}
	var trans [n][n]float64
	for s := 0; s < n; s++ {
		for d := 2; d <= 12; d++ {
			p := diceProb[d]
			t := (s + d) % n
			switch {
			case t == goToJail:
				trans[s][jail] += p
			case chance[t]:
				q := p / float64(len(engine.ChanceCards))
				for _, c := range engine.ChanceCards {
					dest := t
					switch c.Effect.Type {
					case engine.EffectMoveTo:
						dest = c.Effect.Target
					case engine.EffectNearestRail:
						dest = nearest(t, 2)
					case engine.EffectNearestUtility:
						dest = nearest(t, 3)
					case engine.EffectMove:
						dest = ((t+c.Effect.Steps)%n + n) % n
					case engine.EffectJail:
						dest = jail
					}
					trans[s][dest] += q
				}
			case chest[t]:
				q := p / float64(len(engine.ChestCards))
				for _, c := range engine.ChestCards {
					dest := t
					switch c.Effect.Type {
					case engine.EffectMoveTo:
						dest = c.Effect.Target
					case engine.EffectJail:
						dest = jail
					}
					trans[s][dest] += q
				}
			default:
				trans[s][t] += p
			}
		}
	}
	var pi [n]float64
	for i := range pi {
		pi[i] = 1.0 / n
	}
	for it := 0; it < 200; it++ {
		var next [n]float64
		for s := 0; s < n; s++ {
			for t := 0; t < n; t++ {
				next[t] += pi[s] * trans[s][t]
			}
		}
		pi = next
	}
	return pi
}

// rentAt is the rent a visitor pays on board square sq (0 if unowned or
// mortgaged), with utilities at the average roll.
func rentAt(p *Position, sq int) float64 {
	k := ownableSlot[sq]
	if k < 0 {
		return 0
	}
	ps := &p.Props[k]
	if ps.Owner < 0 || ps.Mortgaged {
		return 0
	}
	switch sqKind[sq] {
	case 1:
		if ps.Houses > 0 {
			return float64(sqRents[sq][min(int(ps.Houses), 5)])
		}
		g := sqGroup[sq]
		for _, o := range groupSqs[g] {
			if p.Props[o].Owner != ps.Owner {
				return float64(sqRents[sq][0])
			}
		}
		return float64(2 * sqRents[sq][0])
	case 2:
		count := 0
		for _, o := range railSlots {
			if p.Props[o].Owner == ps.Owner {
				count++
			}
		}
		return float64(int(25) << max(0, count-1))
	case 3:
		count := 0
		for _, o := range utilSlots {
			if p.Props[o].Owner == ps.Owner {
				count++
			}
		}
		if count == 2 {
			return 70
		}
		return 28
	}
	return 0
}

// Featurizer computes features; it keeps scratch buffers (not safe for
// concurrent use).
type Featurizer struct {
	rent     [engine.BoardSize]float64
	worth    [MaxSlots]float64
	income   [MaxSlots]float64
	complete [MaxSlots]int
	houses   [MaxSlots]int
	rails    [MaxSlots]int
	utils    [MaxSlots]int
	mortg    [MaxSlots]int
	dev3     [MaxSlots]int
	nextRent [MaxSlots]float64
	gOwn     [numGroups][MaxSlots]int
	gLevel   [numGroups]int
	gOwner   [numGroups]int // player owning the whole group, or -1
	pos      *Position
}

// prepare computes the per-position quantities shared by every point of view.
func (f *Featurizer) prepare(p *Position) {
	f.pos = p
	f.worth = [MaxSlots]float64{}
	f.income = [MaxSlots]float64{}
	f.complete = [MaxSlots]int{}
	f.houses = [MaxSlots]int{}
	f.rails = [MaxSlots]int{}
	f.utils = [MaxSlots]int{}
	f.mortg = [MaxSlots]int{}
	f.dev3 = [MaxSlots]int{}
	f.gOwn = [numGroups][MaxSlots]int{}
	n := int(p.NPlayers)
	for i := 0; i < n; i++ {
		if !p.Players[i].Bankrupt() {
			f.worth[i] = float64(p.Players[i].Cash)
		}
	}
	for sq := 0; sq < engine.BoardSize; sq++ {
		k := ownableSlot[sq]
		if k < 0 {
			f.rent[sq] = 0
			continue
		}
		f.rent[sq] = rentAt(p, sq)
		ps := &p.Props[k]
		o := int(ps.Owner)
		if o < 0 || o >= n {
			continue
		}
		if ps.Mortgaged {
			f.worth[o] += float64(sqPrice[sq] / 2)
			f.mortg[o]++
		} else {
			f.worth[o] += float64(sqPrice[sq])
		}
		f.worth[o] += float64(int32(ps.Houses) * sqBuild[sq])
		f.income[o] += f.rent[sq] * landProb[sq]
		f.houses[o] += int(ps.Houses)
		switch sqKind[sq] {
		case 1:
			f.gOwn[sqGroup[sq]][o]++
		case 2:
			f.rails[o]++
		case 3:
			f.utils[o]++
		}
	}
	for g := 0; g < numGroups; g++ {
		f.gOwner[g] = -1
		f.gLevel[g] = 0
		for o := 0; o < n; o++ {
			if f.gOwn[g][o] == len(groupSqs[g]) {
				f.gOwner[g] = o
				level := 5
				mortgaged := false
				for _, k := range groupSqs[g] {
					level = min(level, int(p.Props[k].Houses))
					mortgaged = mortgaged || p.Props[k].Mortgaged
				}
				f.gLevel[g] = level
				if !mortgaged {
					f.complete[o]++
				}
				if level >= 3 {
					f.dev3[o]++
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		ps := &p.Players[i]
		f.nextRent[i] = 0
		if ps.Bankrupt() {
			continue
		}
		from := int(ps.Pos)
		for d := 2; d <= 12; d++ {
			sq := (from + d) % engine.BoardSize
			if k := ownableSlot[sq]; k >= 0 && int(p.Props[k].Owner) != i {
				f.nextRent[i] += diceProb[d] * f.rent[sq]
			}
		}
	}
}

// Features writes the features of position p from player me's point of view.
func (f *Featurizer) Features(p *Position, me int, out *Sparse) {
	f.prepare(p)
	f.features(p, me, out)
}

// Prepare reads p for later Perspective calls (p must not change meanwhile).
func (f *Featurizer) Prepare(p *Position) { f.prepare(p) }

// Perspective writes the features of the prepared position from player me's
// point of view.
func (f *Featurizer) Perspective(me int, out *Sparse) { f.features(f.pos, me, out) }

func (f *Featurizer) features(p *Position, me int, out *Sparse) {
	out.reset()
	n := int(p.NPlayers)
	slotOf := func(i int) int { return (i - me + n) % n }
	alive := 0
	for i := 0; i < n; i++ {
		ps := &p.Players[i]
		if !ps.Bankrupt() {
			alive++
		}
		base := slotOf(i) * slotWidth
		out.add(base+0, 1)
		if ps.Bankrupt() {
			continue
		}
		cash := float32(ps.Cash)
		out.add(base+1, 1)
		out.add(base+2, cash/1000)
		out.add(base+3, min(max(cash, 0), 300)/300)
		out.add(base+4, float32(f.worth[i]/3000))
		if ps.InJail() {
			out.add(base+5, 1)
			out.add(base+6, float32(ps.JailTurns)/3)
		}
		out.add(base+7, float32(ps.JailCards))
		if int(p.ToMove) == i {
			out.add(base+8, 1)
		}
		out.add(base+9, float32(f.complete[i])/3)
		out.add(base+10, float32(f.houses[i])/12)
		out.add(base+11, float32(f.income[i]/100))
		out.add(base+12, float32(f.nextRent[i]/100))
		out.add(base+13, float32(f.rails[i])/4)
		out.add(base+14, float32(f.utils[i])/2)
		out.add(base+15, float32(ps.Owed)/1000)
		out.add(base+16, float32(ps.Lent)/1000)
		out.add(base+17, float32(f.mortg[i])/5)
		out.add(base+18, float32(f.dev3[i]))
		out.add(base+slotDense+int(ps.Pos)%engine.BoardSize, 1)
	}
	for k := 0; k < NumOwnable; k++ {
		ps := &p.Props[k]
		base := squaresBase + k*squareWidth
		owner := 0
		if ps.Owner >= 0 && int(ps.Owner) < n {
			owner = 1 + slotOf(int(ps.Owner))
		}
		out.add(base+owner, 1)
		out.add(base+7, float32(ps.Houses)/5)
		if ps.Houses == 5 {
			out.add(base+8, 1)
		}
		if ps.Mortgaged {
			out.add(base+9, 1)
		}
	}
	for g := 0; g < numGroups; g++ {
		base := groupsBase + g*groupWidth
		size := float32(len(groupSqs[g]))
		mine := f.gOwn[g][me]
		best, owned := 0, 0
		for o := 0; o < n; o++ {
			owned += f.gOwn[g][o]
			if o != me && f.gOwn[g][o] > best {
				best = f.gOwn[g][o]
			}
		}
		out.add(base+0, float32(mine)/size)
		out.add(base+1, float32(best)/size)
		if f.gOwner[g] == me {
			out.add(base+2, 1)
		} else if f.gOwner[g] >= 0 {
			out.add(base+3, 1)
		}
		if f.gOwner[g] >= 0 {
			out.add(base+4, float32(f.gLevel[g])/5)
		}
		out.add(base+5, (size-float32(owned))/size)
	}
	housesLeft, hotelsLeft := 32, 12
	for k := 0; k < NumOwnable; k++ {
		if h := p.Props[k].Houses; h == 5 {
			hotelsLeft--
		} else {
			housesLeft -= int(h)
		}
	}
	out.add(globalBase+0, float32(p.Turns)/400)
	out.add(globalBase+1, float32(housesLeft)/32)
	out.add(globalBase+2, float32(hotelsLeft)/12)
	out.add(globalBase+3, float32(alive)/6)
	if p.Rules&RuleLoans != 0 {
		out.add(globalBase+4, 1)
	}
	if p.Rules&RuleJackpot != 0 {
		out.add(globalBase+5, 1)
	}
	if p.Rules&RuleNoAuctions != 0 {
		out.add(globalBase+6, 1)
	}
	out.add(globalBase+7, float32(p.Pot)/500)
}
