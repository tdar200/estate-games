package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Moving a seat or the table screen to a NEW device needs a yes from the game
// (docs/API.md "Getting back in"). Coming back on the same device is
// automatic (/rejoin, or /resume from the browser that was the table); a new
// device asks, and gets in when:
//
//   - the host's phone or the table screen taps Approve (state.claims), or
//   - the person types the 4-digit code shown with that prompt (for a host
//     who lost their own phone: the code is on the table screen), or
//   - when neither the table nor the host is connected, any other player at
//     the game approves (they see the same prompt and code).
//
// A game nobody else could ever approve for (a solo game against computers, a
// table with only keyboard/controller seats) accepts the new device only from
// the address the seat or table last used.

const (
	claimTTL          = 5 * time.Minute // a request waits this long for an answer
	claimCollectTTL   = 2 * time.Minute // an approved request must be used within this
	claimDenyCooldown = time.Minute     // after a "no", the same device waits before asking again
	maxClaimsPerRoom  = 6
	maxClaimAttempts  = 5 // wrong codes before the request is refused
	// Wrong codes one game accepts within wrongCodeWindow, over every
	// request, device and address together. A request may be made again a
	// minute after a refusal, from new addresses, so the per-request limit
	// alone would let a stranger try codes until one fits. Once used up,
	// typed codes are refused unchecked until the window passes; the
	// approve / deny prompt still works.
	maxRoomWrongCodes = 10
	wrongCodeWindow   = 30 * time.Minute

	claimSeat  = "seat"
	claimTable = "table"

	claimPending  = "pending"
	claimApproved = "approved"
	claimDenied   = "denied"
)

var (
	msgClaimDenied     = "The game said no to this request. Ask the host, then try again in a minute."
	msgClaimWrongCode  = "That code doesn't match. Use the 4-digit code shown on the table screen or the host's phone."
	msgClaimCodesShut  = "Too many wrong codes for this game. Ask the host to let you in, or try a code again later."
	msgClaimMissing    = "That request has expired. Ask again."
	msgClaimsFull      = "Too many devices are asking to join this game. Try again in a few minutes."
	msgClaimNotAllowed = "Only the host or the table screen can answer this request"
	msgClaimDecision   = "Choose approve or deny"
	msgClaimNoApprover = "Nobody else at this game can let a new device in. Open it on the phone or browser you played on."
	msgTableNoApprover = "Nobody at this game can let a new table screen in. Open it from the same computer or network as before."
	msgClaimSeatOnline = "That seat is connected again, so the request was cancelled"
)

// claim is a pending, approved or refused request to move a seat or the
// table screen to a new device.
type claim struct {
	id         string
	kind       string // claimSeat or claimTable
	playerID   string // seat claims
	deviceID   string // who asked
	deviceName string // their device profile name, if any
	code       string // 4 digits, shown only to the people who can approve
	status     string
	createdAt  time.Time
	expiresAt  time.Time
	attempts   int
}

// claimView is a pending claim as approvers see it in state.claims.
type claimView struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	PlayerID   *string `json:"playerId"`
	PlayerName *string `json:"playerName"`
	DeviceName *string `json:"deviceName"`
	Code       string  `json:"code"`
	CreatedAt  int64   `json:"createdAt"`
	ExpiresAt  int64   `json:"expiresAt"`
}

// claimStatusView is what the asking device sees.
type claimStatusView struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	PlayerID  *string `json:"playerId"`
	Status    string  `json:"status"`
	ExpiresAt int64   `json:"expiresAt"`
}

// pendingEntry is the 202 answer to /reclaim or /resume while a request waits.
type pendingEntry struct {
	Pending bool            `json:"pending"`
	Claim   claimStatusView `json:"claim"`
}

// accepted marks a dispatch result that is sent with 202 Accepted.
type accepted struct{ value any }

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (c *claim) statusView() claimStatusView {
	return claimStatusView{ID: c.id, Kind: c.kind, PlayerID: stringPtr(c.playerID), Status: c.status, ExpiresAt: c.expiresAt.UnixMilli()}
}

func randomDigits(n int) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	limit := uint64(1)
	for i := 0; i < n; i++ {
		limit *= 10
	}
	return fmt.Sprintf("%0*d", n, binary.BigEndian.Uint64(b[:])%limit)
}

func (r *room) findClaim(id string) *claim {
	for _, c := range r.claims {
		if c.id == id {
			return c
		}
	}
	return nil
}

// claimBy is the latest request from deviceID for the same seat (or table).
func (r *room) claimBy(deviceID, kind, playerID string) *claim {
	for i := len(r.claims) - 1; i >= 0; i-- {
		c := r.claims[i]
		if c.deviceID == deviceID && c.kind == kind && c.playerID == playerID {
			return c
		}
	}
	return nil
}

