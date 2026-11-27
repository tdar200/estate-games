package valuenet2

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"sync"
)

// Training: each example is a position and its game's winner. The net scores
// every active player from that player's point of view; a softmax over the
// scores is the predicted distribution of the winner, trained with the cross
// entropy (log loss) of the actual winner. Adam with decoupled weight decay,
// minibatches split over goroutines.

// TrainConfig sets the optimiser.
type TrainConfig struct {
	Batch       int
	Dropout     float64 // hidden-unit dropout rate in training (0 = none)
	LR          float64
	WeightDecay float64
	Workers     int
	// TargetMix is the weight of a record's search target in the training
	// target (the rest is the actual winner); 0 trains on winners only.
	TargetMix float64
}

// Trainer holds the optimiser state.
type Trainer struct {
	Net  *Net
	Cfg  TrainConfig
	M, V []float32
	Step int

	workers []*gradWorker
}

type gradWorker struct {
	f      Featurizer
	xs     [MaxSlots]Sparse
	h1, h2 [MaxSlots][]float32
	dh1    []float32
	dh2    []float32
	grad   *Net // gradient, same shape as the net
	loss   float64
	count  int
	right  int

	dropout float32 // training only
	mix     float64 // TrainConfig.TargetMix
	rng     *rand.Rand
}

// NewTrainer prepares Adam for net.
func NewTrainer(net *Net, cfg TrainConfig) *Trainer {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	t := &Trainer{Net: net, Cfg: cfg, M: make([]float32, len(net.Params)), V: make([]float32, len(net.Params))}
	for w := 0; w < cfg.Workers; w++ {
		gw := newGradWorker(net)
		gw.dropout = float32(cfg.Dropout)
		gw.mix = cfg.TargetMix
		gw.rng = rand.New(rand.NewPCG(uint64(w)+1, 0xd409))
		t.workers = append(t.workers, gw)
	}
	return t
}

func newGradWorker(net *Net) *gradWorker {
	w := &gradWorker{dh1: make([]float32, net.H1), dh2: make([]float32, net.H2)}
	for i := range w.h1 {
		w.h1[i] = make([]float32, net.H1)
		w.h2[i] = make([]float32, net.H2)
		w.xs[i] = Sparse{Idx: make([]int32, 0, 512), Val: make([]float32, 0, 512)}
	}
	w.grad = &Net{In: net.In, H1: net.H1, H2: net.H2}
	w.grad.alloc()
	return w
}

// example runs one record forward (and backward if train), accumulating the
// gradient scaled by scale.
func (w *gradWorker) example(net *Net, r *Record, train bool, scale float32, probs *[MaxSlots]float64) (loss float64, ok bool) {
	p := &r.Pos
	n := int(p.NPlayers)
	win := int(r.Winner)
	if win < 0 || win >= n || p.Players[win].Bankrupt() {
		return 0, false
	}
	w.f.prepare(p)
	var scores [MaxSlots]float64
	best := math.Inf(-1)
	for i := 0; i < n; i++ {
		if p.Players[i].Bankrupt() {
			continue
		}
		w.f.features(p, i, &w.xs[i])
		if train && w.dropout > 0 {
			scores[i] = float64(w.forwardDrop(net, &w.xs[i], w.h1[i], w.h2[i]))
		} else {
			scores[i] = float64(net.forward(&w.xs[i], w.h1[i], w.h2[i]))
		}
		best = math.Max(best, scores[i])
	}
	sum := 0.0
	*probs = [MaxSlots]float64{}
	for i := 0; i < n; i++ {
		if !p.Players[i].Bankrupt() {
			probs[i] = math.Exp(scores[i] - best)
			sum += probs[i]
		}
	}
	for i := 0; i < n; i++ {
		probs[i] /= sum
	}
	loss = -math.Log(math.Max(probs[win], 1e-12))
	if !train {
		return loss, true
	}
	var tgt [MaxSlots]float64
	tgt[win] = 1
	if w.mix > 0 {
		if tp, ok := r.TargetProbs(); ok {
			for i := range tgt {
				tgt[i] = (1-w.mix)*tgt[i] + w.mix*tp[i]
			}
		}
	}
	g := w.grad
	H1, H2 := net.H1, net.H2
	// With dropout, kept units were scaled by 1/(1-p) in the forward pass;
	// dropped ones are 0 and get no gradient (the a > 0 tests below).
	keep := float32(1)
	if w.dropout > 0 {
		keep = 1 / (1 - w.dropout)
	}
	for i := 0; i < n; i++ {
		if p.Players[i].Bankrupt() {
			continue
		}
		d := float32(probs[i]-tgt[i]) * scale
		h1, h2 := w.h1[i], w.h2[i]
		g.B3[0] += d
		for k, a := range h2 {
			if a > 0 {
				g.W3[k] += d * a
				w.dh2[k] = d * net.W3[k] * keep
			} else {
				w.dh2[k] = 0
			}
		}
		for k := range w.dh2 {
			g.B2[k] += w.dh2[k]
		}
		for j, a := range h1 {
			if a <= 0 {
				w.dh1[j] = 0
				continue
			}
			row := net.W2[j*H2 : j*H2+H2]
			grow := g.W2[j*H2 : j*H2+H2]
			s := float32(0)
			for k, dk := range w.dh2 {
				grow[k] += a * dk
				s += row[k] * dk
			}
			w.dh1[j] = s * keep
		}
		for j := range w.dh1 {
			g.B1[j] += w.dh1[j]
		}
		x := &w.xs[i]
		for k, idx := range x.Idx {
			v := x.Val[k]
			grow := g.W1[int(idx)*H1 : int(idx)*H1+H1]
			for j, dj := range w.dh1 {
				grow[j] += v * dj
			}
		}
	}
	return loss, true
}

