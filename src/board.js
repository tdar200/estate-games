const GROUPS = {
  brown: { name: "Old Town", color: "#8b5a3c" },
  lightBlue: { name: "Waterfront", color: "#59b6d1" },
  pink: { name: "Arts District", color: "#c95091" },
  orange: { name: "Business District", color: "#e7842c" },
  red: { name: "Central District", color: "#d94242" },
  yellow: { name: "Sunshine District", color: "#e0b923" },
  green: { name: "Green District", color: "#2d9665" },
  darkBlue: { name: "Skyline District", color: "#2858a7" },
};

function property(index, name, group, price, rents, buildCost) {
  return { index, type: "property", name, group, price, rents, buildCost };
}

function railroad(index, name) {
  return { index, type: "railroad", name, price: 200 };
}

function utility(index, name) {
  return { index, type: "utility", name, price: 150 };
}

const BOARD = [
  { index: 0, type: "go", name: "PAYDAY" },
  property(1, "Begonia Road", "brown", 60, [2, 10, 30, 90, 160, 250], 50),
  { index: 2, type: "chest", name: "Post" },
  property(3, "Maple Bridge Street", "brown", 60, [4, 20, 60, 180, 320, 450], 50),
  { index: 4, type: "tax", name: "Stamp Duty", amount: 200 },
  railroad(5, "North Station Railroad"),
  property(6, "Bluestone Street", "lightBlue", 100, [6, 30, 90, 270, 400, 550], 50),
  { index: 7, type: "chance", name: "Headlines" },
  property(8, "Lakeside Road", "lightBlue", 100, [6, 30, 90, 270, 400, 550], 50),
  property(9, "Pearl Lane", "lightBlue", 120, [8, 40, 100, 300, 450, 600], 50),
  { index: 10, type: "jail", name: "Jail" },
  property(11, "Garden Avenue", "pink", 140, [10, 50, 150, 450, 625, 750], 100),
  utility(12, "Power Station"),
  property(13, "Theater Street", "pink", 140, [10, 50, 150, 450, 625, 750], 100),
  property(14, "Wisteria Road", "pink", 160, [12, 60, 180, 500, 700, 900], 100),
  railroad(15, "West Station Railroad"),
  property(16, "Pine Road", "orange", 180, [14, 70, 200, 550, 750, 950], 100),
  { index: 17, type: "chest", name: "Post" },
  property(18, "Harbor Street", "orange", 180, [14, 70, 200, 550, 750, 950], 100),
  property(19, "Central Road", "orange", 200, [16, 80, 220, 600, 800, 1000], 100),
  { index: 20, type: "parking", name: "City Park" },
  property(21, "Golden Leaf Road", "red", 220, [18, 90, 250, 700, 875, 1050], 150),
  { index: 22, type: "chance", name: "Headlines" },
  property(23, "Museum Road", "red", 220, [18, 90, 250, 700, 875, 1050], 150),
  property(24, "Galaxy Avenue", "red", 240, [20, 100, 300, 750, 925, 1100], 150),
  railroad(25, "South Station Railroad"),
  property(26, "Sea Breeze Road", "yellow", 260, [22, 110, 330, 800, 975, 1150], 150),
  property(27, "Coral Street", "yellow", 260, [22, 110, 330, 800, 975, 1150], 150),
  utility(28, "Reservoir"),
  property(29, "Sunlight Avenue", "yellow", 280, [24, 120, 360, 850, 1025, 1200], 150),
  { index: 30, type: "goToJail", name: "Busted" },
  property(31, "Emerald Road", "green", 300, [26, 130, 390, 900, 1100, 1275], 200),
  property(32, "Forest Avenue", "green", 300, [26, 130, 390, 900, 1100, 1275], 200),
  { index: 33, type: "chest", name: "Post" },
  property(34, "Century Avenue", "green", 320, [28, 150, 450, 1000, 1200, 1400], 200),
  railroad(35, "East Station Railroad"),
  { index: 36, type: "chance", name: "Headlines" },
  property(37, "Skyline Road", "darkBlue", 350, [35, 175, 500, 1100, 1300, 1500], 200),
  { index: 38, type: "tax", name: "Council Tax", amount: 100 },
  property(39, "Crown Avenue", "darkBlue", 400, [50, 200, 600, 1400, 1700, 2000], 200),
];

const OWNABLE_TYPES = new Set(["property", "railroad", "utility"]);

function groupSpaces(group) {
  return BOARD.filter((space) => space.group === group);
}

module.exports = { BOARD, GROUPS, OWNABLE_TYPES, groupSpaces };
