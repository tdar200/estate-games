package valuenet2

import (
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"sync"

	"github.com/tdar200/estate-games/internal/engine"
)

// Net is a two-hidden-layer perceptron with ReLU units: features -> H1 ->
// H2 -> one score. All weights live in one slice (Params) so the trainer can
// treat them as a vector.
type Net struct {
	In, H1, H2 int
	Params     []float32
	W1, B1     []float32 // W1[i*H1+j]: input i -> unit j
	W2, B2     []float32 // W2[j*H2+k]
	W3, B3     []float32 // W3[k], B3[0]
}

// NewNet returns a net with He-initialised weights.
func NewNet(in, h1, h2 int, seed uint64) *Net {
	n := &Net{In: in, H1: h1, H2: h2}
	n.alloc()
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	// Inputs are sparse (a few hundred of NumFeatures non-zero): scale the
	// first layer by the typical fan-in rather than the nominal one.
	fan1 := 250.0
	for i := range n.W1 {
		n.W1[i] = float32(rng.NormFloat64() * math.Sqrt(2/fan1))
	}
	for i := range n.W2 {
		n.W2[i] = float32(rng.NormFloat64() * math.Sqrt(2/float64(h1)))
	}
	for i := range n.W3 {
		n.W3[i] = float32(rng.NormFloat64() * math.Sqrt(1/float64(h2)))
	}
	return n
}

func (n *Net) alloc() {
	sizes := []int{n.In * n.H1, n.H1, n.H1 * n.H2, n.H2, n.H2, 1}
	total := 0
	for _, s := range sizes {
		total += s
	}
	if len(n.Params) != total {
		n.Params = make([]float32, total)
	}
	views := make([][]float32, len(sizes))
	o := 0
	for i, s := range sizes {
		views[i] = n.Params[o : o+s : o+s]
		o += s
	}
	n.W1, n.B1, n.W2, n.B2, n.W3, n.B3 = views[0], views[1], views[2], views[3], views[4], views[5]
}

// Clone returns a deep copy.
func (n *Net) Clone() *Net {
	c := &Net{In: n.In, H1: n.H1, H2: n.H2, Params: append([]float32(nil), n.Params...)}
	c.alloc()
	return c
}

// forward computes the score of x, leaving the hidden activations in h1, h2
// (len H1, H2).
func (n *Net) forward(x *Sparse, h1, h2 []float32) float32 {
	H1, H2 := n.H1, n.H2
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
		if a <= 0 {
			h1[j] = 0
			continue
		}
		row := n.W2[j*H2 : j*H2+H2]
		for k := range h2 {
			h2[k] += a * row[k]
		}
	}
	out := n.B3[0]
	for k, a := range h2 {
		if a <= 0 {
			h2[k] = 0
			continue
		}
		out += a * n.W3[k]
	}
	return out
}

// Evaluator scores positions with a Net. It keeps scratch buffers: one per
// goroutine.
type Evaluator struct {
	net    *Net
	f      Featurizer
	x      Sparse
	h1, h2 []float32
	pos    Position
}

// NewEvaluator returns an evaluator for net.
func NewEvaluator(net *Net) *Evaluator {
	return &Evaluator{net: net, h1: make([]float32, net.H1), h2: make([]float32, net.H2),
		x: Sparse{Idx: make([]int32, 0, 512), Val: make([]float32, 0, 512)}}
}

// WinProbs returns every player's probability of winning p (index = player
// index in p; bankrupt players get 0).
func (e *Evaluator) WinProbs(p *Position) [MaxSlots]float64 {
	var probs [MaxSlots]float64
	n := int(p.NPlayers)
	e.f.prepare(p)
	best := math.Inf(-1)
	var scores [MaxSlots]float64
	alive := 0
	for i := 0; i < n; i++ {
		if p.Players[i].Bankrupt() {
			continue
		}
		alive++
		e.f.features(p, i, &e.x)
		scores[i] = float64(e.net.forward(&e.x, e.h1, e.h2))
		best = math.Max(best, scores[i])
	}
	if alive == 0 {
		return probs
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		if p.Players[i].Bankrupt() {
			continue
		}
		probs[i] = math.Exp(scores[i] - best)
		sum += probs[i]
	}
	for i := 0; i < n; i++ {
		probs[i] /= sum
	}
	return probs
}

// WinProb returns playerID's probability of winning g (1 if it has won, 0 if
// it is out).
func (e *Evaluator) WinProb(g *engine.Game, playerID string) float64 {
	if g.Status == engine.StatusFinished {
		if g.WinnerID == playerID {
			return 1
		}
		return 0
	}
	me := SlotOf(g, playerID)
	if me < 0 {
		// Bankrupt, unknown, or a game the network cannot read (more than
		// MaxSlots players still in; callers check Fits and evaluate such
		// games another way).
		return 0
	}
	if p := g.Players[playerIndexOf(g, playerID)]; p.Bankrupt {
		return 0
	}
	FillFromGame(&e.pos, g)
	return e.WinProbs(&e.pos)[me]
}

func playerIndexOf(g *engine.Game, id string) int {
	for i, p := range g.Players {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// ------------------------------------------------------------- model file

const modelMagic = "ESV2"

// Write saves the net (little-endian float32 after a small header).
func (n *Net) Write(w io.Writer) error {
	header := []uint32{FeatureVersion, uint32(n.In), uint32(n.H1), uint32(n.H2)}
	if _, err := io.WriteString(w, modelMagic); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, header); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, n.Params)
}

// Save writes the net to path.
func (n *Net) Save(path string) error {
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return err
	}
	if err := n.Write(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// Decode reads a net written by Write.
func Decode(data []byte) (*Net, error) {
	if len(data) < 20 || string(data[:4]) != modelMagic {
		return nil, errors.New("valuenet2: not a model file")
	}
	le := binary.LittleEndian
	version := le.Uint32(data[4:])
	if version != FeatureVersion {
		return nil, fmt.Errorf("valuenet2: model has feature version %d, code has %d", version, FeatureVersion)
	}
	n := &Net{In: int(le.Uint32(data[8:])), H1: int(le.Uint32(data[12:])), H2: int(le.Uint32(data[16:]))}
	if n.In != NumFeatures {
		return nil, fmt.Errorf("valuenet2: model has %d inputs, want %d", n.In, NumFeatures)
	}
	n.alloc()
	body := data[20:]
	if len(body) != 4*len(n.Params) {
		return nil, fmt.Errorf("valuenet2: model body is %d bytes, want %d", len(body), 4*len(n.Params))
	}
	for i := range n.Params {
		n.Params[i] = math.Float32frombits(le.Uint32(body[4*i:]))
	}
	return n, nil
}

// Load reads a model file.
func Load(path string) (*Net, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// The shipped network, if any, is models/gm.bin (copied in by hand once a
// training run has produced and gated one; see docs/ai/GRANDMASTER.md).
//
//go:embed models
var models embed.FS

var (
	defaultOnce sync.Once
	defaultNet  *Net
	defaultErr  error
)

// ErrNoModel is Default's error while no network is shipped.
var ErrNoModel = errors.New("valuenet2: no network shipped (models/gm.bin)")

// Default returns the embedded, trained model (ErrNoModel if none).
func Default() (*Net, error) {
	defaultOnce.Do(func() {
		data, err := models.ReadFile("models/gm.bin")
		if err != nil {
			defaultErr = ErrNoModel
			return
		}
		defaultNet, defaultErr = Decode(data)
	})
	return defaultNet, defaultErr
}
