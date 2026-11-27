package server

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

const maxBodyUnits = 32_768 // server.js: body.length > 32768 -> 413

// classicCSP is the Content-Security-Policy of server.js (API and /classic/).
const classicCSP = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'self'; form-action 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'"

var reSafeHost = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]+$`)

// appCSP is the policy for the new table and phone apps: everything from this
// server (the fonts too, web/shared/fonts) and explicit same-host WebSocket
// URLs (older Safari does not match ws: to 'self').
func appCSP(host string) string {
	connect := "'self'"
	if reSafeHost.MatchString(host) {
		connect += " ws://" + host + " wss://" + host
	}
	return "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'self'; form-action 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data: blob:; media-src 'self' data: blob:; connect-src " + connect
}

func setHeaders(w http.ResponseWriter, csp string) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	h.Set("Content-Security-Policy", csp)
	h.Set("Cache-Control", "no-store")
}

func (s *Server) routes() http.Handler {
	return http.HandlerFunc(s.serve)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			s.log.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, recovered, debug.Stack())
			writeError(w, &httpError{status: http.StatusInternalServerError, msg: "Request failed"})
		}
	}()
	path := r.URL.Path
	isAPI := strings.HasPrefix(r.URL.EscapedPath(), "/api/")
	if isAPI || strings.HasPrefix(path, "/classic") || strings.HasPrefix(path, "/ws/") {
		setHeaders(w, classicCSP)
	} else {
		setHeaders(w, appCSP(r.Host))
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	switch {
	case isAPI:
		key, limit := apiRateKey(r)
		if !s.limiter.consume(key, limit, s.now()) {
			w.Header().Set("Retry-After", "60")
			writeError(w, tooMany(msgTooManyRequests))
			return
		}
		s.handleAPI(w, r)
	case strings.HasPrefix(path, "/ws/"):
		s.handleSocket(w, r)
	default:
		s.handleStatic(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	var data []byte
	switch v := value.(type) {
	case json.RawMessage:
		data = unescapeHTMLInJSON(v)
	default:
		encoded, err := marshalJS(value)
		if err != nil {
			status = http.StatusInternalServerError
			encoded = []byte(`{"error":"Request failed"}`)
		}
		data = encoded
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, err error) {
	msg := err.Error()
	if msg == "" {
		msg = "Request failed"
	}
	writeJSON(w, statusOf(err), map[string]string{"error": msg})
}

// readBody ports readJson from server.js: at most 32 768 UTF-16 units,
// empty body = {}, otherwise JSON.
func readBody(r *http.Request) (*jsBody, []byte, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 4*maxBodyUnits+1))
	if err != nil {
		return nil, nil, badRequest(err.Error())
	}
	if len(data) > 4*maxBodyUnits || utf16Len(data) > maxBodyUnits {
		return nil, nil, errBodyTooLarge
	}
	if len(data) == 0 {
		return &jsBody{fields: map[string]json.RawMessage{}}, nil, nil
	}
	body, err := parseJSBody(data)
	if err != nil {
		return nil, nil, err
	}
	return body, data, nil
}

var (
	reCompatRoom  = regexp.MustCompile(`(?i)^/api/rooms/([A-Z0-9]+)/(join|watch|state|mode|action|trade-quote|not-selling|ai|remove)$`)
	rePartyRoom   = regexp.MustCompile(`^/api/rooms/([A-Za-z0-9]+)/(seats|rejoin|reclaim|rematch|end|settings|standin|skip|summary|qr\.svg)$`)
	reTableResume = regexp.MustCompile(`^/api/tables/([A-Za-z0-9]+)/resume$`)
	reClaim       = regexp.MustCompile(`^/api/rooms/([A-Za-z0-9]+)/claims/([A-Za-z0-9_-]{1,40})$`)
	reRoomSocket  = regexp.MustCompile(`^/ws/rooms/([A-Za-z0-9]+)$`)
)

// apiRoute names an API operation and its HTTP status on success.
type apiRoute struct {
	op       string
	code     string
	claimID  string
	readBody bool
	created  bool
}

func matchAPI(method, path string) (apiRoute, bool) {
	if path == "/api/rooms" && method == http.MethodPost {
		return apiRoute{op: "create", readBody: true, created: true}, true
	}
	if m := reCompatRoom.FindStringSubmatch(path); m != nil {
		op := strings.ToLower(m[2])
		want := http.MethodPost
		if op == "state" {
			want = http.MethodGet
		}
		if method == want {
			return apiRoute{op: op, code: m[1], readBody: op != "state"}, true
		}
	}
	if m := rePartyRoom.FindStringSubmatch(path); m != nil {
		switch {
		case m[2] == "seats" && method == http.MethodPost:
			return apiRoute{op: "seats", code: m[1], readBody: true, created: true}, true
		case m[2] == "rematch" && method == http.MethodPost:
			return apiRoute{op: "rematch", code: m[1], readBody: true}, true
		case m[2] == "end" && method == http.MethodPost:
			return apiRoute{op: "end", code: m[1], readBody: true}, true
		case m[2] == "settings" && method == http.MethodPost:
			return apiRoute{op: "settings", code: m[1], readBody: true}, true
		case (m[2] == "standin" || m[2] == "skip") && method == http.MethodPost:
			return apiRoute{op: m[2], code: m[1], readBody: true}, true
		case m[2] == "rejoin" && method == http.MethodPost:
			return apiRoute{op: "rejoin", code: m[1], readBody: true}, true
		case m[2] == "reclaim" && method == http.MethodPost:
			return apiRoute{op: "reclaim", code: m[1], readBody: true}, true
		case m[2] == "summary" && method == http.MethodGet:
			return apiRoute{op: "summary", code: m[1]}, true
		case m[2] == "qr.svg" && method == http.MethodGet:
			return apiRoute{op: "qr", code: m[1]}, true
		}
	}
	if m := reClaim.FindStringSubmatch(path); m != nil {
		if route, ok := claimRoute(method, m); ok {
			return route, true
		}
	}
	if m := reTableResume.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		return apiRoute{op: "resume", code: m[1], readBody: true}, true
	}
	switch {
	case path == "/api/games" && method == http.MethodPost:
		return apiRoute{op: "games-create", readBody: true, created: true}, true
	case path == "/api/games" && method == http.MethodGet:
		return apiRoute{op: "games-list"}, true
	case path == "/api/tables" && method == http.MethodPost:
		return apiRoute{op: "tables", readBody: true, created: true}, true
	case path == "/api/device" && method == http.MethodPost:
		return apiRoute{op: "device-create", readBody: true, created: true}, true
	case path == "/api/device" && method == http.MethodPatch:
		return apiRoute{op: "device-update", readBody: true}, true
	case path == "/api/device" && method == http.MethodGet:
		return apiRoute{op: "device-get"}, true
	case path == "/api/friends" && method == http.MethodGet:
		return apiRoute{op: "friends"}, true
	case path == "/api/invites" && method == http.MethodPost:
		return apiRoute{op: "invite", readBody: true}, true
	case path == "/api/server" && method == http.MethodGet:
		return apiRoute{op: "server"}, true
	}
	return apiRoute{}, false
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	route, ok := matchAPI(r.Method, r.URL.EscapedPath())
	if !ok {
		writeError(w, errAPINotFound)
		return
	}
	body := &jsBody{fields: map[string]json.RawMessage{}}
	var rawBody []byte
	if route.readBody {
		var err error
		body, rawBody, err = readBody(r)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	if route.op == "qr" {
		s.handleQR(w, r, route.code)
		return
	}
	result, err := s.dispatch(r, route, body, rawBody)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if route.created {
		status = http.StatusCreated
	}
	if pending, ok := result.(accepted); ok {
		status, result = http.StatusAccepted, pending.value
	}
	writeJSON(w, status, result)
}

func (s *Server) dispatch(r *http.Request, route apiRoute, body *jsBody, rawBody []byte) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &httpError{status: http.StatusServiceUnavailable, msg: "The server is shutting down"}
	}
	ip := clientIP(r)
	s.reqIP = ip // sessions remember the address they were last used from (claims.go)
	defer func() { s.reqIP = "" }()
	switch route.op {
	case "create":
		device, _ := s.requestDevice(r, false)
		return s.createRoom(body, device, quotaFor(r, device))
	case "join":
		device, _ := s.requestDevice(r, false)
		return s.joinRoom(route.code, body, device)
	case "watch":
		device, _ := s.requestDevice(r, false)
		return s.watchRoom(route.code, body, device)
	case "state":
		token := r.URL.Query().Get("token")
		return s.roomState(route.code, token)
	case "mode":
		return s.setRole(route.code, body)
	case "action":
		return s.roomAction(route.code, body)
	case "trade-quote":
		return s.tradeQuote(route.code, body, rawBody)
	case "not-selling":
		return s.notSellingDeed(route.code, body)
	case "ai":
		return s.addAI(route.code, body)
	case "remove":
		return s.removePlayer(route.code, body)
	case "tables":
		device, _ := s.requestDevice(r, false)
		return s.createTable(body, device, quotaFor(r, device))
	case "seats":
		return s.addSeat(route.code, body)
	case "rematch":
		return s.rematch(route.code, body)
	case "end":
		return s.endGame(route.code, body)
	case "settings":
		return s.roomSettings(route.code, body)
	case "standin":
		return s.hostStandIn(route.code, body)
	case "skip":
		return s.hostSkip(route.code, body)
	case "rejoin":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		room, err := s.requireRoom(route.code)
		if err != nil {
			return nil, err
		}
		return s.rejoinRoom(room, device)
	case "reclaim":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		room, err := s.requireRoom(route.code)
		if err != nil {
			return nil, err
		}
		return s.reclaimSeat(room, device, body, ip)
	case "resume":
		device, _ := s.requestDevice(r, false)
		return s.resumeTable(route.code, device, body, ip)
	case "claim-status":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		return s.claimStatus(route.code, route.claimID, device)
	case "claim-decide":
		return s.decideClaim(route.code, route.claimID, body)
	case "games-create":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		return s.createGame(body, device, quotaFor(r, device))
	case "games-list":
		device, _ := s.requestDevice(r, false)
		return s.listGames(device), nil
	case "summary":
		return s.roomSummary(route.code)
	case "device-create":
		return s.registerDevice(body)
	case "device-update":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		return s.updateDevice(device, body)
	case "device-get":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		return s.deviceProfile(device, true), nil
	case "friends":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		return s.friendsList(device), nil
	case "invite":
		device, err := s.requireDevice(r)
		if err != nil {
			return nil, err
		}
		return s.sendInvite(device, body)
	case "server":
		var public any
		if s.cfg.PublicURL != "" {
			public = s.cfg.PublicURL
		}
		var lan any
		if s.cfg.LANURL != "" && privateAddress(ip) {
			lan = s.cfg.LANURL // the home network's address is nobody else's business
		}
		return map[string]any{"lanURL": lan, "publicURL": public, "version": s.cfg.Version}, nil
	}
	return nil, errAPINotFound
}

// joinBaseURL picks the address phones should open: the public URL when
// configured (unless ?lan=1), else the LAN URL, else the request host.
func (s *Server) joinBaseURL(r *http.Request) string {
	base := s.cfg.PublicURL
	if base == "" || r.URL.Query().Get("lan") == "1" {
		if s.cfg.LANURL != "" {
			base = s.cfg.LANURL
		}
	}
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return strings.TrimRight(base, "/")
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request, code string) {
	s.mu.Lock()
	rm := s.rooms[normalizeCode(code)]
	s.mu.Unlock()
	if rm == nil {
		writeError(w, notFound(msgRoomMissing))
		return
	}
	svg, err := qrSVG(s.joinBaseURL(r) + "/j/" + rm.code)
	if err != nil {
		writeError(w, &httpError{status: http.StatusInternalServerError, msg: "Could not draw the QR code"})
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(svg)
}

func (s *Server) handleSocket(w http.ResponseWriter, r *http.Request) {
	ip := addressKey(clientIP(r))
	if !s.wsLimiter.consume(ip+":ws", 60, s.now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, tooMany(msgTooManyRequests))
		return
	}
	if !localRequest(r) {
		s.mu.Lock()
		if s.sockets[ip] >= maxSocketsPerIP {
			s.mu.Unlock()
			writeError(w, tooMany("Too many open connections from this address"))
			return
		}
		s.sockets[ip]++
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			if s.sockets[ip]--; s.sockets[ip] <= 0 {
				delete(s.sockets, ip)
			}
			s.mu.Unlock()
		}()
	}
	// The HTTP server's read/write timeouts must not cut long-lived sockets:
	// clear the connection deadlines before it is hijacked.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	if r.URL.Path == "/ws/device" {
		s.handleDeviceSocket(w, r)
		return
	}
	if m := reRoomSocket.FindStringSubmatch(r.URL.Path); m != nil {
		s.handleRoomSocket(w, r, m[1])
		return
	}
	writeError(w, notFound("Not found"))
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, &httpError{status: http.StatusMethodNotAllowed, msg: "Method not allowed"})
		return
	}
	path := r.URL.Path
	if path == "/" && r.URL.Query().Get("room") != "" {
		// Old classic invite links (/?room=CODE) now live under /classic/.
		http.Redirect(w, r, "/classic/?room="+r.URL.Query().Get("room"), http.StatusFound)
		return
	}
	if path == "/classic" {
		target := "/classic/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	if path == "/favicon.ico" && (s.static == nil || !s.static.exists("web/favicon.ico")) {
		// Browsers ask for it on every page; there is no icon file.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if path == "/favicon.ico" {
		s.static.serveFile(w, r, "web/favicon.ico")
		return
	}
	if s.static != nil {
		if name, classic, ok := s.static.resolve(path); ok {
			if classic {
				setHeaders(w, classicCSP)
			}
			if s.static.serveFile(w, r, name) {
				return
			}
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, "Not found\n")
}
