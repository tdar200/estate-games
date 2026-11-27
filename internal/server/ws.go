package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	wsSendBuffer   = 64
	wsPingInterval = 30 * time.Second
	wsWriteTimeout = 10 * time.Second
	wsReadLimit    = 4096
	wsIdleTimeout  = 90 * time.Second
	// rttWindow is how many of a socket's reported round trips count towards
	// its player's latency (the lowest of them; docs/QUIZ-API.md §1.1).
	rttWindow = 8
	// maxRTT caps a reported round trip (ms); anything slower is no use to a
	// game anyway.
	maxRTT = 60_000
)

// wsConn is one open WebSocket. Messages are queued without blocking while
// the server lock is held; a writer goroutine drains the queue.
type wsConn struct {
	conn     *websocket.Conn
	send     chan []byte
	done     chan struct{}
	once     sync.Once
	room     *room  // room sockets
	token    string // room sockets: session token
	ip       string // room sockets: the client address (actions are rate limited by it)
	deviceID string // device sockets
	// rtts are the round trips the client reported with its last pings,
	// oldest first (at most rttWindow). Guarded by Server.mu.
	rtts []int
	// writeMu orders the writer's messages before fail's last message and
	// close frame.
	writeMu sync.Mutex
	// why is the first reason the socket closed (for the connection log);
	// written once, under once.
	why string
}

// noteRTT keeps a round trip the client reported. Requires Server.mu.
func (c *wsConn) noteRTT(ms int) {
	c.rtts = append(c.rtts, ms)
	if len(c.rtts) > rttWindow {
		c.rtts = c.rtts[len(c.rtts)-rttWindow:]
	}
}

// bestRTT is the lowest of the socket's recent round trips; false when it
// has reported none. Requires Server.mu.
func (c *wsConn) bestRTT() (int, bool) {
	if len(c.rtts) == 0 {
		return 0, false
	}
	return slices.Min(c.rtts), true
}

// closedBy closes the socket and records why, for the connection log.
func (c *wsConn) closedBy(code websocket.StatusCode, reason, why string) {
	c.once.Do(func() {
		c.why = why
		close(c.done)
		go c.conn.Close(code, reason)
	})
}

func newWSConn(c *websocket.Conn) *wsConn {
	return &wsConn{conn: c, send: make(chan []byte, wsSendBuffer), done: make(chan struct{})}
}

// enqueue queues a message; a viewer that cannot keep up is disconnected.
func (c *wsConn) enqueue(msg []byte) {
	select {
	case <-c.done:
	case c.send <- msg:
	default:
		c.close(websocket.StatusPolicyViolation, "too slow")
	}
}

func (c *wsConn) close(code websocket.StatusCode, reason string) {
	why := reason
	if why == "" {
		why = "closed by the server"
	}
	c.closedBy(code, reason, why)
}

// fail sends an error message and closes the socket (session removed, room
// expired).
func (c *wsConn) fail(msg string) {
	payload, _ := marshalJS(map[string]string{"type": "error", "error": msg})
	c.once.Do(func() {
		c.why = "error sent: " + msg
		close(c.done)
		go func() {
			// Wait for a write in progress, flush what is queued, then the
			// error, then close: the error always arrives before the close
			// frame (the writer may still be holding a queued message).
			c.writeMu.Lock()
			defer c.writeMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
			defer cancel()
			for queued := true; queued; {
				select {
				case m := <-c.send:
					if c.conn.Write(ctx, websocket.MessageText, m) != nil {
						c.conn.CloseNow()
						return
					}
				default:
					queued = false
				}
			}
			if c.conn.Write(ctx, websocket.MessageText, payload) != nil {
				c.conn.CloseNow()
				return
			}
			c.conn.Close(websocket.StatusPolicyViolation, truncateReason(msg))
		}()
	})
}

func truncateReason(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
}

// writeLoop drains the send queue and pings the peer.
func (c *wsConn) writeLoop(ctx context.Context) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			c.close(websocket.StatusGoingAway, "server shutting down")
			return
		case <-c.done:
			return
		case msg := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			c.writeMu.Lock()
			err := c.conn.Write(writeCtx, websocket.MessageText, msg)
			c.writeMu.Unlock()
			cancel()
			if err != nil {
				c.close(websocket.StatusGoingAway, "write failed")
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				c.close(websocket.StatusGoingAway, "ping failed")
				return
			}
		}
	}
}

