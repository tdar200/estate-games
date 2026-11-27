package valuenet

import (
	"bytes"
	"math"
	"math/rand/v2"
	"testing"
)

// randomPosition makes a plausible random position (for unit tests).
func randomPosition(rng *rand.Rand) Position {
	var p Position
	n := 2 + rng.IntN(5)
	p.NPlayers = uint8(n)
	p.ToMove = uint8(rng.IntN(n))
	p.Turns = uint16(rng.IntN(500))
	for i := 0; i < n; i++ {
		ps := &p.Players[i]
		ps.Cash = int32(rng.IntN(3000))
		ps.Pos = uint8(rng.IntN(40))
		if rng.IntN(8) == 0 {
			ps.Flags |= flagInJail
			ps.JailTurns = uint8(rng.IntN(3))
		}
		if i > 0 && rng.IntN(6) == 0 {
			ps.Flags = flagBankrupt
		}
	}
	for k := range p.Props {
		owner := rng.IntN(n+1) - 1
		if owner >= 0 && p.Players[owner].Bankrupt() {
			owner = -1
		}
		p.Props[k].Owner = int8(owner)
		if owner >= 0 && rng.IntN(4) == 0 {
			p.Props[k].Houses = uint8(rng.IntN(6))
		} else if owner >= 0 && rng.IntN(6) == 0 {
			p.Props[k].Mortgaged = true
		}
	}
	return p
}

func TestRecordRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	buf := make([]byte, RecordSize)
	for i := 0; i < 200; i++ {
		r := Record{Game: uint32(i), Winner: int8(rng.IntN(3)), Pos: randomPosition(rng)}
		r.Pos.Players[0].Owed = uint16(rng.IntN(2000))
		r.Pos.Pot = uint16(rng.IntN(900))
		r.Encode(buf)
		var back Record
		if err := back.Decode(buf); err != nil {
			t.Fatal(err)
		}
		if back != r {
			t.Fatalf("round trip changed the record:\n%+v\n%+v", r, back)
		}
	}
}

func TestStationary(t *testing.T) {
	sum := 0.0
	for _, p := range landProb {
		sum += p
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("landing probabilities sum to %v", sum)
	}
	// Jail is the most visited square.
	for i, p := range landProb {
		if i != 10 && p >= landProb[10] {
			t.Fatalf("square %d (%.4f) at least as likely as jail (%.4f)", i, p, landProb[10])
		}
	}
}

func TestFeaturesInRange(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	var f Featurizer
	var x Sparse
	for i := 0; i < 300; i++ {
		p := randomPosition(rng)
		for me := 0; me < int(p.NPlayers); me++ {
			f.Features(&p, me, &x)
			seen := map[int32]bool{}
			for k, idx := range x.Idx {
				if idx < 0 || int(idx) >= NumFeatures {
					t.Fatalf("feature index %d out of range", idx)
				}
				if seen[idx] {
					t.Fatalf("feature %d set twice", idx)
				}
				seen[idx] = true
				if v := x.Val[k]; math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("feature %d = %v", idx, v)
				}
			}
		}
	}
}

func TestWinProbsSumToOne(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	e := NewEvaluator(NewNet(NumFeatures, 16, 8, 9))
	for i := 0; i < 100; i++ {
		p := randomPosition(rng)
		probs := e.WinProbs(&p)
		sum := 0.0
		for j, q := range probs {
			if j < int(p.NPlayers) && p.Players[j].Bankrupt() && q != 0 {
				t.Fatalf("bankrupt player has probability %v", q)
			}
			sum += q
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Fatalf("probabilities sum to %v", sum)
		}
	}
}

// TestGradient compares the backpropagated gradient with finite differences.
func TestGradient(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	net := NewNet(NumFeatures, 12, 6, 11)
	for i := range net.B1 {
		net.B1[i] = 0.1 // keep units active
	}
	for i := range net.B2 {
		net.B2[i] = 0.1
	}
	w := newGradWorker(net)
	var probs [MaxSlots]float64
	for trial := 0; trial < 5; trial++ {
		p := randomPosition(rng)
		win := 0
		r := Record{Winner: int8(win), Pos: p}
		clear(w.grad.Params)
		if _, ok := w.example(net, &r, true, 1, &probs); !ok {
			t.Fatal("example skipped")
		}
		loss := func() float64 {
			l, _ := w.example(net, &r, false, 0, &probs)
			return l
		}
		checked := 0
		for c := 0; c < 400 && checked < 60; c++ {
			i := rng.IntN(len(net.Params))
			g := float64(w.grad.Params[i])
			if g == 0 && rng.IntN(10) != 0 {
				continue
			}
			old := net.Params[i]
			const h = 1e-2
			net.Params[i] = old + h
			up := loss()
			net.Params[i] = old - h
			down := loss()
			net.Params[i] = old
			num := (up - down) / (2 * h)
			if math.Abs(num-g) > 2e-3+0.05*math.Abs(num) {
				t.Fatalf("param %d: backprop %.6f, numeric %.6f", i, g, num)
			}
			checked++
		}
	}
}

