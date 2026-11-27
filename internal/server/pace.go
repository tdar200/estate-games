package server

import "time"

// How long the screens take to show an Estate state's events at the normal animation speed
// (web/table/table.js playEvents, web/shared/motion.js, web/table/camera.js; the phones are
// no slower). Computers wait this long before their next move (room.cardHoldUntil), so the
// table shows every roll, hop and payment instead of falling behind: the TV skips animations
// and jumps the tokens once three or more states are queued, which looked like a token
// landing and then appearing somewhere else a moment later.
const (
	showDice       = 1050 * time.Millisecond // motion.throwDice
	showHopStep    = 120 * time.Millisecond  // board.hop, per square
	showHopStepFar = 85 * time.Millisecond   // per square on hops of 15+ squares
	showFollow     = 1150 * time.Millisecond // follow camera: zoom in (650 ms) and rest on the landing (500 ms)
	showFollowMin  = 7                       // camera.js FOLLOW_MIN_STEPS
	showJailMove   = 900 * time.Millisecond
	showPay        = 1500 * time.Millisecond // ease out, notes fly, settle
	showPayday     = 500 * time.Millisecond
	showBuy        = 1300 * time.Millisecond // SOLD stamp
	showBuild      = 800 * time.Millisecond
	showJail       = 900 * time.Millisecond
	showBankrupt   = 700 * time.Millisecond
	showMargin     = 250 * time.Millisecond // per state with something to show: frames, sounds, settling
)

// showTime is how long the table needs to show these events at the normal speed.
func showTime(events []event) time.Duration {
	var total time.Duration
	for _, e := range events {
		switch e.kind() {
		case "dice":
			if order, _ := e.get("order").(bool); !order {
				total += showDice
			}
		case "move":
			path, _ := e.get("path").([]int)
			steps := len(path) - 1
			if steps <= 0 {
				continue
			}
			if jail, _ := e.get("jail").(bool); jail {
				total += showJailMove
				continue
			}
			step := showHopStep
			if len(path) > 14 {
				step = showHopStepFar
			}
			total += time.Duration(steps)*step + step*6/10 // the last step lands at 1.6×
			if steps >= showFollowMin || passesPayday(path) {
				total += showFollow
			}
		case "pay":
			if reason, _ := e.get("reason").(string); reason == "go" {
				total += showPayday
			} else {
				total += showPay
			}
		case "buy":
			total += showBuy
		case "build":
			total += showBuild
		case "jail":
			total += showJail
		case "bankrupt":
			total += showBankrupt
		}
	}
	if total > 0 {
		total += showMargin
	}
	return total
}

// passesPayday: the path wraps past square 0 (camera.js passesGo).
func passesPayday(path []int) bool {
	for i := 1; i < len(path); i++ {
		if path[i] < path[i-1] {
			return true
		}
	}
	return false
}

// showHoldAt is how long computers wait after these events: the show time at the room's
// computer speed, scaled with the server's computer delay like cardHoldAt (tests run fast).
func (s *Server) showHoldAt(events []event, speed float64) time.Duration {
	show := showTime(events)
	if show == 0 {
		return 0
	}
	return time.Duration(float64(show) * speed * float64(s.aiDelay) / float64(defaultAIDelay))
}
