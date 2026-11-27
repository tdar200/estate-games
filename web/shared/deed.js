// The full title-deed card for any square (PC table and phone). Pure: returns an HTML string;
// styles live in web/shared/board.css (.deed-card).
//
//   deedDetailHtml(state, spaceIndex, { viewerId, size })
//     viewerId  "YOU" instead of a name where it is the viewer's
//     size      "md" (default) | "lg" | "sm"
//
// Streets: colour bar, name, price; the rent table (rent, full set, 1–4 houses, hotel) with the
// level that applies now highlighted; house and hotel cost; mortgage value and unmortgage cost;
// owner, buildings, MORTGAGED / PLEDGED; the set line (who owns each street of the colour).
// Railroads: rent by how many one owner has; utilities: 4× or 10× the dice. Corners, tax, Headlines
// and Post get a short explanation.

import { GROUP_COLORS, GROUP_INK, colourWord, escapeHtml, money, playerById, possessive, spacePhoto } from "./format.js";
import { groupIndexes, loanSecuredBy, mortgageValue, ownsGroup, propState, rulesOf, unmortgageCost } from "./insights.js";
import { playerChipHtml } from "./tokens.js";

const e = escapeHtml;
const RAILROADS = [5, 15, 25, 35];
const UTILITIES = [12, 28];


function shortName(name) {
  return String(name || "").replace(/\bRailroad\b/, "RR").replace(/\bStreet\b/, "St").replace(/\bAvenue\b/, "Ave").replace(/\bRoad\b/, "Rd").replace(/\bCompany\b/, "Co");
}

function headStyle(space) {
  if (space.type === "property") return `background:${GROUP_COLORS[space.group]};color:${GROUP_INK[space.group] || "#FFFFFF"}`;
  if (space.type === "railroad") return "background:#2B323A;color:#FFFFFF";
  if (space.type === "utility") return "background:#5B6470;color:#FFFFFF";
  return "background:#13294B;color:#FFFFFF";
}

function who(state, playerId, viewerId) {
  const player = playerById(state, playerId);
  if (!player) return "";
  return `${playerChipHtml(state, player, { size: 22, glyph: true })}<b>${e(playerId === viewerId ? "YOU" : String(player.name).toUpperCase())}</b>`;
}

// The rent level that applies now: 0 rent, 1 full set, 2–5 one to four houses, 6 hotel; null when
// nobody owns it or it is mortgaged.
export function rentLevel(state, index) {
  const space = state?.board?.[index];
  const prop = propState(state, index);
  if (!space || space.type !== "property" || !prop?.ownerId || prop.mortgaged) return null;
  const houses = prop.houses || 0;
  if (houses >= 5) return 6;
  if (houses > 0) return houses + 1;
  return ownsGroup(state, prop.ownerId, space.group) ? 1 : 0;
}

function countOwned(state, list, ownerId) {
  return list.filter((index) => propState(state, index)?.ownerId === ownerId).length;
}

function setLine(state, indexes, viewerId) {
  return `<div class="deed-card__set">${indexes.map((index) => {
    const owner = playerById(state, propState(state, index)?.ownerId);
    const mark = owner ? playerChipHtml(state, owner, { size: 20, glyph: true, label: owner.id === viewerId ? "You" : owner.name }) : '<span class="deed-card__nobody" title="Bank"></span>';
    return `<span class="deed-card__member">${mark}<span>${e(shortName(state.board[index]?.name || "").toUpperCase())}</span></span>`;
  }).join("")}</div>`;
}

function row(label, value, now = false) {
  return `<div class="deed-card__row${now ? " is-now" : ""}"><span>${label}</span><b>${value}</b></div>`;
}

function info(text) {
  return `<p class="deed-card__info">${e(text)}</p>`;
}

