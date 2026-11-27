// Package holdem is the No-Limit Texas Hold'em rules engine for the party
// game: a single-hand state machine (Hand) and a multi-hand session (Table)
// with blinds, antes, a blind schedule, side pots, showdowns, bust-outs,
// rebuys and sitting out.
//
// The hand evaluator in ./eval and the pot/side-pot approach are forked from
// github.com/alexclewontin/riverboat (BSD-2-Clause, Copyright (c) 2020 Alex
// Lewontin; see LICENSE-riverboat at the repository root). The betting rules
// follow PokerKit's semantics and are cross-checked against it
// (crosscheck_test.go, tools/pokerkit_check.py).
//
// The package uses the standard library only so it can move into another
// module unchanged.
package holdem

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/tdar200/estate-games/internal/poker/holdem/eval"
)

// Card is one playing card. The zero Card means "no card".
type Card = eval.Card

const rankChars = "23456789TJQKA"
const suitChars = "cdhs"

// Rank returns 0 (deuce) … 12 (ace).
func Rank(c Card) int { return int((int32(c) >> 8) & 0x0F) }

// Suit returns 0 clubs, 1 diamonds, 2 hearts, 3 spades.
func Suit(c Card) int {
	switch int32(c) & 0xF000 {
	case 0x8000:
		return 0
	case 0x4000:
		return 1
	case 0x2000:
		return 2
	default:
		return 3
	}
}

// CardString formats a card as rank + lower-case suit, e.g. "As", "Td", "2c".
func CardString(c Card) string {
	if c == 0 {
		return ""
	}
	return string(rankChars[Rank(c)]) + string(suitChars[Suit(c)])
}

// ParseCard parses "As", "td", "10h" and similar.
func ParseCard(s string) (Card, error) {
	c, err := eval.ParseCardBytes([]byte(s))
	if err != nil {
		return 0, fmt.Errorf("invalid card %q", s)
	}
	return c, nil
}

// MustCard is ParseCard that panics; for tests and fixtures.
func MustCard(s string) Card {
	c, err := ParseCard(s)
	if err != nil {
		panic(err)
	}
	return c
}

// ParseCards parses a run of cards, with or without spaces: "AsKd" or "As Kd".
func ParseCards(s string) ([]Card, error) {
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, ",", "")
	var out []Card
	for len(s) > 0 {
		n := 2
		if strings.HasPrefix(s, "10") {
			n = 3
		}
		if len(s) < n {
			return nil, fmt.Errorf("invalid cards %q", s)
		}
		c, err := ParseCard(s[:n])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
		s = s[n:]
	}
	return out, nil
}

// MustCards is ParseCards that panics.
func MustCards(s string) []Card {
	cs, err := ParseCards(s)
	if err != nil {
		panic(err)
	}
	return cs
}

// CardsString joins cards without separators, e.g. "AsKd".
func CardsString(cs []Card) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(CardString(c))
	}
	return b.String()
}

func cardStrings(cs []Card) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if c != 0 {
			out = append(out, CardString(c))
		}
	}
	return out
}

func parseCardStrings(ss []string) ([]Card, error) {
	out := make([]Card, 0, len(ss))
	for _, s := range ss {
		c, err := ParseCard(s)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// NewDeck returns the 52 cards in a fixed order.
func NewDeck() []Card { return append([]Card(nil), eval.DefaultDeck...) }

// ShuffledDeck returns a deck shuffled with rng (Fisher-Yates).
func ShuffledDeck(rng *rand.Rand) []Card {
	var d eval.Deck
	d.Shuffle(rng)
	return []Card(d)
}

// cardList marshals as ["As","Kd"] and unmarshals the same.
type cardList []Card

func (cl cardList) MarshalJSON() ([]byte, error) { return json.Marshal(cardStrings(cl)) }

func (cl *cardList) UnmarshalJSON(b []byte) error {
	var ss []string
	if err := json.Unmarshal(b, &ss); err != nil {
		return err
	}
	cs, err := parseCardStrings(ss)
	if err != nil {
		return err
	}
	*cl = cs
	return nil
}
