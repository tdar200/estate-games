package server

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	estate "github.com/tdar200/estate-games"
	"rsc.io/qr"
)

func TestQRCodeSVG(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.PublicURL = "https://host.example.ts.net:8443" })
	tb := h.createTable()
	r := h.get("/api/rooms/" + tb.code + "/qr.svg")
	expectStatus(t, r, 200)
	if ct := r.header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("content type %q", ct)
	}
	want := "https://host.example.ts.net:8443/j/" + tb.code
	checkQR(t, r.raw, want)
	lan := h.get("/api/rooms/" + tb.code + "/qr.svg?lan=1")
	checkQR(t, lan.raw, "http://192.168.1.20:3210/j/"+tb.code)
	expectError(t, h.get("/api/rooms/ZZZZZ/qr.svg"), 404, msgRoomMissing)
}

// checkQR parses the SVG, rebuilds the module grid from the path and
// compares it with a fresh encoding, including the finder patterns.
func checkQR(t *testing.T, svg []byte, text string) {
	t.Helper()
	var doc struct {
		XMLName xml.Name `xml:"svg"`
		ViewBox string   `xml:"viewBox,attr"`
		Title   string   `xml:"title"`
		Paths   []struct {
			D string `xml:"d,attr"`
		} `xml:"path"`
	}
	if err := xml.Unmarshal(svg, &doc); err != nil {
		t.Fatalf("invalid SVG XML: %v", err)
	}
	if doc.Title != text {
		t.Fatalf("QR encodes %q, want %q", doc.Title, text)
	}
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	size := code.Size + 2*qrQuietZone
	if doc.ViewBox != "0 0 "+strconv.Itoa(size)+" "+strconv.Itoa(size) || len(doc.Paths) != 1 {
		t.Fatalf("viewBox %q paths %d", doc.ViewBox, len(doc.Paths))
	}
	grid := make([][]bool, size)
	for i := range grid {
		grid[i] = make([]bool, size)
	}
	re := regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)
	for _, m := range re.FindAllStringSubmatch(doc.Paths[0].D, -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		n, _ := strconv.Atoi(m[3])
		for i := 0; i < n; i++ {
			grid[y][x+i] = true
		}
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			want := code.Black(x-qrQuietZone, y-qrQuietZone)
			if grid[y][x] != want {
				t.Fatalf("module (%d,%d) = %v, want %v", x, y, grid[y][x], want)
			}
		}
	}
	// Finder pattern corners: 7x7 dark ring around a 3x3 dark core.
	for _, origin := range [][2]int{{0, 0}, {code.Size - 7, 0}, {0, code.Size - 7}} {
		for dy := 0; dy < 7; dy++ {
			for dx := 0; dx < 7; dx++ {
				ring := dx == 0 || dy == 0 || dx == 6 || dy == 6
				core := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
				if grid[origin[1]+dy+qrQuietZone][origin[0]+dx+qrQuietZone] != (ring || core) {
					t.Fatalf("finder pattern broken at %v+(%d,%d)", origin, dx, dy)
				}
			}
		}
	}
}

func TestServerInfo(t *testing.T) {
	h := newHarness(t)
	r := h.get("/api/server")
	expectStatus(t, r, 200)
	if r.str("lanURL") != "http://192.168.1.20:3210" || r.body["publicURL"] != nil || r.str("version") != "test" {
		t.Fatalf("server info %s", r.raw)
	}
}

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"web/table/index.html":    {Data: []byte("<title>table</title>")},
		"web/table/table.js":      {Data: []byte("// table")},
		"web/phone/index.html":    {Data: []byte("<title>phone</title>")},
		"web/shared/client.js":    {Data: []byte("// shared")},
		"web/fixtures/lobby.json": {Data: []byte(`{"a":1}`)},
		"public/index.html":       {Data: []byte(`<link href="/styles.css"><a href="/">home</a><script src="/app.js"></script><use href="/icons.svg#x"/>`)},
		"public/app.js":           {Data: []byte("history.replaceState(null, \"\", \"/\"); fetch(`/api/rooms`); x = '/icons.svg#a'; if (/does not exist/.test(m)) {}")},
		"public/styles.css":       {Data: []byte("a{background:url(/board-mark.svg)}")},
		"public/icons.svg":        {Data: []byte("<svg/>")},
		"public/board-mark.svg":   {Data: []byte("<svg/>")},
	}
}

