package engine

// Space is one of the 40 board squares. Fields that do not apply to a space
// type are omitted from JSON, mirroring the plain objects in src/board.js.
type Space struct {
	Index     int    `json:"index"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Group     string `json:"group,omitempty"`
	Price     int    `json:"price,omitempty"`
	Rents     []int  `json:"rents,omitempty"`
	BuildCost int    `json:"buildCost,omitempty"`
	Amount    int    `json:"amount,omitempty"`
}

// Group describes a colour group.
type Group struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Space types.
const (
	SpaceGo       = "go"
	SpaceProperty = "property"
	SpaceChest    = "chest"
	SpaceChance   = "chance"
	SpaceTax      = "tax"
	SpaceRailroad = "railroad"
	SpaceUtility  = "utility"
	SpaceJail     = "jail"
	SpaceParking  = "parking"
	SpaceGoToJail = "goToJail"
)

// GroupOrder lists the colour group keys in the same order as the GROUPS
// object literal in src/board.js (JS object key order).
var GroupOrder = []string{"brown", "lightBlue", "pink", "orange", "red", "yellow", "green", "darkBlue"}

// Groups maps a colour group key to its display data (GROUPS in board.js).
var Groups = map[string]Group{
	"brown":     {Name: "Old Town", Color: "#8b5a3c"},
	"lightBlue": {Name: "Waterfront", Color: "#59b6d1"},
	"pink":      {Name: "Arts District", Color: "#c95091"},
	"orange":    {Name: "Business District", Color: "#e7842c"},
	"red":       {Name: "Central District", Color: "#d94242"},
	"yellow":    {Name: "Sunshine District", Color: "#e0b923"},
	"green":     {Name: "Green District", Color: "#2d9665"},
	"darkBlue":  {Name: "Skyline District", Color: "#2858a7"},
}

func property(index int, name, group string, price int, rents []int, buildCost int) Space {
	return Space{Index: index, Type: SpaceProperty, Name: name, Group: group, Price: price, Rents: rents, BuildCost: buildCost}
}

func railroad(index int, name string) Space {
	return Space{Index: index, Type: SpaceRailroad, Name: name, Price: 200}
}

func utility(index int, name string) Space {
	return Space{Index: index, Type: SpaceUtility, Name: name, Price: 150}
}

// Board is the 40-space board (BOARD in board.js). Treat it as read-only.
var Board = []Space{
	{Index: 0, Type: SpaceGo, Name: "PAYDAY"},
	property(1, "Begonia Road", "brown", 60, []int{2, 10, 30, 90, 160, 250}, 50),
	{Index: 2, Type: SpaceChest, Name: "Post"},
	property(3, "Maple Bridge Street", "brown", 60, []int{4, 20, 60, 180, 320, 450}, 50),
	{Index: 4, Type: SpaceTax, Name: "Stamp Duty", Amount: 200},
	railroad(5, "North Station Railroad"),
	property(6, "Bluestone Street", "lightBlue", 100, []int{6, 30, 90, 270, 400, 550}, 50),
	{Index: 7, Type: SpaceChance, Name: "Headlines"},
	property(8, "Lakeside Road", "lightBlue", 100, []int{6, 30, 90, 270, 400, 550}, 50),
	property(9, "Pearl Lane", "lightBlue", 120, []int{8, 40, 100, 300, 450, 600}, 50),
	{Index: 10, Type: SpaceJail, Name: "Jail"},
	property(11, "Garden Avenue", "pink", 140, []int{10, 50, 150, 450, 625, 750}, 100),
	utility(12, "Power Station"),
	property(13, "Theater Street", "pink", 140, []int{10, 50, 150, 450, 625, 750}, 100),
	property(14, "Wisteria Road", "pink", 160, []int{12, 60, 180, 500, 700, 900}, 100),
	railroad(15, "West Station Railroad"),
	property(16, "Pine Road", "orange", 180, []int{14, 70, 200, 550, 750, 950}, 100),
	{Index: 17, Type: SpaceChest, Name: "Post"},
	property(18, "Harbor Street", "orange", 180, []int{14, 70, 200, 550, 750, 950}, 100),
	property(19, "Central Road", "orange", 200, []int{16, 80, 220, 600, 800, 1000}, 100),
	{Index: 20, Type: SpaceParking, Name: "City Park"},
	property(21, "Golden Leaf Road", "red", 220, []int{18, 90, 250, 700, 875, 1050}, 150),
	{Index: 22, Type: SpaceChance, Name: "Headlines"},
	property(23, "Museum Road", "red", 220, []int{18, 90, 250, 700, 875, 1050}, 150),
	property(24, "Galaxy Avenue", "red", 240, []int{20, 100, 300, 750, 925, 1100}, 150),
	railroad(25, "South Station Railroad"),
	property(26, "Sea Breeze Road", "yellow", 260, []int{22, 110, 330, 800, 975, 1150}, 150),
	property(27, "Coral Street", "yellow", 260, []int{22, 110, 330, 800, 975, 1150}, 150),
	utility(28, "Reservoir"),
	property(29, "Sunlight Avenue", "yellow", 280, []int{24, 120, 360, 850, 1025, 1200}, 150),
	{Index: 30, Type: SpaceGoToJail, Name: "Busted"},
	property(31, "Emerald Road", "green", 300, []int{26, 130, 390, 900, 1100, 1275}, 200),
	property(32, "Forest Avenue", "green", 300, []int{26, 130, 390, 900, 1100, 1275}, 200),
	{Index: 33, Type: SpaceChest, Name: "Post"},
	property(34, "Century Avenue", "green", 320, []int{28, 150, 450, 1000, 1200, 1400}, 200),
	railroad(35, "East Station Railroad"),
	{Index: 36, Type: SpaceChance, Name: "Headlines"},
	property(37, "Skyline Road", "darkBlue", 350, []int{35, 175, 500, 1100, 1300, 1500}, 200),
	{Index: 38, Type: SpaceTax, Name: "Council Tax", Amount: 100},
	property(39, "Crown Avenue", "darkBlue", 400, []int{50, 200, 600, 1400, 1700, 2000}, 200),
}

// BoardSize is the number of spaces on the board.
const BoardSize = 40

// IsOwnable reports whether a space type can be owned (OWNABLE_TYPES).
func IsOwnable(spaceType string) bool {
	return spaceType == SpaceProperty || spaceType == SpaceRailroad || spaceType == SpaceUtility
}

// OwnableIndexes lists the indexes of ownable spaces in ascending order. This
// is the iteration order of Object.entries(game.properties) in JS.
var OwnableIndexes = func() []int {
	var out []int
	for _, space := range Board {
		if IsOwnable(space.Type) {
			out = append(out, space.Index)
		}
	}
	return out
}()

// GroupSpaces returns the spaces of a colour group in board order
// (groupSpaces in board.js).
func GroupSpaces(group string) []Space {
	var out []Space
	for _, space := range Board {
		if space.Group == group {
			out = append(out, space)
		}
	}
	return out
}

// spaceAt returns the space at index, or nil when index is off the board
// (JS: BOARD[index] === undefined).
func spaceAt(index int) *Space {
	if index < 0 || index >= len(Board) {
		return nil
	}
	return &Board[index]
}