func TestModelFile(t *testing.T) {
	net := NewNet(NumFeatures, 8, 4, 3)
	var buf bytes.Buffer
	if err := net.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for i := range net.Params {
		if net.Params[i] != back.Params[i] {
			t.Fatal("model round trip changed a weight")
		}
	}
	if _, err := Default(); err != nil {
		t.Fatalf("embedded model: %v", err)
	}
}

// TestTrainingLearns trains on a synthetic rule (the richest player wins)
// and checks the loss falls well below chance.
func TestTrainingLearns(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	var recs []Record
	for i := 0; i < 3000; i++ {
		p := randomPosition(rng)
		best := -1
		for j := 0; j < int(p.NPlayers); j++ {
			if !p.Players[j].Bankrupt() && (best < 0 || p.Players[j].Cash > p.Players[best].Cash) {
				best = j
			}
		}
		recs = append(recs, Record{Game: uint32(i), Winner: int8(best), Pos: p})
	}
	net := NewNet(NumFeatures, 32, 16, 1)
	tr := NewTrainer(net, TrainConfig{Batch: 64, LR: 3e-3, Workers: 4})
	before := Evaluate(net, recs[2500:], 4)
	for epoch := 0; epoch < 6; epoch++ {
		tr.TrainEpoch(recs[:2500], Shuffle(2500, rng), 3e-3, nil)
	}
	after := Evaluate(net, recs[2500:], 4)
	t.Logf("log loss %.3f -> %.3f (uniform %.3f), accuracy %.2f", before.LogLoss, after.LogLoss, after.Uniform, after.Accuracy)
	if after.LogLoss > 0.7*after.Uniform {
		t.Fatalf("did not learn: log loss %.3f vs uniform %.3f", after.LogLoss, after.Uniform)
	}
}

func BenchmarkWinProbs(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 1))
	e := NewEvaluator(NewNet(NumFeatures, 128, 64, 1))
	ps := make([]Position, 64)
	for i := range ps {
		ps[i] = randomPosition(rng)
		ps[i].NPlayers = 4
		for j := 0; j < 4; j++ {
			ps[i].Players[j].Flags &^= flagBankrupt
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.WinProbs(&ps[i%len(ps)])
	}
}

// TestDropoutGradient checks the dropout backward pass against finite
// differences with the same dropout masks (fixed by reseeding the RNG).
func TestDropoutGradient(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 18))
	net := NewNet(NumFeatures, 12, 6, 13)
	for i := range net.B1 {
		net.B1[i] = 0.1
	}
	for i := range net.B2 {
		net.B2[i] = 0.1
	}
	w := newGradWorker(net)
	w.dropout = 0.3
	var probs [MaxSlots]float64
	p := randomPosition(rng)
	r := Record{Winner: 0, Pos: p}
	run := func(train bool) float64 {
		w.rng = rand.New(rand.NewPCG(5, 5)) // same masks every call
		l, _ := w.example(net, &r, train, 1, &probs)
		return l
	}
	clear(w.grad.Params)
	run(true)
	checked := 0
	for c := 0; c < 600 && checked < 40; c++ {
		i := rng.IntN(len(net.Params))
		g := float64(w.grad.Params[i])
		if g == 0 {
			continue
		}
		old := net.Params[i]
		const h = 1e-2
		net.Params[i] = old + h
		clear(w.grad.Params)
		up := run(true)
		net.Params[i] = old - h
		clear(w.grad.Params)
		down := run(true)
		net.Params[i] = old
		num := (up - down) / (2 * h)
		if math.Abs(num-g) > 2e-3+0.05*math.Abs(num) {
			t.Fatalf("param %d: backprop %.6f, numeric %.6f", i, g, num)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no gradient checked")
	}
}
