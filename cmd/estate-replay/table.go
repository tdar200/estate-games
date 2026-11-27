package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/gamerecord"
)

// loadTable reads a poker, Ludo, quiz or closest record or journal; an error means it is
// neither (an Estate record, usually).
func loadTable(path string) (*gamerecord.TableRecord, error) {
	if strings.HasSuffix(path, ".jsonl") {
		if !gamerecord.IsTableJournal(path) {
			return nil, errors.New("not a poker, Ludo, quiz or closest journal")
		}
		j, err := gamerecord.ReadTableJournal(path)
		if err != nil {
			return nil, err
		}
		return j.Finish(), nil
	}
	return gamerecord.ReadTable(path)
}

func tableNames(rec *gamerecord.TableRecord) map[string]string {
	names := map[string]string{}
	for _, p := range rec.Players {
		names[p.ID] = p.Name
	}
	return names
}

// printTableInfo is estate-replay info for a poker, Ludo, quiz or closest record.
func printTableInfo(rec *gamerecord.TableRecord) {
	names := tableNames(rec)
	fmt.Printf("%s  %s, room %s (%s), app %s\n", rec.ID, rec.GameType, rec.Code, rec.Room, rec.AppVersion)
	fmt.Printf("started %s", time.UnixMilli(rec.StartedAt).Format(time.DateTime))
	if rec.EndedAt > 0 {
		fmt.Printf(", ended %s (%s)", time.UnixMilli(rec.EndedAt).Format(time.DateTime), time.Duration(rec.EndedAt-rec.StartedAt)*time.Millisecond/time.Second*time.Second)
	}
	fmt.Printf("\n%d events\n\nPlayers:\n", len(rec.Events))
	for _, p := range rec.Players {
		who := p.Input
		if p.Kind == "ai" {
			who = "computer, " + p.Difficulty
		}
		fmt.Printf("  %d. %-14s %s\n", p.Seat+1, p.Name, who)
	}
	if o := rec.Outcome; o != nil {
		fmt.Printf("\nOutcome: %s", o.Reason)
		if o.WinnerID != "" {
			fmt.Printf(", %s won", names[o.WinnerID])
		}
		fmt.Println()
		for _, s := range o.Standings {
			fmt.Printf("  %-14s", names[s.PlayerID])
			if s.Place > 0 {
				fmt.Printf(" place %d", s.Place)
			}
			if rec.GameType == "poker" {
				fmt.Printf(" %d chips", s.Chips)
			}
			fmt.Println()
		}
	}
	for _, s := range rec.StandIns {
		state := "stopped playing"
		if s.On {
			state = "started playing"
		}
		fmt.Printf("The computer %s for %s at event %d\n", state, names[s.PlayerID], s.Seq)
	}
	switch rec.GameType {
	case "poker":
		hands := gamerecord.PokerHands(rec)
		biggest, won := 0, map[string]int{}
		for _, h := range hands {
			pot := 0
			for _, w := range h.Wins {
				pot += w.Amount
				won[w.PlayerID]++
			}
			biggest = max(biggest, pot)
		}
		fmt.Printf("\n%d hands, biggest pot %d\n", len(hands), biggest)
		for _, p := range rec.Players {
			fmt.Printf("  %-14s won a pot in %d hands\n", p.Name, won[p.ID])
		}
		fmt.Println("See every hand with: estate-replay hands FILE")
	case "ludo":
		fmt.Println("\nPlayer         rolls  sixes  moves  captures  lost  home")
		for _, s := range gamerecord.LudoStats(rec) {
			fmt.Printf("  %-13s %5d  %5d  %5d  %8d  %4d  %4d\n", names[s.PlayerID], s.Rolls, s.Sixes, s.Moves, s.Captures, s.Captured, s.Home)
		}
	}
}

// printHands prints a poker record hand by hand.
func printHands(path string) error {
	rec, err := loadTable(path)
	if err != nil {
		return err
	}
	if rec.GameType != "poker" {
		return fmt.Errorf("hands: this is a %s record", rec.GameType)
	}
	names := tableNames(rec)
	for _, h := range gamerecord.PokerHands(rec) {
		fmt.Printf("Hand %d", h.N)
		if len(h.Board) > 0 {
			fmt.Printf("  board %s", strings.Join(h.Board, " "))
		}
		fmt.Println()
		for _, p := range rec.Players {
			if cards := h.Holes[p.ID]; len(cards) > 0 {
				fmt.Printf("  %-14s %s\n", p.Name, strings.Join(cards, " "))
			}
		}
		for _, st := range h.Steps {
			line := names[st.PlayerID] + " " + st.Action
			if st.Amount > 0 {
				line += fmt.Sprintf(" %d", st.Amount)
			}
			if st.AllIn {
				line += " (all in)"
			}
			fmt.Println("    " + line)
		}
		for _, w := range h.Wins {
			line := fmt.Sprintf("  %s wins %d", names[w.PlayerID], w.Amount)
			if w.HandName != "" {
				line += " with " + w.HandName
			}
			fmt.Println(line)
		}
	}
	return nil
}