// clientMessage is a message from a client (docs/API.md "Live updates"):
// {"type":"ping","t"?,"rtt"?} on any socket, and on a room socket
// {"type":"action","action","id","seatId"?}. Values are kept raw and read
// leniently: a ping with a nonsense t or rtt is still a ping.
type clientMessage struct {
	Type   string          `json:"type"`
	T      json.RawMessage `json:"t"`
	RTT    json.RawMessage `json:"rtt"`
	Action json.RawMessage `json:"action"`
	ID     json.RawMessage `json:"id"`
	SeatID json.RawMessage `json:"seatId"`
}

// readLoop hands each client message to handle, in order, until the socket
// closes. Anything that is not a JSON object is ignored.
func (c *wsConn) readLoop(ctx context.Context, handle func(clientMessage)) {
	c.conn.SetReadLimit(wsReadLimit)
	for {
		readCtx, cancel := context.WithTimeout(ctx, wsIdleTimeout)
		_, data, err := c.conn.Read(readCtx)
		cancel()
		if err != nil {
			why := "client went away: " + err.Error()
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				why = "no message from the client for " + wsIdleTimeout.String()
			case websocket.CloseStatus(err) != -1:
				why = "client closed it (" + websocket.CloseStatus(err).String() + ")"
			}
			c.closedBy(websocket.StatusNormalClosure, "", why)
			return
		}
		var msg clientMessage
		if json.Unmarshal(data, &msg) == nil {
			handle(msg)
		}
	}
}

// pong answers a ping with the server clock and the client's own t echoed
// back, so the client can time the round trip and sync its clock to the
// server's (docs/QUIZ-API.md §1.1). A ping without a usable t (older
// clients send a bare ping) gets now only.
func (s *Server) pong(msg clientMessage) []byte {
	reply := struct {
		Type string   `json:"type"`
		T    *float64 `json:"t,omitempty"`
		Now  int64    `json:"now"`
	}{Type: "pong", Now: s.now().UnixMilli()}
	if t, ok := jsonNumber(msg.T); ok {
		reply.T = &t
	}
	data, _ := marshalJS(reply)
	return data
}

// reportedRTT is a ping's rtt as whole milliseconds; false when it is
// missing or not a sensible number.
func reportedRTT(msg clientMessage) (int, bool) {
	rtt, ok := jsonNumber(msg.RTT)
	if !ok || rtt < 0 {
		return 0, false
	}
	return int(math.Round(min(rtt, maxRTT))), true
}

// jsonNumber reads a raw JSON value that should be a number.
func jsonNumber(raw json.RawMessage) (float64, bool) {
	var n float64
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) || json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	return n, true
}

// actionResult answers an action sent over a room socket.
type actionResult struct {
	Type  string          `json:"type"` // always "actionResult"
	ID    json.RawMessage `json:"id"`   // the client's id, echoed (null when it sent none)
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
}

// socketAction runs an action sent over a room socket exactly as POST
// /api/rooms/{code}/action with the socket's token: the same checks and the
// same per-address rate limit. It answers with an actionResult rather than
// an "error" message, which clients treat as the end of their session; the
// new state reaches every viewer as an ordinary state message first.
func (s *Server) socketAction(c *wsConn, msg clientMessage) []byte {
	reply := actionResult{Type: "actionResult", ID: msg.ID, OK: true}
	if err := s.runSocketAction(c, msg); err != nil {
		reply.OK, reply.Error = false, err.Error()
	}
	data, _ := marshalJS(reply)
	return data
}

func (s *Server) runSocketAction(c *wsConn, msg clientMessage) (err error) {
	key, limit := actionRateKey(c.ip)
	if !s.limiter.consume(key, limit, s.now()) {
		return tooMany(msgTooManyRequests)
	}
	token, _ := marshalJS(c.token)
	body := &jsBody{fields: map[string]json.RawMessage{"token": token}}
	if len(msg.Action) > 0 {
		body.fields["action"] = msg.Action
	}
	if len(msg.SeatID) > 0 {
		body.fields["seatId"] = msg.SeatID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Printf("panic in a socket action in room %s: %v\n%s", c.room.code, recovered, debug.Stack())
			err = errors.New("Request failed")
		}
	}()
	if s.closed {
		return errors.New("The server is shutting down")
	}
	s.reqIP = c.ip // as for a request: the session remembers the address (claims.go)
	defer func() { s.reqIP = "" }()
	_, err = s.roomAction(c.room.code, body)
	return err
}

// roomPing notes a room socket's ping: the session is alive, the room in
// use, and the round trip the client reported counts towards its player's
// latency (playerLatency).
func (s *Server) roomPing(rm *room, c *wsConn, msg clientMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := rm.sessions[c.token]; current != nil {
		current.lastSeen = s.now()
	}
	rm.updatedAt = s.now()
	if rtt, ok := reportedRTT(msg); ok {
		c.noteRTT(rtt)
	}
}

