package ludo

import "encoding/json"

// Viewer describes who is looking (mirrors the Estate server's Viewer).
// Role is "player", "spectator" or "table" (the shared big screen).
type Viewer struct {
	Role        string
	Name        string
	SpectatorID string
	IsHost      bool
	Spectators  []Spectator
}

// Spectator is one entry of View.Spectators.
type Spectator struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IsHost bool   `json:"isHost"`
}

// View is the state one viewer gets. Ludo hides nothing, so every viewer
// sees the same game; only You and the viewer fields differ. PublicState is
// View as JSON, and bots decide from the same View.
type View struct {
	Game      string    `json:"game"` // always "ludo"
	Code      string    `json:"code"`
	Status    string    `json:"status"` // lobby | playing | finished
	Phase     string    `json:"phase"`  // lobby | roll | move | finished
	HostID    string    `json:"hostId"`
	Version   int64     `json:"version"`
	Now       int64     `json:"now"` // server clock (Unix ms)
	Settings  Settings  `json:"settings"`
	SeatLimit int       `json:"seatLimit"` // 6, or 4 when the host chose the 4-arm board
	Board     BoardView `json:"board"`
	// TurnID is the player to act; TurnNumber counts turns (bonus rolls
	// included in the same turn).
	TurnID     string `json:"turnId"`
	TurnNumber int    `json:"turnNumber"`
	// Dice is the last roll (0 before the first) and DiceBy who rolled it;
	// RollSeq counts rolls, so screens animate each new one; Sixes is the
	// number of 6s in a row in this turn.
	Dice     int          `json:"dice"`
	DiceBy   string       `json:"diceBy"`
	RollSeq  int          `json:"rollSeq"`
	Sixes    int          `json:"sixes"`
	Moves    []Move       `json:"moves"` // the legal moves for Dice while phase is move (everyone may see them)
	LastMove *LastMove    `json:"lastMove"`
	Players  []PlayerView `json:"players"`
	You      *YouView     `json:"you"` // set when the viewer is a player in this game
	WinnerID string       `json:"winnerId"`
	// Standings lists every player in finishing order once the game is over.
	Standings    []StandingView `json:"standings"`
	Log          []LogEntry     `json:"log"`
	LastEventSeq int64          `json:"lastEventSeq"`

	ViewerID     string      `json:"viewerId"`
	ViewerRole   string      `json:"viewerRole"`
	ViewerName   string      `json:"viewerName"`
	SpectatorID  string      `json:"spectatorId"`
	ViewerIsHost bool        `json:"viewerIsHost"`
	Spectators   []Spectator `json:"spectators"`
}

// BoardView is the geometry clients need to draw the board (see board.go
// for the numbering).
type BoardView struct {
	Arms      int   `json:"arms"`      // 4 or 6
	Ring      int   `json:"ring"`      // ring squares (13 per arm)
	Column    int   `json:"column"`    // home-column squares per arm (5)
	StepsHome int   `json:"stepsHome"` // steps from a start square to home
	Starts    []int `json:"starts"`    // start square of each arm
	Safe      []int `json:"safe"`      // safe squares (empty when the house rule is off)
}

// PieceView is one piece.
type PieceView struct {
	Index int    `json:"index"`
	Zone  string `json:"zone"` // yard | track | column | home
	Step  int    `json:"step"`
	Cell  string `json:"cell"`
	// Square is the ring square on the track, else -1.
	Square int `json:"square"`
	// Left is how many steps it still has to go to reach home.
	Left int  `json:"left"`
	Safe bool `json:"safe"` // can't be captured where it is
}

// PlayerView is one seat.
type PlayerView struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Kind   string      `json:"kind"` // human | ai
	Arm    int         `json:"arm"`
	Status string      `json:"status"` // waiting (lobby) | playing | finished | left
	Pieces []PieceView `json:"pieces"`
	Home   int         `json:"home"` // pieces home
	Out    int         `json:"out"`  // pieces on the board (not in the yard, not home)
	// Progress is the share of the whole journey their pieces have made, 0–100.
	Progress int `json:"progress"`
	Captures int `json:"captures"`
	Lost     int `json:"lost"`
	Sixes    int `json:"sixes"`
	Place    int `json:"place,omitempty"`
	// CanGoHome is false while the capture-first house rule still holds
	// their pieces back from the home column.
	CanGoHome bool `json:"canGoHome"`
	IsTurn    bool `json:"isTurn"`
}

// YouView is the viewer's own panel.
type YouView struct {
	ID     string `json:"id"`
	ToRoll bool   `json:"toRoll"`
	ToMove bool   `json:"toMove"`
	// Movable are the pieces you may move (the indexes of Moves).
	Movable []int `json:"movable"`
	Place   int   `json:"place,omitempty"`
}

