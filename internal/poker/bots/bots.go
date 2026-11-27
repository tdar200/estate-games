// Package bots holds the computer players for the poker party game:
// heuristic Easy and Normal bots, and a bridge to an external bot process
// (stdin/stdout JSON or localhost HTTP) for the stronger AI.
//
// A bot sees exactly what a player in its seat would see: the holdem.View
// built for that seat. It answers with the same command a phone sends
// ({"type":"raise","to":600}, …).
package bots

import (
	"context"
	"errors"
	"fmt"

	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// Bot decides for one seat. Decide is called when the seat must act or may
// rebuy (View.You.ToAct or View.You.CanRebuy). Implementations must not
// keep the View after returning.
type Bot interface {
	Decide(ctx context.Context, v *holdem.View) (holdem.Command, error)
}

// Difficulty names.
const (
	Easy   = "easy"
	Normal = "normal"
)

// New returns a built-in bot. The seed makes its random choices
// reproducible; different seats should get different seeds.
func New(difficulty string, seed uint64) (Bot, error) {
	switch difficulty {
	case Easy:
		return NewHeuristic(EasyProfile, seed), nil
	case Normal, "":
		return NewHeuristic(NormalProfile, seed), nil
	}
	return nil, fmt.Errorf("unknown bot difficulty %q", difficulty)
}

// Validate checks a command against the View's legal options.
func Validate(v *holdem.View, c holdem.Command) error {
	y := v.You
	if y == nil {
		return errors.New("not a seated player")
	}
	switch c.Type {
	case "rebuy", "declineRebuy":
		if !y.CanRebuy {
			return errors.New("no rebuy decision is pending")
		}
		return nil
	}
	if !y.ToAct || y.Legal == nil {
		return errors.New("not this seat's turn")
	}
	l := y.Legal
	switch c.Type {
	case "fold":
		if !l.Fold {
			return errors.New("folding is not allowed when checking is free")
		}
	case "check":
		if !l.Check {
			return errors.New("there is a bet to call")
		}
	case "call":
		if l.Call == 0 && !l.Check {
			return errors.New("nothing to call")
		}
	case "allin":
	case "bet", "raise":
		if l.Raise == nil {
			return errors.New("raising is not allowed")
		}
		if c.To < l.Raise.Min || c.To > l.Raise.Max {
			return fmt.Errorf("raise to %d outside %d–%d", c.To, l.Raise.Min, l.Raise.Max)
		}
	default:
		return fmt.Errorf("unknown action %q", c.Type)
	}
	return nil
}

// Safe is the fallback decision: check, else fold; decline a rebuy.
func Safe(v *holdem.View) holdem.Command {
	if v.You != nil && v.You.CanRebuy && !v.You.ToAct {
		return holdem.Command{Type: "declineRebuy"}
	}
	if v.You != nil && v.You.Legal != nil && v.You.Legal.Check {
		return holdem.Command{Type: "check"}
	}
	return holdem.Command{Type: "fold"}
}

// WithFallback wraps a bot (typically an external one) so that errors,
// timeouts and illegal answers become fallback's decision instead.
func WithFallback(primary, fallback Bot) Bot { return &fallbackBot{primary, fallback} }

type fallbackBot struct{ primary, fallback Bot }

func (f *fallbackBot) Decide(ctx context.Context, v *holdem.View) (holdem.Command, error) {
	c, err := f.primary.Decide(ctx, v)
	if err == nil && Validate(v, c) == nil {
		return c, nil
	}
	if f.fallback != nil {
		if c2, err2 := f.fallback.Decide(ctx, v); err2 == nil && Validate(v, c2) == nil {
			return c2, nil
		}
	}
	return Safe(v), nil
}
