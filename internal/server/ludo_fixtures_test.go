package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/ludo"
)

// TestLudoFixtures builds the Ludo screens' demo and test states
// (web/fixtures/ludo) with the real server: every state is what stateFor
// sends that viewer, extra player fields and room fields included. Each
// scene is staged on the engine under the server lock (computers never move
// on their own here: their pause is an hour), with seeded dice and bots, so
// the files come out the same every time. To rewrite them:
//
//	ESTATE_LUDO_FIXTURES=web/fixtures/ludo go test ./internal/server -run TestLudoFixtures
//
// (from the repository root). Without the variable the scenes are only built
// and checked.
func TestLudoFixtures(t *testing.T) {
	h := newHarness(t, withLudo(11), func(c *Config) { c.AIDelay = time.Hour; c.ludoPace = 5000 })
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Viewer      string `json:"viewer"`
	}
	var index []entry
	out := os.Getenv("ESTATE_LUDO_FIXTURES")
	write := func(name, description, viewer string, state json.RawMessage) {
		t.Helper()
		var probe map[string]any
		if err := json.Unmarshal(state, &probe); err != nil || probe["game"] != "ludo" {
			t.Fatalf("%s: not a ludo state", name)
		}
		for _, bad := range []string{`"rng"`, `"forced"`} {
			if strings.Contains(string(state), bad) {
				t.Fatalf("%s: server-only field %s in a client state", name, bad)
			}
		}
		index = append(index, entry{name, description, viewer})
		if out == "" {
			return
		}
		doc, _ := json.MarshalIndent(map[string]any{"state": json.RawMessage(state), "viewer": viewer, "meta": map[string]any{"description": description}}, "", "  ")
		if err := os.WriteFile(filepath.Join(out, name+".json"), append(doc, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// stage runs fn on the room under the lock with its timer stopped, then
	// returns the state each named session gets.
	stage := func(code string, fn func(r *room, g *ludo.Game), tokens ...string) []json.RawMessage {
		t.Helper()
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		r := h.srv.rooms[code]
		r.stopTimer()
		if fn != nil {
			fn(r, ludoOf(r).g)
			h.srv.commit(r)
			r.stopTimer()
		}
		var states []json.RawMessage
		for _, token := range tokens {
			st, err := h.srv.stateFor(r, r.sessions[token])
			if err != nil {
				t.Fatal(err)
			}
			states = append(states, st)
		}
		return states
	}
	// play advances the game with seeded bots (Normal for everyone) for n
	// actions, keeping out of finishing anybody.
	play := func(g *ludo.Game, n int, seed uint64) {
		bots := map[string]ludo.Bot{}
		for i, p := range g.Players {
			bots[p.ID], _ = ludo.NewBot(ludo.Normal, seed+uint64(i))
		}
		for i := 0; i < n && g.Status == ludo.StatusPlaying; i++ {
			c, err := ludo.Choose(bots[g.Current().ID], g)
			if err != nil {
				t.Fatal(err)
			}
			if err := g.Do(g.Current().ID, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	// toTurn plays on until it is id's roll.
	toTurn := func(g *ludo.Game, id string, seed uint64) {
		for i := 0; i < 400 && !(g.Current() != nil && g.Current().ID == id && g.Phase == ludo.PhaseRoll); i++ {
			play(g, 1, seed+uint64(i)*31)
		}
	}

	// ---- A PC table: Maya on her phone (host player), Kim on the keyboard, two computers.
	tb := h.post("/api/tables", map[string]any{"gameType": "ludo"})
	expectStatus(t, tb, http.StatusCreated)
	code, tableToken := tb.str("code"), tb.str("tableToken")
	maya := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Maya"}, h.device("Maya", "lighthouse", "198.51.100.231").opt())
	expectStatus(t, maya, http.StatusOK)
	mayaToken, mayaID := maya.str("token"), maya.str("playerId")
	expectStatus(t, h.addSeat(table{code, tableToken}, "keyboard", "Kim", "key", nil), http.StatusCreated)
	for _, level := range []string{"hard", "normal"} {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": tableToken, "difficulty": level}), http.StatusOK)
	}
	st := stage(code, nil, tableToken, mayaToken)
	write("ludo-lobby-table", "Lobby on the table screen: Maya's phone (host), Kim on the keyboard, two computers (Hard, Normal); default rules", "table", st[0])
	write("ludo-lobby-phone", "Lobby on Maya's phone: she hosts, rule chips and START", "Maya", st[1])

	expectStatus(t, h.act(code, tableToken, map[string]any{"type": "start"}), http.StatusOK)
	st = stage(code, func(r *room, g *ludo.Game) {
		play(g, 70, 1)
		toTurn(g, mayaID, 2)
	}, tableToken, mayaToken)
	write("ludo-roll-table", "Mid-game on the 4-arm board: Maya's turn to roll, pieces all round the board", "table", st[0])
	write("ludo-roll-phone", "Mid-game on Maya's phone: the big ROLL button", "Maya", st[1])

	st = stage(code, func(r *room, g *ludo.Game) {
		// A real choice: a 6 with pieces out and one still in the yard.
		me := g.Player(mayaID)
		me.Pieces[0] = ludo.Piece{Zone: ludo.ZoneTrack, Step: 14}
		me.Pieces[1] = ludo.Piece{Zone: ludo.ZoneTrack, Step: 31}
		me.Pieces[2] = ludo.Piece{Zone: ludo.ZoneYard}
		me.Pieces[3] = ludo.Piece{Zone: ludo.ZoneColumn, Step: 1}
		g.QueueDice(5)
		if err := g.Do(mayaID, ludo.Command{Type: "roll"}); err != nil {
			t.Fatal(err)
		}
	}, tableToken, mayaToken)
	write("ludo-move-table", "Maya rolled a 5: three pieces can move, highlighted on the board", "table", st[0])
	write("ludo-move-phone", "Maya's phone: tap one of the three highlighted pieces", "Maya", st[1])

	st = stage(code, func(r *room, g *ludo.Game) {
		// Maya captures: one of her pieces lands on another player's piece.
		me := g.Player(mayaID)
		var victim *ludo.Player
		for _, p := range g.Players {
			if p.ID != mayaID {
				victim = p
				break
			}
		}
		b := g.Board()
		square := b.Square(me.Arm, me.Pieces[0].Step+3)
		if b.Safe(square) {
			me.Pieces[0].Step++
			square = b.Square(me.Arm, me.Pieces[0].Step+3)
		}
		victim.Pieces[1] = ludo.Piece{Zone: ludo.ZoneTrack, Step: (square - b.Start(victim.Arm) + b.Ring) % b.Ring}
		g.Phase, g.Moves = ludo.PhaseRoll, nil
		g.QueueDice(3)
		if err := g.Do(mayaID, ludo.Command{Type: "roll"}); err != nil {
			t.Fatal(err)
		}
		if err := g.Do(mayaID, ludo.MoveCommand(0)); err != nil {
			t.Fatal(err)
		}
	}, tableToken, mayaToken)
	write("ludo-capture-table", "Maya just captured a piece (sent back to its yard) and rolls again", "table", st[0])
	write("ludo-capture-phone", "Maya's phone after her capture: ROLL AGAIN", "Maya", st[1])

	// Somebody else's turn, as Maya's phone sees it.
	st = stage(code, func(r *room, g *ludo.Game) {
		g.QueueDice(2)
		play(g, 2, 9) // Maya rolls a 2 and moves; the turn passes
		for g.Current() != nil && g.Current().ID == mayaID {
			play(g, 1, 10)
		}
	}, tableToken, mayaToken)
	write("ludo-waiting-table", "Another player's turn (a computer is next to roll)", "table", st[0])
	write("ludo-waiting-phone", "Maya's phone while someone else plays: whose turn it is, the board", "Maya", st[1])

	// The end: play the game out with every rule default.
	st = stage(code, func(r *room, g *ludo.Game) {
		play(g, 20000, 3)
	}, tableToken, mayaToken)
	write("ludo-finished-table", "Game over: final places, PLAY AGAIN / NEW TABLE", "table", st[0])
	write("ludo-finished-phone", "Game over on Maya's phone: her place and the standings", "Maya", st[1])

	// ---- A phone-hosted game for six on the 6-arm board, house rules on.
	sana := h.ludoGame("Sana", h.device("Sana", "rocket", "198.51.100.241"))
	for i, name := range []string{"Omar", "Leila"} {
		res := h.post("/api/rooms/"+sana.code+"/join", map[string]any{"name": name}, h.device(name, []string{"teapot", "bicycle"}[i], fmt.Sprintf("198.51.100.%d", 242+i)).opt())
		expectStatus(t, res, http.StatusOK)
	}
	for _, level := range []string{"easy", "normal", "hard"} {
		expectStatus(t, h.post("/api/rooms/"+sana.code+"/ai", map[string]any{"token": sana.token, "difficulty": level}), http.StatusOK)
	}
	expectStatus(t, h.act(sana.code, sana.token, map[string]any{"type": "settings", "settings": map[string]any{"pieces": 3, "blocks": true, "captureFirst": true}}), http.StatusOK)
	st = stage(sana.code, nil, sana.token)
	write("ludo-lobby6-phone", "Six players on Sana's phone (host): the 6-arm board, 3 pieces, blocks and capture-first on", "Sana", st[0])
	expectStatus(t, h.act(sana.code, sana.token, map[string]any{"type": "start"}), http.StatusOK)
	st = stage(sana.code, func(r *room, g *ludo.Game) {
		play(g, 160, 5)
		toTurn(g, sana.playerID, 6)
		g.QueueDice(6)
		if err := g.Do(sana.playerID, ludo.Command{Type: "roll"}); err != nil {
			t.Fatal(err)
		}
	}, sana.token)
	// A spectator's (no seat) view of the same game, as the table screen would show it.
	watch := h.post("/api/rooms/"+sana.code+"/watch", map[string]any{"name": "Guest"})
	expectStatus(t, watch, http.StatusOK)
	spectator := stage(sana.code, nil, watch.str("token"))
	write("ludo-six-phone", "Six players on the 6-arm board: Sana rolled a 6", "Sana", st[0])
	write("ludo-six-table", "Six players on the 6-arm board, as a spectator (or the PC) sees it", "spectator", spectator[0])

	if out != "" {
		doc, _ := json.MarshalIndent(index, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "index.json"), append(doc, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d ludo fixtures to %s", len(index), out)
	}
}