func (s *Server) acceptWS(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.originPatterns()})
}

// originPatterns allows the configured public and LAN hosts in addition to
// the request host (Tailscale Funnel may forward a different Host).
func (s *Server) originPatterns() []string {
	var patterns []string
	for _, raw := range []string{s.cfg.PublicURL, s.cfg.LANURL} {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			patterns = append(patterns, u.Host, u.Hostname())
		}
	}
	return patterns
}

func messageJSON(kind, key string, value json.RawMessage) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"`)
	b.WriteString(kind)
	b.WriteString(`","`)
	b.WriteString(key)
	b.WriteString(`":`)
	b.Write(value)
	b.WriteString("}")
	return []byte(b.String())
}

// broadcast sends events, then each viewer's own state, to every room
// socket. Requires s.mu.
func (s *Server) broadcast(r *room, events []event) {
	if len(r.conns) == 0 {
		return
	}
	var eventMsgs [][]byte
	for _, e := range events {
		data, err := marshalJS(e)
		if err == nil {
			eventMsgs = append(eventMsgs, messageJSON("event", "event", data))
		}
	}
	s.broadcastMessages(r, eventMsgs)
}

// broadcastMessages sends ready-made event messages, then each viewer's own
// state, to every room socket. Requires s.mu.
func (s *Server) broadcastMessages(r *room, eventMsgs [][]byte) {
	if len(r.conns) == 0 {
		return
	}
	cache := map[*session][]byte{}
	for c := range r.conns {
		sess := r.sessions[c.token]
		if sess == nil {
			c.fail(msgSessionInvalid)
			continue
		}
		for _, msg := range eventMsgs {
			c.enqueue(msg)
		}
		msg, ok := cache[sess]
		if !ok {
			state, err := s.stateFor(r, sess)
			if err != nil {
				s.log.Printf("room %s: state failed: %v", r.code, err)
				continue
			}
			msg = messageJSON("state", "state", state)
			cache[sess] = msg
		}
		c.enqueue(msg)
	}
}

// handleRoomSocket serves GET /ws/rooms/{code}?token=.
func (s *Server) handleRoomSocket(w http.ResponseWriter, r *http.Request, code string) {
	conn, err := s.acceptWS(w, r)
	if err != nil {
		return
	}
	c := newWSConn(conn)
	token := r.URL.Query().Get("token")

	// Joining and leaving run under s.mu through underLock, which releases
	// the lock even if presence or state code panics: a panic here used to
	// leave the server locked for every request (serve recovers, but never
	// unlocked).
	var (
		rm         *room
		authErr    error
		shutting   bool
		registered bool
		who        string
	)
	ok := s.underLock("opening a room socket", func() {
		if s.closed {
			shutting = true
			return
		}
		var sess *session
		rm, sess, authErr = s.authenticate(code, token)
		if authErr != nil {
			return
		}
		c.room = rm
		c.token = token
		c.ip = clientIP(r)
		rm.conns[c] = struct{}{}
		sess.conns++
		registered = true
		now := s.now()
		sess.lastSeen = now
		sess.ip = c.ip
		rm.updatedAt = now
		who = sess.role
		if sess.name != "" {
			who += " " + sess.name
		}
		if state, err := s.stateFor(rm, sess); err == nil {
			c.enqueue(messageJSON("state", "state", state))
		}
		s.touchPresence(rm)
		s.wg.Add(1) // last: done only when the socket is fully open
	})
	switch {
	case !ok:
		if registered {
			s.underLock("closing a room socket", func() { s.leaveRoomSocket(rm, c, token) })
		}
		conn.Close(websocket.StatusInternalError, "Request failed")
		return
	case shutting:
		conn.Close(websocket.StatusGoingAway, "server shutting down")
		return
	case authErr != nil:
		payload, _ := marshalJS(map[string]string{"type": "error", "error": authErr.Error()})
		ctx, cancel := context.WithTimeout(r.Context(), wsWriteTimeout)
		_ = conn.Write(ctx, websocket.MessageText, payload)
		cancel()
		conn.Close(websocket.StatusPolicyViolation, truncateReason(authErr.Error()))
		return
	}
	defer s.wg.Done()

	opened := time.Now()
	go c.writeLoop(s.ctx)
	c.readLoop(s.ctx, func(msg clientMessage) {
		switch msg.Type {
		case "ping":
			s.roomPing(rm, c, msg)
			c.enqueue(s.pong(msg))
		case "action":
			c.enqueue(s.socketAction(c, msg))
		}
	})
	<-c.done // readLoop has closed it; done is closed after why is written
	s.log.Printf("room %s: %s socket closed after %s: %s", rm.code, who, time.Since(opened).Round(time.Second), c.why)
	s.underLock("closing a room socket", func() { s.leaveRoomSocket(rm, c, token) })
}

