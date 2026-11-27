package hardai

// splitMix is a tiny, fast, seedable generator (SplitMix64). It implements
// engine.RNG and is used for dice in simulations and for the Easy level's
// mistakes. It is not safe for concurrent use.
type splitMix struct{ state uint64 }

func newSplitMix(seed uint64) *splitMix { return &splitMix{state: seed} }

func (s *splitMix) next() uint64 {
	s.state += 0x9e3779b97f4a7c15
	z := s.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Float64 returns a uniform float in [0, 1) (53 random bits).
func (s *splitMix) Float64() float64 {
	return float64(s.next()>>11) / (1 << 53)
}

// Intn returns a uniform int in [0, n); n must be positive.
func (s *splitMix) Intn(n int) int {
	return int(s.next() % uint64(n))
}

// mix derives an independent 64-bit seed from a list of values.
func mix(values ...uint64) uint64 {
	h := uint64(0x6a09e667f3bcc909)
	for _, v := range values {
		h ^= v + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
		h = (h ^ (h >> 33)) * 0xff51afd7ed558ccd
		h = (h ^ (h >> 33)) * 0xc4ceb93fe53b5c4f
		h ^= h >> 33
	}
	return h
}

// hashString is FNV-1a, used to fold player ids into seeds.
func hashString(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}
