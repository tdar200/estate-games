package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	deviceHeader    = "X-Estate-Device"
	maxDevices      = 50_000
	newDevicesPerIP = 20 // new device records per address per minute
	maxRecentTables = 20
	invitesPerMin   = 10
	invitesPerPair  = 3
)

// tokenColors are the default colours of the eight tokens, the same as
// web/shared/palette.json (which overrides them at startup, see palette.go).
// Every colour is at least 19 CIEDE2000 from all eight property-group colours;
// anchor (Rose) and kite (Slate), added for 7-8 player games, are at least
// 20.4 from every group and 26.4 from every other token.
var tokenColors = map[string]string{
	"rocket":     "#1EA0FF", // Sky
	"teapot":     "#7A1838", // Wine
	"bicycle":    "#6F7A1A", // Olive
	"lighthouse": "#0E8C8C", // Teal
	"key":        "#8A5CF5", // Violet
	"umbrella":   "#1E2228", // Onyx
	"anchor":     "#D88890", // Rose
	"kite":       "#727078", // Slate
}

var (
	reDeviceID = regexp.MustCompile(`^dev_[A-Za-z0-9_-]{8,40}$`)
	reSecret   = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
	reColor    = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// deviceRecord is a phone or browser identity (no accounts). Only a hash of
// the secret is stored.
type deviceRecord struct {
	ID         string                 `json:"id"`
	SecretHash string                 `json:"secretHash"`
	Name       string                 `json:"name,omitempty"`
	TokenKind  string                 `json:"tokenKind,omitempty"`
	Color      string                 `json:"color,omitempty"`
	CreatedAt  int64                  `json:"createdAt"`
	LastSeenAt int64                  `json:"lastSeenAt"`
	Friends    map[string]*friendLink `json:"friends,omitempty"`
	Recent     []*recentTable         `json:"recent,omitempty"`
	// Played is what the quiz and Near Thing games this device hosted have
	// asked, by game type, least recently played first (played.go).
	Played map[string][]string `json:"played,omitempty"`
}

// friendLink is one side of a friendship ("people you've played with").
type friendLink struct {
	Since        int64  `json:"since"`
	LastPlayedAt int64  `json:"lastPlayedAt"`
	LastResult   string `json:"lastResult,omitempty"` // the friend's result in the last finished game together
	Games        int    `json:"games"`
	Name         string `json:"name,omitempty"` // friend's last in-game name (fallback)
	TokenKind    string `json:"tokenKind,omitempty"`
}

// recentTable is a game the device played.
type recentTable struct {
	Code       string   `json:"code"`
	GameType   string   `json:"gameType,omitempty"` // "estate" or "poker" (absent in records from before game types)
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt,omitempty"`
	Result     string   `json:"result,omitempty"` // "won" or "lost"
	Players    []string `json:"players"`
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// deviceFromCredential resolves "id:secret". An empty credential is no
// device. A well-formed but unknown id is adopted (so phones recover after
// the data file is lost), within the per-address limit on new devices; a
// known id with the wrong secret is rejected. Requires s.mu.
func (s *Server) deviceFromCredential(credential, ip string) (*deviceRecord, error) {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return nil, nil
	}
	id, secret, ok := strings.Cut(credential, ":")
	if !ok || !reDeviceID.MatchString(id) || !reSecret.MatchString(secret) {
		return nil, unauthorized(msgDeviceInvalid)
	}
	hash := hashSecret(secret)
	d := s.devices[id]
	if d == nil {
		if err := s.allowNewDevice(ip); err != nil {
			return nil, err
		}
		if err := s.makeRoomForDevice(); err != nil {
			return nil, err
		}
		now := s.now().UnixMilli()
		d = &deviceRecord{ID: id, SecretHash: hash, CreatedAt: now, LastSeenAt: now}
		s.devices[id] = d
		s.markDirty()
		return d, nil
	}
	if subtle.ConstantTimeCompare([]byte(d.SecretHash), []byte(hash)) != 1 {
		return nil, unauthorized(msgDeviceInvalid)
	}
	return d, nil
}

// requestDevice returns the request's device. With strict false an invalid
// credential is ignored (joining must not fail because of a stale device).
func (s *Server) requestDevice(r *http.Request, strict bool) (*deviceRecord, error) {
	d, err := s.deviceFromCredential(r.Header.Get(deviceHeader), addressKey(clientIP(r)))
	if err != nil && !strict {
		return nil, nil
	}
	if err == nil && d != nil {
		d.LastSeenAt = s.now().UnixMilli()
	}
	return d, err
}

func (s *Server) requireDevice(r *http.Request) (*deviceRecord, error) {
	d, err := s.requestDevice(r, true)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, unauthorized(msgDeviceNeeded)
	}
	return d, nil
}