// StandingView is one row of the final standings.
type StandingView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Place    int    `json:"place"`
	Home     int    `json:"home"`
	Progress int    `json:"progress"`
	Captures int    `json:"captures"`
	Left     bool   `json:"left,omitempty"`
}

// View builds the state for one viewer (viewerID "" for the table screen,
// spectators and the lobby summary).
func (g *Game) View(viewerID string, v Viewer) *View {
	b := g.Board()
	r := g.rules()
	view := &View{
		Game: "ludo", Code: g.Code, Status: g.Status, Phase: g.Phase, HostID: g.HostID, Version: g.Version, Now: g.Now(),
		Settings: g.Settings, SeatLimit: g.SeatLimit(), TurnNumber: g.TurnNumber, Dice: g.Dice, DiceBy: g.DiceBy,
		RollSeq: g.RollSeq, Sixes: g.Sixes, Moves: []Move{}, LastMove: g.LastMove, WinnerID: g.WinnerID,
		Log: g.Log, LastEventSeq: g.EventSeq, Players: make([]PlayerView, 0, len(g.Players)),
		ViewerID: viewerID, ViewerRole: v.Role, ViewerName: v.Name, SpectatorID: v.SpectatorID, ViewerIsHost: v.IsHost,
		Spectators: v.Spectators,
	}
	if view.Spectators == nil {
		view.Spectators = []Spectator{}
	}
	view.Board = BoardView{Arms: b.Arms, Ring: b.Ring, Column: ColumnLen, StepsHome: b.StepsHome(), Starts: make([]int, b.Arms), Safe: []int{}}
	for arm := range view.Board.Starts {
		view.Board.Starts[arm] = b.Start(arm)
	}
	if g.Settings.SafeSquares {
		view.Board.Safe = b.SafeSquares()
	}
	if g.Moves != nil {
		view.Moves = g.Moves
	}
	current := g.Current()
	if current != nil {
		view.TurnID = current.ID
	}
	for _, p := range g.Players {
		pv := PlayerView{ID: p.ID, Name: p.Name, Kind: p.Kind, Arm: p.Arm, Pieces: make([]PieceView, len(p.Pieces)),
			Home: p.HomeCount(), Captures: p.Captures, Lost: p.Lost, Sixes: p.Sixes, Place: p.Place,
			CanGoHome: r.mayEnter(p), IsTurn: p == current}
		switch {
		case g.Status == StatusLobby:
			pv.Status = "waiting"
		case p.Left:
			pv.Status = "left"
		case p.Place > 0 && p.allHome():
			pv.Status = "finished"
		default:
			pv.Status = "playing"
		}
		total := 0
		for i, pc := range p.Pieces {
			square := -1
			if pc.Zone == ZoneTrack {
				square = b.Square(p.Arm, pc.Step)
			}
			if pc.Zone == ZoneTrack || pc.Zone == ZoneColumn {
				pv.Out++
			}
			pv.Pieces[i] = PieceView{Index: i, Zone: pc.Zone, Step: pc.Step, Cell: b.Cell(p.Arm, i, pc), Square: square,
				Left: b.Distance(pc), Safe: r.safeAt(p, pc)}
			total += b.Travelled(pc)
		}
		if n := len(p.Pieces); n > 0 {
			pv.Progress = total * 100 / (n * (b.StepsHome() + 1))
		}
		view.Players = append(view.Players, pv)
		if p.ID == viewerID {
			you := &YouView{ID: p.ID, Movable: []int{}, Place: p.Place}
			if p == current {
				you.ToRoll = g.Phase == PhaseRoll
				you.ToMove = g.Phase == PhaseMove
				for _, m := range g.Moves {
					you.Movable = append(you.Movable, m.Piece)
				}
			}
			view.You = you
		}
	}
	if g.Status == StatusFinished {
		view.Standings = g.standings()
	}
	return view
}

func (g *Game) standings() []StandingView {
	b := g.Board()
	out := make([]StandingView, 0, len(g.Players))
	for _, p := range g.Players {
		total := 0
		for _, pc := range p.Pieces {
			total += b.Travelled(pc)
		}
		progress := 0
		if n := len(p.Pieces); n > 0 {
			progress = total * 100 / (n * (b.StepsHome() + 1))
		}
		out = append(out, StandingView{ID: p.ID, Name: p.Name, Place: p.Place, Home: p.HomeCount(), Progress: progress, Captures: p.Captures, Left: p.Left})
	}
	for i := 1; i < len(out); i++ { // by place (insertion sort: at most six)
		for j := i; j > 0 && out[j].Place < out[j-1].Place; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// PublicState is View as JSON.
func (g *Game) PublicState(viewerID string, v Viewer) ([]byte, error) {
	return json.Marshal(g.View(viewerID, v))
}
