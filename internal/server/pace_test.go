package server

import (
	"testing"
	"time"
)

// A computer's roll is shown before its next move: the dice, every hop and the rent.
func TestShowTimeCoversARoll(t *testing.T) {
	roll := []event{
		newEvent("dice", f("playerId", "ai_1"), f("dice", []int{2, 2})),
		newEvent("move", f("playerId", "ai_1"), f("path", []int{5, 6, 7, 8, 9})),
		newEvent("pay", f("from", "ai_1"), f("to", "p_1"), f("amount", 14), f("reason", "rent")),
	}
	want := showDice + 4*showHopStep + showHopStep*6/10 + showPay + showMargin
	if got := showTime(roll); got != want {
		t.Fatalf("showTime(roll) = %v, want %v", got, want)
	}
	if got := showTime(roll); got < 3*time.Second {
		t.Fatalf("a roll with rent shows for %v; computers moved every 0.52 s and the TV fell behind", got)
	}
	// A long hop past PAYDAY: the camera follows it, and the $200 shows.
	long := []event{
		newEvent("dice", f("playerId", "ai_1"), f("dice", []int{6, 5})),
		newEvent("move", f("playerId", "ai_1"), f("path", []int{35, 36, 37, 38, 39, 0, 1, 2, 3, 4, 5, 6})),
		newEvent("pay", f("from", nil), f("to", "ai_1"), f("amount", 200), f("reason", "go")),
	}
	if got, want := showTime(long), showDice+11*showHopStep+showHopStep*6/10+showFollow+showPayday+showMargin; got != want {
		t.Fatalf("showTime(long) = %v, want %v", got, want)
	}
	// Nothing to show (a bid, a trade answer): no hold beyond the usual delay.
	if got := showTime([]event{newEvent("auction", f("spaceIndex", 3), f("bid", 20))}); got != 0 {
		t.Fatalf("showTime(auction) = %v, want 0", got)
	}
	// The opening roll for turn order has its own screen and no hold.
	if got := showTime([]event{newEvent("dice", f("playerId", "ai_1"), f("dice", []int{1, 2}), f("order", true))}); got != 0 {
		t.Fatalf("showTime(order roll) = %v, want 0", got)
	}
}

// The hold follows the computer speed and the server's delay (tests run with a tiny delay).
func TestShowHoldScales(t *testing.T) {
	s := &Server{aiDelay: defaultAIDelay}
	roll := []event{newEvent("dice", f("playerId", "ai_1"), f("dice", []int{3, 4})), newEvent("move", f("playerId", "ai_1"), f("path", []int{1, 2, 3, 4, 5, 6, 7, 8}))}
	normal := s.showHoldAt(roll, 1)
	if normal != showTime(roll) {
		t.Fatalf("normal hold %v, want the show time %v", normal, showTime(roll))
	}
	if slow, fast := s.showHoldAt(roll, computerSpeeds["slow"]), s.showHoldAt(roll, computerSpeeds["fast"]); !(slow > normal && fast < normal) {
		t.Fatalf("holds slow %v, normal %v, fast %v", slow, normal, fast)
	}
	quick := &Server{aiDelay: 5 * time.Millisecond}
	if got := quick.showHoldAt(roll, 1); got > 100*time.Millisecond {
		t.Fatalf("test servers hold %v; it should scale down with the delay", got)
	}
}