// allowNewDevice limits how fast one address can mint device records (every
// one is stored on disk).
func (s *Server) allowNewDevice(ip string) error {
	if ip != "" && !s.deviceLimiter.consume("new:"+ip, newDevicesPerIP, s.now()) {
		return tooMany("Too many new devices from this address; please try again shortly")
	}
	return nil
}

// makeRoomForDevice keeps the device table under maxDevices: devices that
// never played with anyone, aren't connected or seated, and haven't been seen
// for a day (a month for those with friends or games) make way for new ones.
func (s *Server) makeRoomForDevice() error {
	if len(s.devices) < maxDevices {
		return nil
	}
	now := s.now()
	seated := map[string]bool{}
	for _, r := range s.rooms {
		for _, pair := range r.seatedDevices() {
			seated[pair[0]] = true
		}
	}
	idle := now.Add(-24 * time.Hour).UnixMilli()
	old := now.Add(-30 * 24 * time.Hour).UnixMilli()
	for id, d := range s.devices {
		if len(s.deviceConns[id]) > 0 || seated[id] {
			continue
		}
		used := len(d.Friends) > 0 || len(d.Recent) > 0
		if (!used && d.LastSeenAt < idle) || (len(d.Friends) == 0 && d.LastSeenAt < old) {
			delete(s.devices, id)
		}
	}
	if len(s.devices) >= maxDevices {
		return &httpError{status: http.StatusServiceUnavailable, msg: "The server has too many registered devices"}
	}
	s.markDirty()
	return nil
}

// deviceRetention is how long a device record (nickname, token, colour,
// friends, recent tables, quiz memory) is kept after the device was last seen.
// The privacy notice (web/privacy/index.html) states it.
const deviceRetention = 365 * 24 * time.Hour

// pruneDevices deletes the device records not seen for deviceRetention (unless
// connected or seated now), and the friend links other devices keep to them.
// Requires s.mu.
func (s *Server) pruneDevices(now time.Time) {
	cutoff := now.Add(-deviceRetention).UnixMilli()
	var seated map[string]bool
	var gone []string
	for id, d := range s.devices {
		if d.LastSeenAt >= cutoff || len(s.deviceConns[id]) > 0 {
			continue
		}
		if seated == nil {
			seated = map[string]bool{}
			for _, r := range s.rooms {
				for _, pair := range r.seatedDevices() {
					seated[pair[0]] = true
				}
			}
		}
		if seated[id] {
			continue
		}
		delete(s.devices, id)
		delete(s.friendSig, id)
		gone = append(gone, id)
	}
	if len(gone) == 0 {
		return
	}
	for id, d := range s.devices {
		dropped := false
		for _, old := range gone {
			if d.Friends[old] != nil {
				delete(d.Friends, old)
				dropped = true
			}
		}
		if dropped {
			delete(s.friendSig, id) // its friends list changed
		}
	}
	s.markDirty()
}

func (s *Server) registerDevice(body *jsBody) (map[string]string, error) {
	if err := s.allowNewDevice(s.reqIPKey()); err != nil {
		return nil, err
	}
	if err := s.makeRoomForDevice(); err != nil {
		return nil, err
	}
	now := s.now().UnixMilli()
	d := &deviceRecord{CreatedAt: now, LastSeenAt: now}
	if err := applyProfile(d, body, false); err != nil {
		return nil, err
	}
	for {
		d.ID = "dev_" + randomToken(12)
		if s.devices[d.ID] == nil {
			break
		}
	}
	secret := randomToken(32)
	d.SecretHash = hashSecret(secret)
	s.devices[d.ID] = d
	s.markDirty()
	return map[string]string{"deviceId": d.ID, "deviceSecret": secret}, nil
}