func (r *room) removeClaim(target *claim) {
	kept := r.claims[:0]
	for _, c := range r.claims {
		if c != target {
			kept = append(kept, c)
		}
	}
	r.claims = kept
}

// dropClaims removes every request for a seat (or the table) once it has
// moved: the others are moot.
func (r *room) dropClaims(kind, playerID string) {
	kept := r.claims[:0]
	for _, c := range r.claims {
		if c.kind != kind || c.playerID != playerID {
			kept = append(kept, c)
		}
	}
	r.claims = kept
}

// expireClaims drops requests past their time and publishes the change.
// Requires s.mu.
func (s *Server) expireClaims(r *room, now time.Time) {
	kept := r.claims[:0]
	for _, c := range r.claims {
		if now.Before(c.expiresAt) {
			kept = append(kept, c)
		}
	}
	r.claims = kept
}

// hostOrTableOnline reports whether the table screen or the host player is
// connected (the primary approvers).
func (s *Server) hostOrTableOnline(r *room, now time.Time) bool {
	if r.table && s.tableOnline(r, now) {
		return true
	}
	for _, sess := range r.order {
		if sess.role == "player" && sess.isHost && sess.online(now) {
			return true
		}
	}
	return false
}

// activeHuman reports whether a player id is a person still in the game.
func (r *room) activeHuman(playerID string) bool {
	if r.info == nil {
		return false
	}
	p := r.info.player(playerID)
	return p != nil && p.Kind != "ai" && !p.Out
}

// canDecide reports whether a session may approve or refuse a claim.
func (s *Server) canDecide(r *room, sess *session, c *claim, now time.Time) bool {
	if c.status != claimPending || !now.Before(c.expiresAt) {
		return false
	}
	switch sess.role {
	case "table":
		return true
	case "player":
		if c.kind == claimSeat && sess.playerID == c.playerID {
			return true // the seat's owner is back: their call
		}
		if sess.isHost {
			return true
		}
		return r.activeHuman(sess.playerID) && !s.hostOrTableOnline(r, now)
	}
	return false
}

// approversExist reports whether anyone could ever answer a request: a table
// screen (for seats), or a session of another person still in the game.
func (r *room) approversExist(kind, playerID string) bool {
	for _, sess := range r.order {
		switch sess.role {
		case "table":
			if kind == claimSeat {
				return true
			}
		case "player":
			if (kind == claimTable || sess.playerID != playerID) && r.activeHuman(sess.playerID) {
				return true
			}
		}
	}
	return false
}

// seatAddress is the client address the seat's most recent session used.
func (r *room) seatAddress(playerID string) string {
	var latest *session
	for _, sess := range r.order {
		if sess.role == "player" && sess.playerID == playerID && sess.ip != "" && (latest == nil || sess.lastSeen.After(latest.lastSeen)) {
			latest = sess
		}
	}
	if latest == nil {
		return ""
	}
	return latest.ip
}

// requestClaim records or advances deviceID's request for a seat or the
// table. It reports granted when an approved request (or the right code)
// lets the device in now; the request is then used up. Requires s.mu.
func (s *Server) requestClaim(r *room, device *deviceRecord, kind, playerID, code string) (bool, *claim, error) {
	now := s.now()
	s.expireClaims(r, now)
	c := r.claimBy(device.ID, kind, playerID)
	if c != nil && c.status == claimDenied {
		return false, c, forbidden(msgClaimDenied)
	}
	if c != nil && c.status == claimApproved {
		r.removeClaim(c)
		s.commit(r)
		return true, c, nil
	}
	created := false
	if c == nil {
		pending := 0
		for _, other := range r.claims {
			if other.status == claimPending {
				pending++
			}
		}
		if pending >= maxClaimsPerRoom {
			return false, nil, tooMany(msgClaimsFull)
		}
		c = &claim{
			id: uid("claim"), kind: kind, playerID: playerID, deviceID: device.ID, deviceName: device.Name,
			code: s.freshClaimCode(r), status: claimPending, createdAt: now,
		}
		r.claims = append(r.claims, c)
		created = true
		s.log.Printf("room %s: a new device asks for %s", r.code, claimTarget(r, c))
	}
	c.expiresAt = now.Add(claimTTL)
	if code = strings.TrimSpace(code); code != "" {
		if r.codesShut(now) {
			// The request still stands, for the host or the table to answer.
			if created {
				s.commit(r)
			}
			return false, c, forbidden(msgClaimCodesShut)
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(c.code)) == 1 {
			r.removeClaim(c)
			s.commit(r)
			return true, c, nil
		}
		r.wrongCodes = append(r.wrongCodes, now)
		c.attempts++
		if c.attempts >= maxClaimAttempts {
			c.status = claimDenied
			c.expiresAt = now.Add(claimDenyCooldown)
			created = true
		}
		if created {
			s.commit(r)
		}
		return false, c, forbidden(msgClaimWrongCode)
	}
	if created {
		s.commit(r) // approvers see the new request
	}
	return false, c, nil
}