export function deedDetailHtml(state, spaceIndex, { viewerId = null, size = "md" } = {}) {
  const space = state?.board?.[spaceIndex];
  if (!space) return "";
  const prop = propState(state, spaceIndex);
  const owned = Boolean(prop?.ownerId);
  const loan = owned ? loanSecuredBy(state, spaceIndex) : null;
  const tags = [];
  if (prop?.mortgaged) tags.push('<span class="deed-card__tag deed-card__tag--mortgaged">MORTGAGED</span>');
  if (loan) tags.push('<span class="deed-card__tag deed-card__tag--pledged">PLEDGED</span>');
  const ownable = ["property", "railroad", "utility"].includes(space.type);
  const ownerLine = ownable
    ? `<div class="deed-card__owner">${owned ? who(state, prop.ownerId, viewerId) : '<span class="deed-card__bank">FOR SALE · BANK</span>'}${tags.join("")}</div>`
    : "";
  let body = "";
  let foot = "";
  let eyebrow = "DEED";

  if (space.type === "property") {
    const level = rentLevel(state, spaceIndex);
    const rents = space.rents || [];
    const houses = prop?.houses || 0;
    body = [
      row("Rent", money(rents[0]), level === 0),
      row("Full colour set", money(rents[0] * 2), level === 1),
      row("With 1 house", money(rents[1]), level === 2),
      row("With 2 houses", money(rents[2]), level === 3),
      row("With 3 houses", money(rents[3]), level === 4),
      row("With 4 houses", money(rents[4]), level === 5),
      row("With a hotel", money(rents[5]), level === 6),
    ].join("");
    const built = houses >= 5 ? '<span class="deed-card__hotel" title="Hotel"></span>' : houses ? `<span class="deed-card__houses">${'<span class="deed-card__house"></span>'.repeat(houses)}</span>` : "";
    foot = `<div class="deed-card__costs">
        ${row("Houses", `${money(space.buildCost)} each`)}${row("Hotel", `${money(space.buildCost)} + 4 houses`)}
        ${row("Mortgage", `+${money(mortgageValue(space))}`)}${row("Unmortgage", money(unmortgageCost(space)))}
      </div>
      ${built ? `<div class="deed-card__built">${built}</div>` : ""}
      <div class="deed-card__set-title">${e(colourWord(space.group).toUpperCase())} SET</div>${setLine(state, groupIndexes(state, space.group), viewerId)}`;
  } else if (space.type === "railroad") {
    const count = owned && !prop.mortgaged ? countOwned(state, RAILROADS, prop.ownerId) : 0;
    body = [1, 2, 3, 4].map((n) => row(`${n} railroad${n === 1 ? "" : "s"}`, money(25 * 2 ** (n - 1)), count === n)).join("");
    foot = `<div class="deed-card__costs">${row("Mortgage", `+${money(mortgageValue(space))}`)}${row("Unmortgage", money(unmortgageCost(space)))}</div>
      <div class="deed-card__set-title">RAILROADS</div>${setLine(state, RAILROADS, viewerId)}`;
  } else if (space.type === "utility") {
    const count = owned && !prop.mortgaged ? countOwned(state, UTILITIES, prop.ownerId) : 0;
    body = [row("One utility", "4× the dice", count === 1), row("Both utilities", "10× the dice", count === 2)].join("");
    foot = `<div class="deed-card__costs">${row("Mortgage", `+${money(mortgageValue(space))}`)}${row("Unmortgage", money(unmortgageCost(space)))}</div>
      <div class="deed-card__set-title">UTILITIES</div>${setLine(state, UTILITIES, viewerId)}`;
  } else {
    eyebrow = "SQUARE";
    const rules = rulesOf(state);
    const text = {
      go: "Collect $200 every time you pass or land on PAYDAY.",
      jail: "Landing here is safe: you are just passing. Sent to jail? Roll doubles, pay $50 or use a Jail pass to get out. After three tries you pay and move.",
      parking: rules.freeParkingJackpot ? `Free rest. Taxes and fees build a jackpot here: ${money(state.freeParking || 0)} now. Land on it to take it.` : "Free rest: nothing happens here.",
      goToJail: "Busted: go straight to jail. No PAYDAY, no $200.",
      chance: "Draw a Headlines card. It shows until it is read and confirmed.",
      chest: "Draw a Post card. It happens at once.",
      tax: `Pay ${money(space.amount || 0)} to the bank${rules.freeParkingJackpot ? " (it goes to the City Park jackpot)" : ""}.`,
    }[space.type] || "";
    body = info(text);
  }

  const price = ownable ? `<span class="deed-card__price">${money(space.price)}</span>` : "";
  return `<div class="deed-card deed-card--${e(size)} deed-card--${e(space.type)}" data-deed-card="${spaceIndex}"${spacePhoto(space) ? ` data-photo="${spacePhoto(space)}"` : ""}>
    <div class="deed-card__head" style="${headStyle(space)}"><span class="deed-card__eyebrow">${eyebrow}</span><span class="deed-card__name">${e(String(space.name).toUpperCase())}</span>${price}</div>
    ${ownerLine}
    <div class="deed-card__rows">${body}</div>
    ${foot}
  </div>`;
}

// A short label for whose square it is ("Maya's", "Yours", "For sale"), for captions.
export function deedOwnerLabel(state, spaceIndex, viewerId = null) {
  const owner = playerById(state, propState(state, spaceIndex)?.ownerId);
  if (!owner) return "For sale";
  return owner.id === viewerId ? "Yours" : possessive(owner.name);
}