// TrainEpoch runs one pass over recs in the order given by perm with
// learning rate lr, and returns the mean training loss.
func (t *Trainer) TrainEpoch(recs []Record, perm []int, lr float64, progress func(done int)) float64 {
	B := t.Cfg.Batch
	W := len(t.workers)
	totalLoss, totalN := 0.0, 0
	var wg sync.WaitGroup
	for start := 0; start < len(perm); start += B {
		end := min(len(perm), start+B)
		batch := perm[start:end]
		scale := float32(1) / float32(len(batch))
		for wi, w := range t.workers {
			wg.Add(1)
			go func(wi int, w *gradWorker) {
				defer wg.Done()
				clear(w.grad.Params)
				w.loss, w.count = 0, 0
				var probs [MaxSlots]float64
				for k := wi; k < len(batch); k += W {
					if l, ok := w.example(t.Net, &recs[batch[k]], true, scale, &probs); ok {
						w.loss += l
						w.count++
					}
				}
			}(wi, w)
		}
		wg.Wait()
		for _, w := range t.workers {
			totalLoss += w.loss
			totalN += w.count
		}
		t.adam(lr)
		if progress != nil {
			progress(end)
		}
	}
	if totalN == 0 {
		return 0
	}
	return totalLoss / float64(totalN)
}

// adam sums the workers' gradients and applies one AdamW step, in parallel
// over chunks of the parameters.
func (t *Trainer) adam(lr float64) {
	t.Step++
	const b1, b2, eps = 0.9, 0.999, 1e-8
	c1 := 1 - math.Pow(b1, float64(t.Step))
	c2 := 1 - math.Pow(b2, float64(t.Step))
	params := t.Net.Params
	nW := len(t.Net.W1) + len(t.Net.B1) // offsets of the weight blocks
	w2Start, w2End := nW, nW+len(t.Net.W2)
	w3Start := w2End + len(t.Net.B2)
	w3End := w3Start + len(t.Net.W3)
	isWeight := func(i int) bool {
		return i < len(t.Net.W1) || (i >= w2Start && i < w2End) || (i >= w3Start && i < w3End)
	}
	chunks := len(t.workers)
	size := (len(params) + chunks - 1) / chunks
	var wg sync.WaitGroup
	for c := 0; c < chunks; c++ {
		lo, hi := c*size, min(len(params), (c+1)*size)
		if lo >= hi {
			continue
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				g := float32(0)
				for _, w := range t.workers {
					g += w.grad.Params[i]
				}
				m := b1*float64(t.M[i]) + (1-b1)*float64(g)
				v := b2*float64(t.V[i]) + (1-b2)*float64(g)*float64(g)
				t.M[i], t.V[i] = float32(m), float32(v)
				step := lr * (m / c1) / (math.Sqrt(v/c2) + eps)
				if isWeight(i) && t.Cfg.WeightDecay > 0 {
					step += lr * t.Cfg.WeightDecay * float64(params[i])
				}
				params[i] -= float32(step)
			}
		}(lo, hi)
	}
	wg.Wait()
}