// leaveRoomSocket forgets a closed room socket and publishes the presence
// change. The bookkeeping comes first, so a panic while publishing can't
// leave the seat counted as connected. Requires s.mu.
func (s *Server) leaveRoomSocket(rm *room, c *wsConn, token string) {
	if _, open := rm.conns[c]; !open {
		return
	}
	delete(rm.conns, c)
	if current := rm.sessions[token]; current != nil {
		current.conns--
		current.lastSeen = s.now()
		if current.conns == 0 {
			// Disconnected sockets mean offline now, not after the poll window.
			current.lastSeen = s.now().Add(-onlineWindow)
		}
	}
	if s.rooms[rm.code] == rm && !s.closed {
		s.touchPresence(rm)
	}
}

// handleDeviceSocket serves GET /ws/device (presence, invites, friends).
func (s *Server) handleDeviceSocket(w http.ResponseWriter, r *http.Request) {
	credential := r.Header.Get(deviceHeader)
	if credential == "" {
		credential = r.URL.Query().Get("device")
	}
	conn, err := s.acceptWS(w, r)
	if err != nil {
		return
	}
	c := newWSConn(conn)

	// As for room sockets, the locked parts run through underLock so a panic
	// can't leave s.mu held.
	var (
		authErr    error
		shutting   bool
		registered bool
	)
	ok := s.underLock("opening a device socket", func() {
		if s.closed {
			shutting = true
			return
		}
		device, err := s.deviceFromCredential(credential, addressKey(clientIP(r)))
		if err == nil && device == nil {
			err = unauthorized(msgDeviceNeeded)
		}
		if authErr = err; authErr != nil {
			return
		}
		c.deviceID = device.ID
		conns := s.deviceConns[device.ID]
		if conns == nil {
			conns = map[*wsConn]struct{}{}
			s.deviceConns[device.ID] = conns
		}
		wasOffline := len(conns) == 0
		conns[c] = struct{}{}
		registered = true
		device.LastSeenAt = s.now().UnixMilli()
		list := s.friendsList(device)
		if data, err := marshalJS(list); err == nil {
			c.enqueue(messageJSON("friends", "friends", data))
			s.friendSig[device.ID] = friendsSignature(list)
		}
		if wasOffline {
			s.pushFriendsAbout([]string{device.ID})
		}
		s.wg.Add(1) // last: done only when the socket is fully open
	})
	switch {
	case !ok:
		if registered {
			s.underLock("closing a device socket", func() { s.leaveDeviceSocket(c) })
		}
		conn.Close(websocket.StatusInternalError, "Request failed")
		return
	case shutting:
		conn.Close(websocket.StatusGoingAway, "server shutting down")
		return
	case authErr != nil:
		payload, _ := marshalJS(map[string]string{"type": "error", "error": authErr.Error()})
		ctx, cancel := context.WithTimeout(r.Context(), wsWriteTimeout)
		_ = conn.Write(ctx, websocket.MessageText, payload)
		cancel()
		conn.Close(websocket.StatusPolicyViolation, truncateReason(authErr.Error()))
		return
	}
	defer s.wg.Done()

	go c.writeLoop(s.ctx)
	c.readLoop(s.ctx, func(msg clientMessage) {
		if msg.Type != "ping" {
			return
		}
		s.underLock("a device ping", func() {
			if d := s.devices[c.deviceID]; d != nil {
				d.LastSeenAt = s.now().UnixMilli()
			}
		})
		c.enqueue(s.pong(msg))
	})
	s.underLock("closing a device socket", func() { s.leaveDeviceSocket(c) })
}

// leaveDeviceSocket forgets a closed device socket and tells friends when
// the device went offline. Requires s.mu.
func (s *Server) leaveDeviceSocket(c *wsConn) {
	if conns := s.deviceConns[c.deviceID]; conns != nil {
		delete(conns, c)
		if len(conns) == 0 {
			delete(s.deviceConns, c.deviceID)
			delete(s.friendSig, c.deviceID)
			if !s.closed {
				s.pushFriendsAbout([]string{c.deviceID})
			}
		}
	}
	if d := s.devices[c.deviceID]; d != nil {
		d.LastSeenAt = s.now().UnixMilli()
		s.markDirty()
	}
}

// pushToDevice sends a message to every open socket of a device.
func (s *Server) pushToDevice(deviceID string, msg []byte) int {
	sent := 0
	for c := range s.deviceConns[deviceID] {
		c.enqueue(msg)
		sent++
	}
	return sent
}