func TestStaticRoutes(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.Assets = testAssets() })
	cases := []struct {
		path, contains, contentType string
		status                      int
	}{
		{"/", "table", "text/html; charset=utf-8", 200},
		{"/play", "phone", "text/html; charset=utf-8", 200},
		{"/j/ABCDE", "phone", "text/html; charset=utf-8", 200},
		{"/table/table.js", "// table", "text/javascript; charset=utf-8", 200},
		{"/table/", "table", "text/html; charset=utf-8", 200},
		{"/shared/client.js", "// shared", "text/javascript; charset=utf-8", 200},
		{"/fixtures/lobby.json", `{"a":1}`, "application/json; charset=utf-8", 200},
		{"/classic/", `href="/classic/styles.css"`, "text/html; charset=utf-8", 200},
		{"/classic/missing/route", `src="/classic/app.js"`, "text/html; charset=utf-8", 200},
		{"/classic/icons.svg", "<svg/>", "image/svg+xml", 200},
		{"/nothing", "Not found", "text/plain; charset=utf-8", 404},
		{"/shared/../../go.mod", "Not found", "text/plain; charset=utf-8", 404},
	}
	for _, c := range cases {
		r := h.get(c.path)
		if r.status != c.status || !strings.Contains(string(r.raw), c.contains) || r.header.Get("Content-Type") != c.contentType {
			t.Errorf("%s: status %d type %q body %q", c.path, r.status, r.header.Get("Content-Type"), r.raw)
		}
	}
	index := string(h.get("/classic/").raw)
	for _, want := range []string{`href="/classic/"`, `href="/classic/icons.svg#x"`} {
		if !strings.Contains(index, want) {
			t.Errorf("classic index missing %s: %s", want, index)
		}
	}
	if csp := h.get("/classic/").header.Get("Content-Security-Policy"); csp != classicCSP {
		t.Errorf("classic CSP %q", csp)
	}
	if csp := h.get("/").header.Get("Content-Security-Policy"); !strings.Contains(csp, "font-src 'self';") || strings.Contains(csp, "googleapis") || !strings.Contains(csp, "ws://") {
		t.Errorf("app CSP %q", csp)
	}
	app := string(h.get("/classic/app.js").raw)
	for _, want := range []string{`replaceState(null, "", "/classic/")`, "fetch(`/api/rooms`)", "'/classic/icons.svg#a'", "(/does not exist/"} {
		if !strings.Contains(app, want) {
			t.Errorf("classic app.js missing %s: %s", want, app)
		}
	}
	if css := string(h.get("/classic/styles.css").raw); !strings.Contains(css, "url(/classic/board-mark.svg)") {
		t.Errorf("classic css %s", css)
	}
	if r := h.do("GET", "/classic", nil); r.status != 200 || !strings.Contains(string(r.raw), "/classic/styles.css") {
		t.Errorf("/classic redirect: %d", r.status)
	}
}