// Metrics summarise predictions on held-out positions.
type Metrics struct {
	N           int       `json:"n"`
	LogLoss     float64   `json:"logLoss"`
	Uniform     float64   `json:"uniformLogLoss"` // log loss of guessing 1/players
	Accuracy    float64   `json:"accuracy"`       // the most likely player won
	Brier       float64   `json:"brier"`          // mean (p - won)^2 over active players
	Calibration []CalBin  `json:"calibration"`
	ByStage     []Segment `json:"byStage"`
	ByPlayers   []Segment `json:"byPlayers"`
}

// CalBin is one bucket of predicted probabilities.
type CalBin struct {
	Lo, Hi    float64
	Count     int
	Predicted float64 // mean predicted probability
	Observed  float64 // share that won
}

// Segment is the log loss and accuracy of a subset.
type Segment struct {
	Name     string  `json:"name"`
	N        int     `json:"n"`
	LogLoss  float64 `json:"logLoss"`
	Uniform  float64 `json:"uniformLogLoss"`
	Accuracy float64 `json:"accuracy"`
}

// Evaluate computes metrics of net on recs with workers goroutines.
func Evaluate(net *Net, recs []Record, workers int) Metrics {
	workers = max(1, workers)
	type acc struct {
		n, right       int
		loss, uni, bri float64
		briN           int
		cal            [10]struct {
			n, won int
			p      float64
		}
		stage [5]struct {
			n, right  int
			loss, uni float64
		}
		players [MaxSlots + 1]struct {
			n, right  int
			loss, uni float64
		}
	}
	accs := make([]acc, workers)
	var wg sync.WaitGroup
	for wi := 0; wi < workers; wi++ {
		wg.Add(1)
		go func(wi int) {
			defer wg.Done()
			w := newGradWorker(net)
			a := &accs[wi]
			var probs [MaxSlots]float64
			for k := wi; k < len(recs); k += workers {
				r := &recs[k]
				loss, ok := w.example(net, r, false, 0, &probs)
				if !ok {
					continue
				}
				n := int(r.Pos.NPlayers)
				alive := 0
				best := -1
				for i := 0; i < n; i++ {
					if r.Pos.Players[i].Bankrupt() {
						continue
					}
					alive++
					if best < 0 || probs[i] > probs[best] {
						best = i
					}
					won := 0.0
					if i == int(r.Winner) {
						won = 1
					}
					a.bri += (probs[i] - won) * (probs[i] - won)
					a.briN++
					b := min(9, int(probs[i]*10))
					a.cal[b].n++
					a.cal[b].p += probs[i]
					if won == 1 {
						a.cal[b].won++
					}
				}
				right := 0
				if best == int(r.Winner) {
					right = 1
				}
				uni := math.Log(float64(alive))
				a.n++
				a.right += right
				a.loss += loss
				a.uni += uni
				s := stageOf(int(r.Pos.Turns))
				a.stage[s].n++
				a.stage[s].right += right
				a.stage[s].loss += loss
				a.stage[s].uni += uni
				a.players[n].n++
				a.players[n].right += right
				a.players[n].loss += loss
				a.players[n].uni += uni
			}
		}(wi)
	}
	wg.Wait()
	var tot acc
	for _, a := range accs {
		tot.n += a.n
		tot.right += a.right
		tot.loss += a.loss
		tot.uni += a.uni
		tot.bri += a.bri
		tot.briN += a.briN
		for b := range a.cal {
			tot.cal[b].n += a.cal[b].n
			tot.cal[b].won += a.cal[b].won
			tot.cal[b].p += a.cal[b].p
		}
		for s := range a.stage {
			tot.stage[s].n += a.stage[s].n
			tot.stage[s].right += a.stage[s].right
			tot.stage[s].loss += a.stage[s].loss
			tot.stage[s].uni += a.stage[s].uni
		}
		for s := range a.players {
			tot.players[s].n += a.players[s].n
			tot.players[s].right += a.players[s].right
			tot.players[s].loss += a.players[s].loss
			tot.players[s].uni += a.players[s].uni
		}
	}
	m := Metrics{N: tot.n}
	if tot.n == 0 {
		return m
	}
	m.LogLoss = tot.loss / float64(tot.n)
	m.Uniform = tot.uni / float64(tot.n)
	m.Accuracy = float64(tot.right) / float64(tot.n)
	m.Brier = tot.bri / float64(max(1, tot.briN))
	for b, c := range tot.cal {
		bin := CalBin{Lo: float64(b) / 10, Hi: float64(b+1) / 10, Count: c.n}
		if c.n > 0 {
			bin.Predicted = c.p / float64(c.n)
			bin.Observed = float64(c.won) / float64(c.n)
		}
		m.Calibration = append(m.Calibration, bin)
	}
	for s, c := range tot.stage {
		if c.n > 0 {
			m.ByStage = append(m.ByStage, Segment{stageNames[s], c.n, c.loss / float64(c.n), c.uni / float64(c.n), float64(c.right) / float64(c.n)})
		}
	}
	for s, c := range tot.players {
		if c.n > 0 {
			m.ByPlayers = append(m.ByPlayers, Segment{itoa(s) + " players", c.n, c.loss / float64(c.n), c.uni / float64(c.n), float64(c.right) / float64(c.n)})
		}
	}
	return m
}

