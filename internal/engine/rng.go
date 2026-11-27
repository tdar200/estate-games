package engine

import (
	"math"
	"math/rand/v2"
)

// RNG supplies uniformly distributed floats in [0, 1), like JS Math.random.
// Every random decision in the engine consumes floats from an RNG with the same
// arithmetic as game.js, so a recorded float sequence replays identically.
type RNG interface {
	Float64() float64
}

// RNGFunc adapts a plain function to the RNG interface.
type RNGFunc func() float64

// Float64 implements RNG.
func (f RNGFunc) Float64() float64 { return f() }

// DefaultRNG is used when a nil RNG is passed (JS default: Math.random).
var DefaultRNG RNG = RNGFunc(rand.Float64)

func orDefault(rng RNG) RNG {
	if rng == nil {
		return DefaultRNG
	}
	return rng
}

// rollDie mirrors Math.floor(rng() * 6) + 1.
func rollDie(rng RNG) int {
	return int(math.Floor(rng.Float64()*6)) + 1
}

// shuffle is the Fisher–Yates shuffle from game.js; it returns a new slice.
func shuffle(items []string, rng RNG) []string {
	result := append([]string{}, items...)
	for index := len(result) - 1; index > 0; index-- {
		swapIndex := int(math.Floor(rng.Float64() * float64(index+1)))
		result[index], result[swapIndex] = result[swapIndex], result[index]
	}
	return result
}

// actionRNG is the RNG of the action in progress (DefaultRNG outside one).
func (g *Game) actionRNG() RNG {
	return orDefault(g.rng)
}
