package ai

import (
	"os"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

// The fixture and trace tests compare this package with decisions recorded
// from src/ai.js on src/game.js rules (the engine replays the traces in its
// legacy mode), so every test here prices mortgages with game.js's maths and
// keeps game.js's hotel rule in a house shortage. TestFixedMortgageMaths
// checks the maths real games use, and the debt tests (debt_test.go) the
// hotel rule (UseEngineHotelRule).
func TestMain(m *testing.M) {
	gameJSMaths = true
	gameJSHotelShortage = true
	os.Exit(m.Run())
}

// Outside the parity tests the AI prices mortgages exactly as the engine.
func TestFixedMortgageMaths(t *testing.T) {
	gameJSMaths = false
	defer func() { gameJSMaths = true }()
	for _, index := range engine.OwnableIndexes {
		price := engine.Board[index].Price
		if got, want := unmortgageCost(price), engine.UnmortgageCost(price); got != want {
			t.Errorf("%s: unmortgage cost %d, engine %d", engine.Board[index].Name, got, want)
		}
		if got, want := transferFee(price), engine.MortgageTransferFee(price); got != want {
			t.Errorf("%s: transfer fee %d, engine %d", engine.Board[index].Name, got, want)
		}
	}
	if unmortgageCost(200) != 110 {
		t.Fatalf("a $200 deed costs %d to lift, want 110", unmortgageCost(200))
	}
}