var stageNames = [5]string{"turns 0-39", "turns 40-99", "turns 100-199", "turns 200-399", "turns 400+"}

func stageOf(turns int) int {
	switch {
	case turns < 40:
		return 0
	case turns < 100:
		return 1
	case turns < 200:
		return 2
	case turns < 400:
		return 3
	}
	return 4
}

func itoa(v int) string {
	if v < 10 {
		return string(rune('0' + v))
	}
	return string(rune('0'+v/10)) + string(rune('0'+v%10))
}

// SaveState writes the optimiser state (Adam moments and step).
func (t *Trainer) SaveState(path string) error {
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return err
	}
	err = binary.Write(f, binary.LittleEndian, int64(t.Step))
	if err == nil {
		err = binary.Write(f, binary.LittleEndian, t.M)
	}
	if err == nil {
		err = binary.Write(f, binary.LittleEndian, t.V)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// LoadState reads state written by SaveState.
func (t *Trainer) LoadState(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var step int64
	if err := binary.Read(f, binary.LittleEndian, &step); err != nil {
		return err
	}
	if err := binary.Read(f, binary.LittleEndian, t.M); err != nil {
		return err
	}
	if err := binary.Read(f, binary.LittleEndian, t.V); err != nil {
		return err
	}
	if step < 0 {
		return errors.New("valuenet2: bad optimiser state")
	}
	t.Step = int(step)
	return nil
}

// Shuffle returns a random permutation of n.
func Shuffle(n int, rng *rand.Rand) []int {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	rng.Shuffle(n, func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
	return perm
}

// forwardDrop is Net.forward with inverted dropout on both hidden layers
// (training only): each unit is zeroed with probability w.dropout and the
// kept ones are scaled by 1/(1-dropout).
func (w *gradWorker) forwardDrop(n *Net, x *Sparse, h1, h2 []float32) float32 {
	H1, H2 := n.H1, n.H2
	p := w.dropout
	scale := 1 / (1 - p)
	copy(h1, n.B1)
	for k, i := range x.Idx {
		v := x.Val[k]
		row := n.W1[int(i)*H1 : int(i)*H1+H1]
		for j := range h1 {
			h1[j] += v * row[j]
		}
	}
	copy(h2, n.B2)
	for j, a := range h1 {
		if a <= 0 || w.rng.Float32() < p {
			h1[j] = 0
			continue
		}
		a *= scale
		h1[j] = a
		row := n.W2[j*H2 : j*H2+H2]
		for k := range h2 {
			h2[k] += a * row[k]
		}
	}
	out := n.B3[0]
	for k, a := range h2 {
		if a <= 0 || w.rng.Float32() < p {
			h2[k] = 0
			continue
		}
		a *= scale
		h2[k] = a
		out += a * n.W3[k]
	}
	return out
}
