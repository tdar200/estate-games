package valuenet2

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// BenchmarkWinProbsSizes: one evaluation of a 4-player position (every
// player's point of view) for two network sizes.
func BenchmarkWinProbsSizes(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 2))
	p := randomPosition(rng)
	p.NPlayers = 4
	for i := range p.Players[:4] {
		p.Players[i].Flags = 0
	}
	for _, size := range []struct{ h1, h2 int }{{128, 64}, {256, 128}} {
		e := NewEvaluator(NewNet(NumFeatures, size.h1, size.h2, 1))
		b.Run(fmt.Sprintf("%dx%d", size.h1, size.h2), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				e.WinProbs(&p)
			}
		})
	}
}
