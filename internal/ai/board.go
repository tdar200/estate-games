package ai

// Private copy of the board data the AI needs (src/board.js). It is kept
// separate from internal/engine on purpose so the AI has no dependency on the
// engine package.

const boardSize = 40

// Space types (src/board.js).
const (
	typeGo       = "go"
	typeProperty = "property"
	typeRailroad = "railroad"
	typeUtility  = "utility"
	typeChest    = "chest"
	typeChance   = "chance"
	typeTax      = "tax"
	typeJail     = "jail"
	typeParking  = "parking"
	typeGoToJail = "goToJail"
)

// Colour groups in the order of their first appearance on the board, which is
// also the key order of every per-group table in ai.js (GROUP_STAGE_EV,
// COLOR_GROUP_STRATEGY, BLOCKER_GROUP_WEIGHTS) and of the `groups` Set built in
// chooseBuild.
const (
	groupBrown = iota
	groupLightBlue
	groupPink
	groupOrange
	groupRed
	groupYellow
	groupGreen
	groupDarkBlue
	groupCount

	noGroup = -1
)

var groupNames = [groupCount]string{
	"brown", "lightBlue", "pink", "orange", "red", "yellow", "green", "darkBlue",
}

// space is one board square. group is noGroup for spaces without a colour
// group (railroads, utilities and the non-ownable squares).
type space struct {
	index     int
	kind      string
	name      string
	group     int
	price     int
	rents     [6]int
	buildCost int
}

func (s *space) ownable() bool {
	return s.kind == typeProperty || s.kind == typeRailroad || s.kind == typeUtility
}

func prop(index int, name string, group, price int, rents [6]int, buildCost int) space {
	return space{index: index, kind: typeProperty, name: name, group: group, price: price, rents: rents, buildCost: buildCost}
}

func rail(index int, name string) space {
	return space{index: index, kind: typeRailroad, name: name, group: noGroup, price: 200}
}

func util(index int, name string) space {
	return space{index: index, kind: typeUtility, name: name, group: noGroup, price: 150}
}

func plain(index int, kind, name string) space {
	return space{index: index, kind: kind, name: name, group: noGroup}
}

var board = [boardSize]space{
	plain(0, typeGo, "PAYDAY"),
	prop(1, "Begonia Road", groupBrown, 60, [6]int{2, 10, 30, 90, 160, 250}, 50),
	plain(2, typeChest, "Post"),
	prop(3, "Maple Bridge Street", groupBrown, 60, [6]int{4, 20, 60, 180, 320, 450}, 50),
	plain(4, typeTax, "Stamp Duty"),
	rail(5, "North Station Railroad"),
	prop(6, "Bluestone Street", groupLightBlue, 100, [6]int{6, 30, 90, 270, 400, 550}, 50),
	plain(7, typeChance, "Headlines"),
	prop(8, "Lakeside Road", groupLightBlue, 100, [6]int{6, 30, 90, 270, 400, 550}, 50),
	prop(9, "Pearl Lane", groupLightBlue, 120, [6]int{8, 40, 100, 300, 450, 600}, 50),
	plain(10, typeJail, "Jail"),
	prop(11, "Garden Avenue", groupPink, 140, [6]int{10, 50, 150, 450, 625, 750}, 100),
	util(12, "Power Station"),
	prop(13, "Theater Street", groupPink, 140, [6]int{10, 50, 150, 450, 625, 750}, 100),
	prop(14, "Wisteria Road", groupPink, 160, [6]int{12, 60, 180, 500, 700, 900}, 100),
	rail(15, "West Station Railroad"),
	prop(16, "Pine Road", groupOrange, 180, [6]int{14, 70, 200, 550, 750, 950}, 100),
	plain(17, typeChest, "Post"),
	prop(18, "Harbor Street", groupOrange, 180, [6]int{14, 70, 200, 550, 750, 950}, 100),
	prop(19, "Central Road", groupOrange, 200, [6]int{16, 80, 220, 600, 800, 1000}, 100),
	plain(20, typeParking, "City Park"),
	prop(21, "Golden Leaf Road", groupRed, 220, [6]int{18, 90, 250, 700, 875, 1050}, 150),
	plain(22, typeChance, "Headlines"),
	prop(23, "Museum Road", groupRed, 220, [6]int{18, 90, 250, 700, 875, 1050}, 150),
	prop(24, "Galaxy Avenue", groupRed, 240, [6]int{20, 100, 300, 750, 925, 1100}, 150),
	rail(25, "South Station Railroad"),
	prop(26, "Sea Breeze Road", groupYellow, 260, [6]int{22, 110, 330, 800, 975, 1150}, 150),
	prop(27, "Coral Street", groupYellow, 260, [6]int{22, 110, 330, 800, 975, 1150}, 150),
	util(28, "Reservoir"),
	prop(29, "Sunlight Avenue", groupYellow, 280, [6]int{24, 120, 360, 850, 1025, 1200}, 150),
	plain(30, typeGoToJail, "Busted"),
	prop(31, "Emerald Road", groupGreen, 300, [6]int{26, 130, 390, 900, 1100, 1275}, 200),
	prop(32, "Forest Avenue", groupGreen, 300, [6]int{26, 130, 390, 900, 1100, 1275}, 200),
	plain(33, typeChest, "Post"),
	prop(34, "Century Avenue", groupGreen, 320, [6]int{28, 150, 450, 1000, 1200, 1400}, 200),
	rail(35, "East Station Railroad"),
	plain(36, typeChance, "Headlines"),
	prop(37, "Skyline Road", groupDarkBlue, 350, [6]int{35, 175, 500, 1100, 1300, 1500}, 200),
	plain(38, typeTax, "Council Tax"),
	prop(39, "Crown Avenue", groupDarkBlue, 400, [6]int{50, 200, 600, 1400, 1700, 2000}, 200),
}

// groupSpaces[g] lists the spaces of colour group g in board order
// (BOARD.filter((space) => space.group === group)).
var groupSpaces [groupCount][]*space

// railroadSpaces and utilitySpaces list those spaces in board order.
var railroadSpaces, utilitySpaces []*space

func init() {
	for i := range board {
		s := &board[i]
		switch {
		case s.group != noGroup:
			groupSpaces[s.group] = append(groupSpaces[s.group], s)
		case s.kind == typeRailroad:
			railroadSpaces = append(railroadSpaces, s)
		case s.kind == typeUtility:
			utilitySpaces = append(utilitySpaces, s)
		}
	}
}

// spaceAt mirrors BOARD[index]: nil when the index is off the board.
func spaceAt(index int) *space {
	if index < 0 || index >= boardSize {
		return nil
	}
	return &board[index]
}