// codesShut reports whether this game has had maxRoomWrongCodes wrong codes
// within the last wrongCodeWindow, so typed codes are not checked now. Older
// wrong codes are forgotten here.
func (r *room) codesShut(now time.Time) bool {
	recent := r.wrongCodes[:0]
	for _, at := range r.wrongCodes {
		if now.Sub(at) < wrongCodeWindow {
			recent = append(recent, at)
		}
	}
	r.wrongCodes = recent
	return len(r.wrongCodes) >= maxRoomWrongCodes
}

func (s *Server) freshClaimCode(r *room) string {
	for {
		code := randomDigits(4)
		clash := false
		for _, c := range r.claims {
			clash = clash || c.code == code
		}
		if !clash {
			return code
		}
	}
}

func claimTarget(r *room, c *claim) string {
	if c.kind == claimTable {
		return "the table screen"
	}
	if p := r.info.player(c.playerID); p != nil {
		return p.Name + "'s seat"
	}
	return "a seat"
}

func pendingResult(c *claim) accepted {
	return accepted{pendingEntry{Pending: true, Claim: c.statusView()}}
}

// claimsFor lists the pending requests a session may answer (state.claims).
func (s *Server) claimsFor(r *room, sess *session, now time.Time) []claimView {
	var out []claimView
	for _, c := range r.claims {
		if !s.canDecide(r, sess, c, now) {
			continue
		}
		view := claimView{ID: c.id, Kind: c.kind, PlayerID: stringPtr(c.playerID), DeviceName: stringPtr(c.deviceName),
			Code: c.code, CreatedAt: c.createdAt.UnixMilli(), ExpiresAt: c.expiresAt.UnixMilli()}
		if p := r.info.player(c.playerID); p != nil {
			view.PlayerName = stringPtr(p.Name)
		}
		out = append(out, view)
	}
	return out
}

// claimsSignature is the part of presence that changes when requests come
// and go (so approvers get a fresh state).
func (r *room) claimsSignature() string {
	var b strings.Builder
	for _, c := range r.claims {
		b.WriteString(c.id + ":" + c.status + ";")
	}
	return b.String()
}

// claimStatus answers GET /api/rooms/{code}/claims/{id} for the device that asked.
func (s *Server) claimStatus(code, id string, device *deviceRecord) (claimStatusView, error) {
	r, err := s.requireRoom(code)
	if err != nil {
		return claimStatusView{}, err
	}
	s.expireClaims(r, s.now())
	c := r.findClaim(id)
	if c == nil || c.deviceID != device.ID {
		return claimStatusView{}, notFound(msgClaimMissing)
	}
	return c.statusView(), nil
}

// decideClaim answers POST /api/rooms/{code}/claims/{id} {token, decision}.
func (s *Server) decideClaim(code, id string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticate(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	now := s.now()
	s.expireClaims(r, now)
	c := r.findClaim(id)
	if c == nil {
		return nil, notFound(msgClaimMissing)
	}
	if !s.canDecide(r, sess, c, now) {
		return nil, forbidden(msgClaimNotAllowed)
	}
	switch decision, _ := body.exactString("decision"); decision {
	case "approve":
		c.status = claimApproved
		c.expiresAt = now.Add(claimCollectTTL)
		s.log.Printf("room %s: %s may move to a new device", r.code, claimTarget(r, c))
	case "deny":
		c.status = claimDenied
		c.expiresAt = now.Add(claimDenyCooldown)
	default:
		return nil, badRequest(msgClaimDecision)
	}
	sess.lastSeen = now
	r.updatedAt = now
	s.commit(r)
	return s.stateFor(r, sess)
}

// claimRoute matches /api/rooms/{code}/claims/{id}.
func claimRoute(method string, m []string) (apiRoute, bool) {
	switch method {
	case http.MethodGet:
		return apiRoute{op: "claim-status", code: m[1], claimID: m[2]}, true
	case http.MethodPost:
		return apiRoute{op: "claim-decide", code: m[1], claimID: m[2], readBody: true}, true
	}
	return apiRoute{}, false
}
