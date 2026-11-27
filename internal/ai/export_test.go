package ai

// UseEngineHotelRule lets the AI sell a hotel during a house shortage, as it
// does in real games (TestMain keeps game.js's rule for the parity tests;
// see gameJSHotelShortage). The returned function puts game.js's rule back.
// Not for parallel tests.
func UseEngineHotelRule() (restore func()) {
	gameJSHotelShortage = false
	return func() { gameJSHotelShortage = true }
}