// applyProfile copies name/tokenKind/color from a request body. With strict
// set, present-but-invalid values are errors; otherwise they are ignored.
func applyProfile(d *deviceRecord, body *jsBody, strict bool) error {
	if raw := body.raw("name"); len(raw) > 0 && string(raw) != "null" {
		name, err := cleanName(body.str("name"))
		if err != nil {
			if strict {
				return err
			}
		} else {
			d.Name = name
		}
	}
	if raw := body.raw("tokenKind"); len(raw) > 0 && string(raw) != "null" {
		kind, _ := body.exactString("tokenKind")
		if validTokenKind(kind) {
			d.TokenKind = canonicalTokenKind(kind)
		} else if strict {
			return badRequest("Choose one of the eight tokens")
		}
	}
	if raw := body.raw("color"); len(raw) > 0 && string(raw) != "null" {
		color, _ := body.exactString("color")
		if reColor.MatchString(color) {
			d.Color = strings.ToUpper(color)
		} else if strict {
			return badRequest("Choose a color like #2F6FE0")
		}
	}
	return nil
}

func (s *Server) updateDevice(d *deviceRecord, body *jsBody) (map[string]any, error) {
	updated := *d
	if err := applyProfile(&updated, body, true); err != nil {
		return nil, err
	}
	changed := updated.Name != d.Name || updated.TokenKind != d.TokenKind || updated.Color != d.Color
	d.Name, d.TokenKind, d.Color = updated.Name, updated.TokenKind, updated.Color
	if changed {
		s.markDirty()
		for id := range d.Friends {
			delete(s.friendSig, id) // force a refresh of names/tokens
		}
		s.pushFriendsAbout([]string{d.ID})
	}
	return s.deviceProfile(d, false), nil
}

func (s *Server) deviceProfile(d *deviceRecord, withRecent bool) map[string]any {
	profile := map[string]any{
		"deviceId":  d.ID,
		"name":      nullable(d.Name),
		"tokenKind": nullable(d.TokenKind),
		"color":     nullable(d.deviceColor()),
	}
	if withRecent {
		recent := d.Recent
		if recent == nil {
			recent = []*recentTable{}
		}
		profile["recent"] = recent
		profile["friends"] = len(d.Friends)
	}
	return profile
}

func (d *deviceRecord) deviceColor() string {
	if d.Color != "" {
		return d.Color
	}
	return tokenColors[canonicalTokenKind(d.TokenKind)]
}

// rememberProfile fills an unnamed device's profile from its first seat.
func (s *Server) rememberProfile(d *deviceRecord, name, tokenKind string) {
	if d == nil {
		return
	}
	changed := false
	if d.Name == "" && name != "" {
		d.Name, changed = name, true
	}
	if d.TokenKind == "" && validTokenKind(tokenKind) {
		d.TokenKind, changed = canonicalTokenKind(tokenKind), true
	}
	if changed {
		s.markDirty()
	}
}

// ---- Friends ---------------------------------------------------------------------------

type friendEntry struct {
	DeviceID     string `json:"deviceId"`
	Name         string `json:"name"`
	TokenKind    any    `json:"tokenKind"`
	Color        any    `json:"color"`
	Status       string `json:"status"`
	Code         string `json:"code,omitempty"`
	Turn         int    `json:"turn,omitempty"`
	LastPlayedAt int64  `json:"lastPlayedAt"`
	LastResult   string `json:"lastResult,omitempty"`
}

var statusRank = map[string]int{"online": 0, "in_game": 1, "offline": 2}

