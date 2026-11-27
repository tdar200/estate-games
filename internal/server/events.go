package server

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// event is an animation event sent as {"type":"event","event":{…}}. Fields
// keep insertion order so "type" comes first.
type event []eventField

type eventField struct {
	key   string
	value any
}

func newEvent(kind string, fields ...eventField) event {
	return append(event{{"type", kind}}, fields...)
}

func (e event) kind() string { return e[0].value.(string) }

func (e event) get(key string) any {
	for _, f := range e {
		if f.key == key {
			return f.value
		}
	}
	return nil
}

func (e event) set(key string, value any) {
	for i := range e {
		if e[i].key == key {
			e[i].value = value
			return
		}
	}
}

// MarshalJSON implements json.Marshaler.
func (e event) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range e {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(f.key)
		value, err := marshalJS(f.value)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func f(key string, value any) eventField { return eventField{key, value} }

// nullable returns nil (JSON null) for "".
func nullable(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// logItem is parseLog's result reduced to what diffEvents needs.
type logItem struct {
	kind       string
	playerID   string
	otherID    string
	spaceIndex *int
	amount     int
	bid        int
}

var (
	reOrderRoll   = regexp.MustCompile(`^ rolled (\d) \+ (\d) = (\d+) for turn order$`)
	reRoll        = regexp.MustCompile(`^ rolled (\d) \+ (\d) = (\d+)$`)
	reLanded      = regexp.MustCompile(`^ landed on (.+)$`)
	reBought      = regexp.MustCompile(`^ bought (.+) for \$(\d+)$`)
	reWon         = regexp.MustCompile(`^ won (.+) for \$(\d+)$`)
	reRent        = regexp.MustCompile(`^ paid \$(\d+) \((.+) rent\)$`)
	rePaid        = regexp.MustCompile(`^ paid \$(\d+) \((.+)\)$`)
	reTransferFee = regexp.MustCompile(`^ paid a \$(\d+) mortgage transfer fee$`)
	reDrew        = regexp.MustCompile(`^ drew: (.+)$`)
	reBid         = regexp.MustCompile(`^ bid \$(\d+)$`)
	reBuilt       = regexp.MustCompile(`^ built a (house|hotel) on (.+)$`)
	reBankrupt    = regexp.MustCompile(`^ declared bankruptcy(.*)$`)
)

type eventContext struct {
	next  *gameView
	board []boardSpace
	names []playerView // players sorted by name length, longest first
}

func (c *eventContext) leadingPlayer(text string) *playerView {
	for i := range c.names {
		p := &c.names[i]
		if text == p.Name || strings.HasPrefix(text, p.Name+" ") || strings.HasPrefix(text, p.Name+"'s ") {
			return p
		}
	}
	return nil
}

func (c *eventContext) spaceByName(name string) *int {
	for i := range c.board {
		if c.board[i].Name == name {
			index := c.board[i].Index
			return &index
		}
	}
	return nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// parseLog ports parseLog from web/shared/format.js (event-relevant kinds).
func (c *eventContext) parseLog(text string) logItem {
	actor := c.leadingPlayer(text)
	if actor == nil {
		return logItem{kind: "system"}
	}
	item := logItem{kind: "info", playerID: actor.ID}
	rest := text[len(actor.Name):]
	if m := reOrderRoll.FindStringSubmatch(rest); m != nil {
		item.kind = "order"
		return item
	}
	if m := reRoll.FindStringSubmatch(rest); m != nil {
		item.kind = "roll"
		return item
	}
	if m := reLanded.FindStringSubmatch(rest); m != nil {
		item.kind = "land"
		item.spaceIndex = c.spaceByName(m[1])
		return item
	}
	if m := reBought.FindStringSubmatch(rest); m != nil {
		item.kind, item.amount, item.spaceIndex = "buy", -atoi(m[2]), c.spaceByName(m[1])
		return item
	}
	if m := reWon.FindStringSubmatch(rest); m != nil {
		item.kind, item.amount, item.spaceIndex = "buy", -atoi(m[2]), c.spaceByName(m[1])
		return item
	}
	if m := reRent.FindStringSubmatch(rest); m != nil {
		item.kind, item.amount = "rent", atoi(m[1])
		item.spaceIndex = c.spaceByName(m[2])
		if item.spaceIndex != nil {
			if prop, ok := c.next.Properties[strconv.Itoa(*item.spaceIndex)]; ok && c.next.player(prop.OwnerID) != nil {
				item.otherID = prop.OwnerID
			}
		}
		return item
	}
	if m := rePaid.FindStringSubmatch(rest); m != nil {
		item.kind, item.amount = "pay", -atoi(m[1])
		return item
	}
	if m := reTransferFee.FindStringSubmatch(rest); m != nil {
		item.kind, item.amount = "pay", -atoi(m[1])
		return item
	}
	switch rest {
	case " passed PAYDAY and collected $200":
		item.kind, item.amount = "go", 200
		return item
	case " was sent to jail", " rolled doubles three times in a row", " rolled doubles and left jail",
		" used a Jail pass", " paid bail and left jail":
		item.kind = "jail"
		return item
	case " wins the game":
		item.kind = "win"
		return item
	}
	if reDrew.MatchString(rest) {
		item.kind = "card"
		return item
	}
	if m := reBid.FindStringSubmatch(rest); m != nil {
		item.kind, item.bid = "bid", atoi(m[1])
		return item
	}
	if m := reBuilt.FindStringSubmatch(rest); m != nil {
		item.kind, item.spaceIndex = "build", c.spaceByName(m[2])
		return item
	}
	if reBankrupt.MatchString(rest) {
		item.kind = "bankrupt"
		return item
	}
	if rest == " left the game; assets returned to the bank" { // the host removed them
		item.kind = "left"
		return item
	}
	return item
}

var (
	reDiceText      = regexp.MustCompile(`rolled (\d) \+ (\d) = `)
	reOrderDiceText = regexp.MustCompile(`rolled (\d) \+ (\d) = \d+ for turn order$`)
	reDrewPrefix    = regexp.MustCompile(`^.*? drew: `)
)

// diffEvents derives animation events from two consecutive game states. It
// is a port of diffEvents in web/shared/events.js, so the WebSocket events
// match what polling clients derive themselves.
func diffEvents(prev, next *gameView, board []boardSpace) []event {
	if prev == nil || next == nil || prev.Code != next.Code {
		return nil
	}
	ctx := newEventContext(next, board)

	seenLog := map[string]bool{}
	for _, entry := range prev.Log {
		seenLog[entry.ID] = true
	}
	seenMoves := map[string]bool{}
	if prev.Turn != nil {
		for _, m := range prev.Turn.Movements {
			seenMoves[m.ID] = true
		}
	}
	var moves []movementView
	if next.Turn != nil {
		for _, m := range next.Turn.Movements {
			if !seenMoves[m.ID] {
				moves = append(moves, m)
			}
		}
	}
	var events []event
	pendingGo := map[string]bool{}
	var goOrder []string
	landedType := map[string]string{}

	takeMove := func(playerID string) *movementView {
		for i, m := range moves {
			if m.PlayerID == playerID {
				moves = append(moves[:i], moves[i+1:]...)
				return &m
			}
		}
		return nil
	}
	flushGo := func(playerID string) {
		if pendingGo[playerID] {
			events = append(events, newEvent("pay", f("from", nil), f("to", playerID), f("amount", 200), f("reason", "go")))
			delete(pendingGo, playerID)
		}
	}
	spaceType := func(index *int) string {
		if index == nil {
			return ""
		}
		for _, space := range board {
			if space.Index == *index {
				return space.Type
			}
		}
		return ""
	}
	houses := func(view *gameView, index int) int {
		return view.Properties[strconv.Itoa(index)].Houses
	}

	for index, entry := range next.Log {
		if seenLog[entry.ID] {
			continue
		}
		item := ctx.parseLog(entry.Text)
		playerID := item.playerID
		switch item.kind {
		case "roll":
			if m := reDiceText.FindStringSubmatch(entry.Text); m != nil {
				events = append(events, newEvent("dice", f("playerId", nullable(playerID)), f("dice", []int{atoi(m[1]), atoi(m[2])})))
			}
		case "order":
			if m := reOrderDiceText.FindStringSubmatch(entry.Text); m != nil {
				events = append(events, newEvent("dice", f("playerId", nullable(playerID)), f("dice", []int{atoi(m[1]), atoi(m[2])}), f("order", true)))
			}
		case "go":
			if !pendingGo[playerID] {
				goOrder = append(goOrder, playerID)
			}
			pendingGo[playerID] = true
		case "land":
			if m := takeMove(playerID); m != nil {
				events = append(events, newEvent("move", f("playerId", m.PlayerID), f("path", pathOf(m))))
			}
			flushGo(playerID)
			if item.spaceIndex != nil {
				landedType[playerID] = spaceType(item.spaceIndex)
			}
		case "jail":
			if strings.HasSuffix(entry.Text, "was sent to jail") {
				if m := takeMove(playerID); m != nil {
					events = append(events, newEvent("move", f("playerId", m.PlayerID), f("path", pathOf(m)), f("jail", true)))
				}
				events = append(events, newEvent("jail", f("playerId", nullable(playerID))))
			}
		case "card":
			text := reDrewPrefix.ReplaceAllString(entry.Text, "")
			deck := "chance"
			switch {
			case landedType[playerID] == "chest":
				deck = "chest"
			case landedType[playerID] == "chance":
				deck = "chance"
			case next.LastCard != nil && next.LastCard.Text == text:
				deck = next.LastCard.Deck
			}
			events = append(events, newEvent("card", f("playerId", nullable(playerID)), f("deck", deck), f("text", text)))
		case "rent":
			var space any
			if item.spaceIndex != nil {
				space = *item.spaceIndex
			}
			pay := newEvent("pay", f("from", nullable(playerID)), f("to", nullable(item.otherID)), f("amount", item.amount), f("reason", "rent"), f("spaceIndex", space))
			if payer := next.player(playerID); payer != nil {
				if landed := ctx.landedBefore(next.Log, index, payer); landed >= 0 {
					pay = append(pay, rentFields(ctx.cardRentAt(next.Log, landed, payer, item.amount))...) // RENT x2 / x10 (cardrent.go)
				}
			}
			events = append(events, pay)
		case "pay":
			amount := item.amount
			if amount < 0 {
				amount = -amount
			}
			events = append(events, newEvent("pay", f("from", nullable(playerID)), f("to", nil), f("amount", amount), f("reason", "fee")))
		case "buy":
			if item.spaceIndex != nil {
				price := item.amount
				if price < 0 {
					price = -price
				}
				events = append(events, newEvent("buy", f("playerId", nullable(playerID)), f("spaceIndex", *item.spaceIndex), f("price", price)))
			}
		case "build":
			if item.spaceIndex != nil {
				events = append(events, newEvent("build", f("playerId", nullable(playerID)), f("spaceIndex", *item.spaceIndex),
					f("houses", houses(next, *item.spaceIndex)), f("from", houses(prev, *item.spaceIndex))))
			}
		case "bid":
			var space any
			if next.Auction != nil {
				space = next.Auction.SpaceIndex
			} else if prev.Auction != nil {
				space = prev.Auction.SpaceIndex
			}
			events = append(events, newEvent("auction", f("spaceIndex", space), f("bid", item.bid), f("bidderId", nullable(playerID))))
		case "bankrupt":
			var creditor any
			if next.LastBankruptcy != nil {
				creditor = nullable(next.LastBankruptcy.CreditorID)
			}
			events = append(events, newEvent("bankrupt", f("playerId", nullable(playerID)), f("creditorId", creditor)))
		case "left":
			events = append(events, newEvent("left", f("playerId", nullable(playerID))))
		case "win":
			events = append(events, newEvent("win", f("playerId", nullable(playerID))))
		}
	}
	for i := range moves {
		events = append(events, newEvent("move", f("playerId", moves[i].PlayerID), f("path", pathOf(&moves[i]))))
	}
	for _, playerID := range goOrder {
		flushGo(playerID)
	}
	if next.Status == "finished" && prev.Status != "finished" {
		hasWin := false
		for _, e := range events {
			if e.kind() == "win" {
				hasWin = true
			}
		}
		if !hasWin {
			events = append(events, newEvent("win", f("playerId", nullable(next.WinnerID))))
		}
	}
	// Merge builds on the same square into one event.
	var merged []event
	for _, e := range events {
		if len(merged) > 0 {
			last := merged[len(merged)-1]
			if e.kind() == "build" && last.kind() == "build" && last.get("spaceIndex") == e.get("spaceIndex") {
				last.set("houses", e.get("houses"))
				continue
			}
		}
		merged = append(merged, e)
	}
	return merged
}

func pathOf(m *movementView) []int {
	if m.Path == nil {
		return []int{}
	}
	return m.Path
}
