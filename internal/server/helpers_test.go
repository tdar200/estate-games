package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// testClock is real time plus an adjustable offset.
type testClock struct{ offset atomic.Int64 }

func (c *testClock) Now() time.Time          { return time.Now().Add(time.Duration(c.offset.Load())) }
func (c *testClock) Advance(d time.Duration) { c.offset.Add(int64(d)) }

type harness struct {
	t      *testing.T
	srv    *Server
	ts     *httptest.Server
	engine *fakeEngine
	clock  *testClock
}

type harnessOption func(*Config)

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	fake := newFakeEngine()
	clock := &testClock{}
	cfg := Config{Engine: fake, AI: &fakeQuoteAI{}, AIDelay: 5 * time.Millisecond, Clock: clock.Now, LANURL: "http://192.168.1.20:3210", Version: "test"}
	for _, opt := range opts {
		opt(&cfg)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return &harness{t: t, srv: srv, ts: ts, engine: fake, clock: clock}
}

type response struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
	list   []any
}

func (r response) str(path ...string) string {
	v := dig(r.body, path...)
	s, _ := v.(string)
	return s
}

// dig walks maps by key and arrays by numeric index strings.
func dig(v any, path ...string) any {
	for _, key := range path {
		switch node := v.(type) {
		case map[string]any:
			v = node[key]
		case []any:
			i := 0
			for _, ch := range key {
				i = i*10 + int(ch-'0')
			}
			if i >= len(node) {
				return nil
			}
			v = node[i]
		default:
			return nil
		}
	}
	return v
}

// request options: a client IP (sent as X-Forwarded-For, trusted from the
// loopback test client) and a device credential.
type reqOpt func(*http.Request)

func fromIP(ip string) reqOpt { return func(r *http.Request) { r.Header.Set("X-Forwarded-For", ip) } }
func asDevice(id, secret string) reqOpt {
	return func(r *http.Request) { r.Header.Set(deviceHeader, id+":"+secret) }
}

func (h *harness) do(method, path string, body any, opts ...reqOpt) response {
	h.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	case []byte:
		reader = bytes.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, h.ts.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, opt := range opts {
		opt(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := response{status: res.StatusCode, header: res.Header, raw: raw}
	if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			out.body, _ = v.(map[string]any)
			out.list, _ = v.([]any)
		}
	}
	return out
}

func (h *harness) post(path string, body any, opts ...reqOpt) response {
	h.t.Helper()
	return h.do(http.MethodPost, path, body, opts...)
}

func (h *harness) get(path string, opts ...reqOpt) response {
	h.t.Helper()
	return h.do(http.MethodGet, path, nil, opts...)
}

func expectStatus(t *testing.T, r response, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d: %s", r.status, status, r.raw)
	}
}

func expectError(t *testing.T, r response, status int, msg string) {
	t.Helper()
	expectStatus(t, r, status)
	if got := r.str("error"); got != msg {
		t.Fatalf("error %q, want %q", got, msg)
	}
}

// room helpers ------------------------------------------------------------------------

type seat struct {
	code, token, playerID string
}

func (h *harness) createRoom(name string, opts ...reqOpt) seat {
	h.t.Helper()
	r := h.post("/api/rooms", map[string]any{"name": name}, opts...)
	expectStatus(h.t, r, http.StatusCreated)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

func (h *harness) join(code, name string, opts ...reqOpt) seat {
	h.t.Helper()
	r := h.post("/api/rooms/"+code+"/join", map[string]any{"name": name}, opts...)
	expectStatus(h.t, r, http.StatusOK)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

func (h *harness) act(code, token string, action any, extra ...map[string]any) response {
	h.t.Helper()
	body := map[string]any{"token": token, "action": action}
	for _, e := range extra {
		for k, v := range e {
			body[k] = v
		}
	}
	return h.post("/api/rooms/"+code+"/action", body)
}

func (h *harness) state(code, token string) map[string]any {
	h.t.Helper()
	r := h.get("/api/rooms/" + code + "/state?token=" + token)
	expectStatus(h.t, r, http.StatusOK)
	return r.body
}

type device struct{ id, secret string }

func (d device) opt() reqOpt { return asDevice(d.id, d.secret) }

func (h *harness) device(name, token string, ip string) device {
	h.t.Helper()
	r := h.post("/api/device", map[string]any{"name": name, "tokenKind": token}, fromIP(ip))
	expectStatus(h.t, r, http.StatusCreated)
	return device{r.str("deviceId"), r.str("deviceSecret")}
}

// view returns the room's current game view (no HTTP, no rate limits).
func (h *harness) view(code string) *gameView {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	return h.srv.rooms[code].last
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// sockets ---------------------------------------------------------------------------------

type socket struct {
	t    *testing.T
	conn *websocket.Conn
}

func (h *harness) dial(path string, header http.Header) *socket {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(h.ts.URL, "http") + path
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { conn.CloseNow() })
	return &socket{t: h.t, conn: conn}
}

// next reads one message, failing after the timeout.
func (s *socket) next(timeout time.Duration) map[string]any {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := s.conn.Read(ctx)
	if err != nil {
		s.t.Fatalf("socket read: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		s.t.Fatalf("socket message %s: %v", data, err)
	}
	return msg
}

// until reads messages until one satisfies match.
func (s *socket) until(timeout time.Duration, match func(map[string]any) bool) (seen []map[string]any) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			s.t.Fatalf("no matching socket message; saw %d", len(seen))
		}
		msg := s.next(remaining)
		seen = append(seen, msg)
		if match(msg) {
			return seen
		}
	}
}

func (s *socket) send(v any) {
	s.t.Helper()
	data, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
		s.t.Fatal(err)
	}
}

func players(state map[string]any) []map[string]any {
	list, _ := state["players"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, p.(map[string]any))
	}
	return out
}

func playerByID(state map[string]any, id string) map[string]any {
	for _, p := range players(state) {
		if p["id"] == id {
			return p
		}
	}
	return nil
}