func (s *Server) friendsList(d *deviceRecord) []friendEntry {
	list := []friendEntry{}
	for id, link := range d.Friends {
		friend := s.devices[id]
		if friend == nil {
			continue
		}
		name := friend.Name
		if name == "" {
			name = link.Name
		}
		tokenKind := friend.TokenKind
		if tokenKind == "" {
			tokenKind = link.TokenKind
		}
		color := friend.Color
		if color == "" {
			color = s.tokenColor(tokenKind)
		}
		status, code, turn := s.deviceStatus(id)
		list = append(list, friendEntry{
			DeviceID:     id,
			Name:         name,
			TokenKind:    nullable(tokenKind),
			Color:        nullable(color),
			Status:       status,
			Code:         code,
			Turn:         turn,
			LastPlayedAt: link.LastPlayedAt,
			LastResult:   link.LastResult,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if statusRank[a.Status] != statusRank[b.Status] {
			return statusRank[a.Status] < statusRank[b.Status]
		}
		if a.LastPlayedAt != b.LastPlayedAt {
			return a.LastPlayedAt > b.LastPlayedAt
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.DeviceID < b.DeviceID
	})
	return list
}

// deviceStatus reports online / in_game (with room code and turn) / offline.
func (s *Server) deviceStatus(deviceID string) (status, code string, turn int) {
	now := s.now()
	online := len(s.deviceConns[deviceID]) > 0
	var best *room
	bestPlayer := ""
	for _, r := range s.rooms {
		if r.info == nil || r.info.Status == "finished" {
			continue
		}
		playerID := r.devicePlayer(deviceID)
		if playerID == "" {
			continue
		}
		if best == nil || roomPreferred(r, best) {
			best, bestPlayer = r, playerID
		}
	}
	if best != nil && (online || s.playerOnline(best, bestPlayer, "human", now)) {
		turn := 0
		if best.info.Status == "playing" {
			turn = best.info.Turn // Estate turn or poker hand
		}
		return "in_game", best.code, turn
	}
	if online {
		return "online", "", 0
	}
	return "offline", "", 0
}

func roomPreferred(a, b *room) bool {
	ap, bp := a.info.Status == "playing", b.info.Status == "playing"
	if ap != bp {
		return ap
	}
	if !a.updatedAt.Equal(b.updatedAt) {
		return a.updatedAt.After(b.updatedAt)
	}
	return a.code < b.code
}

func friendsSignature(list []friendEntry) string {
	var b strings.Builder
	for _, e := range list {
		b.WriteString(e.DeviceID + ":" + e.Status + ":" + e.Code + ":" + e.Name + ":" + e.LastResult + ";")
		if kind, ok := e.TokenKind.(string); ok {
			b.WriteString(kind)
		}
		if color, ok := e.Color.(string); ok {
			b.WriteString(color)
		}
	}
	return b.String()
}

// pushFriendsAbout sends fresh friend lists to the online friends of the
// given devices whenever what they would see changed.
func (s *Server) pushFriendsAbout(deviceIDs []string) {
	targets := map[string]bool{}
	for _, id := range deviceIDs {
		d := s.devices[id]
		if d == nil {
			continue
		}
		for friendID := range d.Friends {
			if len(s.deviceConns[friendID]) > 0 {
				targets[friendID] = true
			}
		}
	}
	for _, target := range sortedKeys(targets) {
		d := s.devices[target]
		if d == nil {
			continue
		}
		list := s.friendsList(d)
		sig := friendsSignature(list)
		if sig == s.friendSig[target] {
			continue
		}
		s.friendSig[target] = sig
		if data, err := marshalJS(list); err == nil {
			s.pushToDevice(target, messageJSON("friends", "friends", data))
		}
	}
}

// seatedDevices lists (deviceID, playerID) pairs of a room's phone players.
func (r *room) seatedDevices() [][2]string {
	var pairs [][2]string
	if r.info == nil {
		return nil
	}
	for _, p := range r.info.Players {
		if m := r.meta[p.ID]; m != nil && m.deviceID != "" {
			pairs = append(pairs, [2]string{m.deviceID, p.ID})
		}
	}
	return pairs
}

// checkDevicePresence pushes friend updates when a room's device membership,
// status or seat presence changed.
func (s *Server) checkDevicePresence(r *room, now time.Time) {
	var b strings.Builder
	b.WriteString(r.info.Status)
	var ids []string
	for _, pair := range r.seatedDevices() {
		ids = append(ids, pair[0])
		b.WriteString("|" + pair[0])
		if s.playerOnline(r, pair[1], "human", now) {
			b.WriteString("+")
		}
	}
	sig := b.String()
	if sig == r.deviceSig {
		return
	}
	affected := append(append([]string(nil), r.deviceIDs...), ids...)
	r.deviceSig = sig
	r.deviceIDs = ids
	if len(affected) > 0 {
		s.pushFriendsAbout(affected)
	}
}

// trackGameProgress makes tablemates friends when a game starts and records
// results when it ends.
func (s *Server) trackGameProgress(r *room, prev, next *GameInfo, now time.Time) {
	if prev == nil {
		return
	}
	ms := now.UnixMilli()
	if prev.Status == "lobby" && next.Status == "playing" {
		r.startedAt = ms
		pairs := r.seatedDevices()
		names := make([]string, 0, len(next.Players))
		for _, p := range next.Players {
			names = append(names, p.Name)
		}
		for _, a := range pairs {
			da := s.devices[a[0]]
			if da == nil {
				continue
			}
			entry := &recentTable{Code: r.code, GameType: r.gameTypeName(), StartedAt: ms, Players: names}
			da.Recent = append([]*recentTable{entry}, da.Recent...)
			if len(da.Recent) > maxRecentTables {
				da.Recent = da.Recent[:maxRecentTables]
			}
			for _, b := range pairs {
				if a[0] == b[0] {
					continue
				}
				if da.Friends == nil {
					da.Friends = map[string]*friendLink{}
				}
				link := da.Friends[b[0]]
				if link == nil {
					link = &friendLink{Since: ms}
					da.Friends[b[0]] = link
				}
				link.LastPlayedAt = ms
				link.Games++
				if p := next.player(b[1]); p != nil {
					link.Name = p.Name
				}
				if m := r.meta[b[1]]; m != nil {
					link.TokenKind = m.tokenKind
				}
			}
		}
		s.markDirty()
		return
	}
	if prev.Status == "playing" && next.Status == "finished" {
		pairs := r.seatedDevices()
		result := func(playerID string) string {
			if playerID == next.WinnerID {
				return "won"
			}
			return "lost"
		}
		for _, a := range pairs {
			da := s.devices[a[0]]
			if da == nil {
				continue
			}
			for _, entry := range da.Recent {
				if entry.Code == r.code && entry.StartedAt == r.startedAt {
					entry.FinishedAt = ms
					entry.Result = result(a[1])
				}
			}
			for _, b := range pairs {
				if link := da.Friends[b[0]]; link != nil && a[0] != b[0] {
					link.LastResult = result(b[1])
					link.LastPlayedAt = ms
				}
			}
		}
		s.markDirty()
	}
}

// ---- Invites -----------------------------------------------------------------------------

func (s *Server) sendInvite(d *deviceRecord, body *jsBody) (map[string]any, error) {
	toID, _ := body.exactString("toDeviceId")
	if d.Friends[toID] == nil || s.devices[toID] == nil {
		return nil, forbidden("You can only invite people you have played with")
	}
	r, err := s.requireRoom(body.str("code"))
	if err != nil {
		return nil, err
	}
	if r.info.Status == "finished" {
		return nil, badRequest("That game has already finished")
	}
	now := s.now()
	if !s.inviteLimiter.consume("from:"+d.ID, invitesPerMin, now) ||
		!s.inviteLimiter.consume("pair:"+d.ID+"|"+toID, invitesPerPair, now) {
		return nil, tooMany("Too many invites; please wait a moment")
	}
	name := d.Name
	tokenKind := d.TokenKind
	if playerID := r.devicePlayer(d.ID); playerID != "" {
		if p := r.info.player(playerID); p != nil && name == "" {
			name = p.Name
		}
		if m := r.meta[playerID]; m != nil && tokenKind == "" {
			tokenKind = m.tokenKind
		}
	}
	invite := map[string]any{
		"from": map[string]any{
			"name":      name,
			"tokenKind": nullable(tokenKind),
			"color":     nullable(firstNonEmpty(d.Color, s.tokenColor(tokenKind))),
		},
		"code":           r.code,
		"playersWaiting": len(r.info.Players),
	}
	data, err := marshalJS(invite)
	if err != nil {
		return nil, err
	}
	if r.invited == nil {
		r.invited = map[string]time.Time{}
	}
	r.invited[toID] = now // the game now shows in their /api/games list
	sent := s.pushToDevice(toID, messageJSON("invite", "invite", data)) > 0
	return map[string]any{"sent": sent}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// snapshotDevices serialises device data for persistence (takes s.mu).
func (s *Server) snapshotDevices() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return marshalJS(persistedData{Version: 1, Devices: s.devices})
}

// reqIPKey is the rate-limit key of the request being dispatched ("" outside
// dispatch). Requires s.mu.
func (s *Server) reqIPKey() string {
	if s.reqIP == "" {
		return ""
	}
	return addressKey(s.reqIP)
}