func TestEmbeddedClassicClient(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.Assets = estate.Assets })
	index := string(h.get("/classic/").raw)
	if !strings.Contains(index, `href="/classic/styles.css"`) || strings.Contains(index, `"/styles.css"`) || strings.Contains(index, `"/app.js"`) {
		t.Fatalf("classic index not rewritten")
	}
	app := string(h.get("/classic/app.js").raw)
	if strings.Contains(app, `"/icons.svg`) || !strings.Contains(app, "/api/rooms") {
		t.Fatal("classic app.js not rewritten")
	}
	if r := h.get("/"); r.status != 200 || !strings.Contains(string(r.raw), "<html") {
		t.Fatalf("table index: %d", r.status)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, func(c *Config) { c.DataDir = dir; c.SaveDelay = 20e6 })
	tb := h.createTable()
	alice := h.device("Alice", "rocket", "198.51.100.81")
	bob := h.device("Bob", "teapot", "198.51.100.82")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Bob"}, bob.opt())
	h.act(tb.code, a.str("token"), map[string]any{"type": "start"})
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, dataFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), alice.secret) {
		t.Fatal("secrets must not be stored in clear")
	}
	var doc persistedData
	if err := json.Unmarshal(data, &doc); err != nil || doc.Devices[alice.id] == nil || doc.Devices[alice.id].Friends[bob.id] == nil {
		t.Fatalf("persisted data %s (%v)", data, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	// A new server sees the same devices and friendships.
	h2 := newHarness(t, func(c *Config) { c.DataDir = dir })
	f := h2.get("/api/friends", alice.opt())
	expectStatus(t, f, 200)
	if len(f.list) != 1 || dig(f.list, "0", "deviceId") != bob.id || dig(f.list, "0", "status") != "offline" {
		t.Fatalf("friends after restart %s", f.raw)
	}
	expectError(t, h2.get("/api/friends", asDevice(alice.id, bob.secret)), 401, msgDeviceInvalid)
}

func TestJSCoercions(t *testing.T) {
	for raw, want := range map[string]string{
		`"Ann"`: "Ann", `5`: "5", `0`: "", `true`: "true", `false`: "", `null`: "", `{}`: "[object Object]",
		`[1,null,"a"]`: "1,,a", `1.5`: "1.5", `1e21`: "1e+21", `""`: "",
	} {
		if got := jsStringOr(json.RawMessage(raw)); got != want {
			t.Errorf("String(%s || \"\") = %q, want %q", raw, got, want)
		}
	}
	if got, _ := cleanName("  \x01Abcdefghijklmnop "); got != "Abcdefghijkl" {
		t.Errorf("cleanName = %q", got)
	}
	if got, _ := cleanName("😀😀😀😀😀😀😀"); got != "😀😀😀😀😀😀" {
		t.Errorf("cleanName emoji = %q", got)
	}
	if _, err := cleanName("\x00\x1f "); err == nil || err.Error() != "Enter a player name" {
		t.Errorf("cleanName empty: %v", err)
	}
	if got := string(unescapeHTMLInJSON([]byte(`{"a":"\u003cb\u003e \\u003c \u0026"}`))); got != `{"a":"<b> \\u003c &"}` {
		t.Errorf("unescape = %s", got)
	}
}

func TestDiffEventsRentAndGo(t *testing.T) {
	board := []boardSpace{{Index: 0, Type: "go", Name: "PAYDAY"}, {Index: 3, Type: "property", Name: "Elm Street", Price: 60}}
	prev := &gameView{Code: "ABCDE", Status: "playing",
		Players: []playerView{{ID: "p1", Name: "Ann"}, {ID: "p2", Name: "Ann B"}},
		Log:     []logEntryView{{ID: "l1", Text: "Ann's turn"}},
		Turn:    &turnView{PlayerID: "p1"},
	}
	next := &gameView{Code: "ABCDE", Status: "playing", Players: prev.Players,
		Properties: map[string]propertyView{"3": {OwnerID: "p2"}},
		Turn:       &turnView{PlayerID: "p1", Dice: []int{2, 3}, Movements: []movementView{{ID: "m1", PlayerID: "p1", Path: []int{38, 39, 0, 1, 2, 3}}}},
		Log: []logEntryView{{ID: "l1", Text: "Ann's turn"}, {ID: "l2", Text: "Ann rolled 2 + 3 = 5"},
			{ID: "l3", Text: "Ann passed PAYDAY and collected $200"}, {ID: "l4", Text: "Ann landed on Elm Street"},
			{ID: "l5", Text: "Ann paid $10 (Elm Street rent)"}},
	}
	events := diffEvents(prev, next, board)
	var got []string
	for _, e := range events {
		data, _ := json.Marshal(e)
		got = append(got, string(data))
	}
	want := []string{
		`{"type":"dice","playerId":"p1","dice":[2,3]}`,
		`{"type":"move","playerId":"p1","path":[38,39,0,1,2,3]}`,
		`{"type":"pay","from":null,"to":"p1","amount":200,"reason":"go"}`,
		`{"type":"pay","from":"p1","to":"p2","amount":10,"reason":"rent","spaceIndex":3}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The longest matching name wins ("Ann B" is not "Ann").
	next2 := *next
	next2.Turn = prev.Turn
	next2.Log = append(append([]logEntryView(nil), prev.Log...), logEntryView{ID: "x", Text: "Ann B wins the game"})
	if ev := diffEvents(prev, &next2, board); len(ev) != 1 || ev[0].get("playerId") != "p2" {
		t.Fatalf("win event %v", ev)
	}
	// Games saved before the squares were renamed say "passed GO".
	next3 := *next
	next3.Turn = prev.Turn
	next3.Log = append(append([]logEntryView(nil), prev.Log...), logEntryView{ID: "y", Text: "Ann passed GO and collected $200"})
	if ev := diffEvents(prev, &next3, board); len(ev) != 1 || ev[0].get("reason") != "go" || ev[0].get("to") != "p1" {
		t.Fatalf("old GO wording: events %v", ev)
	}
}

// Review 4 Nov #9: eight distinct computer names, short enough for the TV's action card.
func TestAINamesEightShortDistinct(t *testing.T) {
	if len(aiNames) < maxPlayersInGame {
		t.Fatalf("%d names for %d seats", len(aiNames), maxPlayersInGame)
	}
	seen := map[string]bool{}
	for _, name := range aiNames {
		if seen[name] {
			t.Fatalf("duplicate name %q", name)
		}
		seen[name] = true
		if n := len([]rune(name)); n > 8 || strings.Contains(name, " ") {
			t.Fatalf("name %q: %d characters (8 at most, one word)", name, n)
		}
	}
}
