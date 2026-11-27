package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// At a party table a player is shown in their chosen token's colour, so the key
// you pick purple on the join screen is purple on the board and your plate.
func TestTablePlayersUseTokenColour(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.11")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice", "tokenKind": "key", "deviceId": alice.id}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	pad := h.addSeat(tb, "controller", "Maya", "lighthouse", 0)
	expectStatus(t, pad, http.StatusCreated)

	st := h.state(tb.code, tb.token)
	if p := playerByID(st, a.str("playerId")); p["color"] != tokenColors["key"] {
		t.Fatalf("phone player colour %v, want key colour %s", p["color"], tokenColors["key"])
	}
	seats := st["localSeats"].([]any)
	padPlayer := dig(seats, "0", "playerId").(string)
	if p := playerByID(st, padPlayer); p["color"] != tokenColors["lighthouse"] {
		t.Fatalf("controller seat colour %v, want lighthouse colour %s", p["color"], tokenColors["lighthouse"])
	}
}

// Classic rooms keep the engine's seat colours (byte-compatibility with Node).
func TestClassicRoomKeepsSeatColours(t *testing.T) {
	h := newHarness(t)
	r := h.post("/api/rooms", map[string]any{"name": "Ann"})
	expectStatus(t, r, http.StatusCreated)
	if p := playerByID(r.body["state"].(map[string]any), r.str("playerId")); p["color"] != "#d33f49" {
		t.Fatalf("classic host colour %v, want the engine's first seat colour", p["color"])
	}
}

func TestLoadPaletteOverridesDefaults(t *testing.T) {
	assets := fstest.MapFS{palettePath: {Data: []byte(`{"tokens":{"key":"#123456","rocket":"not-a-colour","nope":"#FFFFFF"}}`)}}
	p := loadPalette(assets)
	if p["key"] != "#123456" {
		t.Fatalf("key = %q, want override", p["key"])
	}
	if p["rocket"] != tokenColors["rocket"] {
		t.Fatalf("invalid colour must be ignored, got %q", p["rocket"])
	}
	if _, ok := p["nope"]; ok {
		t.Fatal("unknown token kinds must be ignored")
	}
	if loadPalette(nil)["umbrella"] != tokenColors["umbrella"] {
		t.Fatal("nil assets must give defaults")
	}
}

// The embedded palette file and the built-in defaults agree, so a server built
// without assets colours players exactly like the web apps.
func TestDefaultsMatchPaletteFile(t *testing.T) {
	data, err := os.ReadFile("../../" + palettePath)
	if err != nil {
		t.Skip("palette file not found:", err)
	}
	var file struct {
		Tokens map[string]string `json:"tokens"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for kind, color := range tokenColors {
		if !strings.EqualFold(file.Tokens[kind], color) {
			t.Errorf("%s: default %s, palette.json %s", kind, color, file.Tokens[kind])
		}
	}
}
