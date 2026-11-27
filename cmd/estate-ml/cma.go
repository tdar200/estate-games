package main

import (
	"math"
	"math/rand/v2"
	"sort"
)

// sepCMA is the separable (diagonal-covariance) CMA-ES of Ros and Hansen
// (2008), "A Simple Modification in CMA-ES Achieving Linear Time and Space
// Complexity". With ~60 noisy dimensions and a few thousand evaluations, a
// full covariance cannot be learned (its learning rate is ~1/n^2); the
// diagonal one learns ~n/3 times faster and still adapts a step size per knob.
// It works in normalised coordinates: every knob mapped to [0, 1] by its
// range. Samples outside the box are repaired (clamped) and the repaired
// points are used in the update.
type sepCMA struct {
	N      int       `json:"n"`
	Lambda int       `json:"lambda"`
	Mean   []float64 `json:"mean"`
	Sigma  float64   `json:"sigma"`
	C      []float64 `json:"c"`  // diagonal covariance
	PS     []float64 `json:"ps"` // evolution path for sigma
	PC     []float64 `json:"pc"` // evolution path for C
	Gen    int       `json:"gen"`

	// derived constants (recomputed by init)
	mu                    int
	weights               []float64
	mueff, cs, ds, cc, c1 float64
	cmu, chiN             float64
}

func newSepCMA(mean []float64, sigma float64, lambda int) *sepCMA {
	n := len(mean)
	s := &sepCMA{N: n, Lambda: lambda, Mean: append([]float64(nil), mean...), Sigma: sigma,
		C: make([]float64, n), PS: make([]float64, n), PC: make([]float64, n)}
	for i := range s.C {
		s.C[i] = 1
	}
	s.init()
	return s
}

func (s *sepCMA) init() {
	n := float64(s.N)
	s.mu = s.Lambda / 2
	s.weights = make([]float64, s.mu)
	sum := 0.0
	for i := range s.weights {
		s.weights[i] = math.Log(float64(s.mu)+0.5) - math.Log(float64(i+1))
		sum += s.weights[i]
	}
	sq := 0.0
	for i := range s.weights {
		s.weights[i] /= sum
		sq += s.weights[i] * s.weights[i]
	}
	s.mueff = 1 / sq
	s.cs = (s.mueff + 2) / (n + s.mueff + 5)
	s.ds = 1 + 2*math.Max(0, math.Sqrt((s.mueff-1)/(n+1))-1) + s.cs
	s.cc = (4 + s.mueff/n) / (n + 4 + 2*s.mueff/n)
	c1 := 2 / ((n+1.3)*(n+1.3) + s.mueff)
	cmu := math.Min(1-c1, 2*(s.mueff-2+1/s.mueff)/((n+2)*(n+2)+s.mueff))
	// Separable variant: learning rates scaled by (n+2)/3.
	scale := (n + 2) / 3
	s.c1 = math.Min(1, c1*scale)
	s.cmu = math.Min(1-s.c1, cmu*scale)
	s.chiN = math.Sqrt(n) * (1 - 1/(4*n) + 1/(21*n*n))
}

// sample draws lambda candidates (repaired into [0,1]^n).
func (s *sepCMA) sample(rng *rand.Rand) [][]float64 {
	out := make([][]float64, s.Lambda)
	for k := range out {
		x := make([]float64, s.N)
		for i := range x {
			x[i] = clamp01(s.Mean[i] + s.Sigma*math.Sqrt(s.C[i])*rng.NormFloat64())
		}
		out[k] = x
	}
	return out
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// update moves the distribution towards the best candidates (higher fitness
// is better).
func (s *sepCMA) update(xs [][]float64, fitness []float64) {
	order := make([]int, len(xs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return fitness[order[a]] > fitness[order[b]] })
	n := s.N
	yw := make([]float64, n) // weighted mean step in y space
	ys := make([][]float64, s.mu)
	for k := 0; k < s.mu; k++ {
		x := xs[order[k]]
		y := make([]float64, n)
		for i := range y {
			y[i] = (x[i] - s.Mean[i]) / s.Sigma
			yw[i] += s.weights[k] * y[i]
		}
		ys[k] = y
	}
	for i := range s.Mean {
		s.Mean[i] = clamp01(s.Mean[i] + s.Sigma*yw[i])
	}
	// Step-size path (in z = C^-1/2 y space).
	norm := 0.0
	csf := math.Sqrt(s.cs * (2 - s.cs) * s.mueff)
	for i := range s.PS {
		s.PS[i] = (1-s.cs)*s.PS[i] + csf*yw[i]/math.Sqrt(s.C[i])
		norm += s.PS[i] * s.PS[i]
	}
	norm = math.Sqrt(norm)
	s.Gen++
	hs := 0.0
	if norm/math.Sqrt(1-math.Pow(1-s.cs, 2*float64(s.Gen))) < (1.4+2/float64(n+1))*s.chiN {
		hs = 1
	}
	ccf := math.Sqrt(s.cc * (2 - s.cc) * s.mueff)
	for i := range s.PC {
		s.PC[i] = (1-s.cc)*s.PC[i] + hs*ccf*yw[i]
	}
	for i := range s.C {
		rankMu := 0.0
		for k := 0; k < s.mu; k++ {
			rankMu += s.weights[k] * ys[k][i] * ys[k][i]
		}
		s.C[i] = (1-s.c1-s.cmu)*s.C[i] +
			s.c1*(s.PC[i]*s.PC[i]+(1-hs)*s.cc*(2-s.cc)*s.C[i]) +
			s.cmu*rankMu
		s.C[i] = math.Max(1e-6, math.Min(16, s.C[i]))
	}
	s.Sigma *= math.Exp((s.cs / s.ds) * (norm/s.chiN - 1))
	s.Sigma = math.Max(0.005, math.Min(0.4, s.Sigma))
}
