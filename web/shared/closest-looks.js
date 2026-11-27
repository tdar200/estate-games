// The closest-number game (GAME_TITLE in closest-view.js): the four screens in the looks the owner picked (docs/CLOSEST-API.md §8): Pad A,
// Line-up A, Bet A and Reveal A. Each takes a model from closest-view.js (plain data, already worked
// out and tested) and returns markup; all their CSS is in web/shared/closest.css under "Looks".
// Nothing else reads this markup except the data-cg buttons, which closest-app.js listens for.
//
//   padLook(model)           phone · the number pad: the number on a dark panel, its words, the
//                            range, white keys 3 × 4 (000 and ⌫ dark), LOCK IN with the number
//   lineLook(model, state)   table · odds discs on a gold rail, an ivory card per guess (number,
//                            unit, owner), from the chips phase a tray of chips under each card
//   betLook(model, state)    phone · a dark row per slot (odds, number, owner, CHIP 1, CHIP 2), the
//                            stake panel and CONFIRM kept on screen at the bottom
//   revealLook(model, state) table · the question with the answer beside it, a flag on the rail at
//                            the answer, OFF BY on each card, CLOSEST / SECOND, what each tray pays,
//                            the fact, and the players' plates with their points
//   revealLineHtml(…)        both · the reveal's phase line (THE ANSWER, then who is closest)
//
// A chip is a disc in its player's colour with their token's silhouette (two players never share a
// token, while initials can), a ×2 badge for both chips and a gold diamond for points staked.
// Floors (docs §8): table text ≥ 20 stage px, slot numbers ≥ 36; phone text ≥ 14 px, touch targets
// ≥ 44 px, bet rows ≥ 56 px.

import { escapeHtml, upper } from "./format.js";
import { fmtScore, formatNumber, revealModel, signedPoints } from "./closest-view.js";
import { inkOn, playerChipHtml, playerColor, playerTokenSvg } from "./tokens.js";

const e = escapeHtml;
const ms = (value) => (Number.isFinite(value) ? `${Math.round(value)}ms` : "0ms");
const oddsOf = (odds) => Math.max(2, Math.min(5, Number(odds) || 2));

const SVG = (body, { size = 24, stroke = 2 } = {}) => `<svg class="cg-ico" width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${stroke}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;
const ICON_BACK = SVG('<path d="M21 4H8l-7 8 7 8h13a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2z"/><path d="M18 9l-6 6M12 9l6 6"/>', { size: 32 });
const ICON_LOCK = SVG('<rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>', { stroke: 2.4 });
const ICON_CHECK = SVG('<path d="M20 6L9 17l-5-5"/>', { stroke: 2.6 });
const ICON_MINUS = SVG('<path d="M5 12h14"/>', { stroke: 2.6 });
const ICON_PLUS = SVG('<path d="M12 5v14M5 12h14"/>', { stroke: 2.6 });

function viewerOf(state) {
  return (state?.players || []).find((player) => player.id === state?.viewerId) || null;
}

// ---------------------------------------------------------------------------------------------
// Phone: the number pad (Pad A)

// The number's size on the panel by its length with separators: up to 5 characters at the full
// size, then smaller steps so 999,999,999 and its unit still fit a 360 px phone.
function padSize(text) {
  const n = String(text || "").length;
  return n <= 5 ? "xl" : n <= 7 ? "l" : n <= 9 ? "m" : "s";
}

export function padLook(model) {
  const keys = model.keys.map((key) => {
    const kind = key.key === "back" ? " cg-key--back" : key.key === "000" ? " cg-key--zeros" : "";
    const label = key.key === "back" ? ICON_BACK : e(key.label);
    return `<button type="button" class="cg-key${kind}" data-cg="pad" data-pad="${e(key.key)}" aria-label="${e(key.aria)}"${key.disabled ? " disabled" : ""}>${label}</button>`;
  }).join("");
  const caret = '<i class="cg-pad__caret" aria-hidden="true"></i>';
  const shown = model.display
    ? `<b class="cg-pad__num" data-size="${padSize(model.display)}">${e(model.display)}</b>${caret}${model.unit ? `<span class="cg-pad__unit">${e(model.unit)}</span>` : ""}`
    : `${caret}<span class="cg-pad__empty">Type your guess</span>`;
  const year = model.format === "year";
  const words = model.words && model.words !== model.display ? model.words : "";
  const range = model.range ? (year ? `Any year from ${model.range}` : `From ${model.range}`) : "";
  const note = model.error
    ? `<span class="cg-pad__note is-bad">${e(`Out of range: ${model.error}`)}</span>`
    : `<span class="cg-pad__note">${e(range)}</span>`;
  const lockWord = model.changing ? "CHANGE TO" : "LOCK IN";
  const lockNum = model.canLock && model.display ? ` <span class="cg-lock__num">${e(model.display)}</span>` : "";
  const lockLong = lockWord.length + (lockNum ? model.display.length + 1 : 0) > 16;
  return `<section class="cg-pad${model.changing ? " is-changing" : ""}" aria-label="Your guess">
    <div class="cg-pad__screen${model.error ? " is-bad" : ""}${model.display ? "" : " is-empty"}" aria-live="polite">
      <span class="cg-pad__line">${shown}</span>
      ${year ? "" : `<span class="cg-pad__words">${e(words || " ")}</span>`}
      ${note}
    </div>
    <div class="cg-keys" role="group" aria-label="Number pad">${keys}</div>
    <button type="button" class="btn btn--primary cg-lock${lockLong ? " is-long" : ""}" data-cg="lock"${model.canLock ? "" : " disabled"}>${ICON_LOCK}<span class="btn__label">${lockWord}${lockNum}</span></button>
    ${model.changing ? "" : '<p class="cg-pad__hint">You can change it once after locking in.</p>'}
  </section>`;
}

// ---------------------------------------------------------------------------------------------
// Table: the line of slots (Line-up A; the bet, the chips and the reveal build on it)

function chipHtml(state, chip) {
  const label = `${chip.player.name || "A player"}: ${chip.count === 2 ? "both chips" : "a chip"}${chip.stake ? `, ${chip.stake} staked` : ""}`;
  return `<li class="cg-chip" data-key="cgc-${e(chip.player.id)}" role="img" aria-label="${e(label)}">${playerChipHtml(state, chip.player, { size: 40, glyph: true, className: "cg-chip__disc" })}${chip.count === 2 ? '<b class="cg-chip__n">×2</b>' : ""}${chip.stake ? `<i class="cg-chip__stake"><b>${chip.stake}</b></i>` : ""}</li>`;
}

// The guess's owner: their disc and name; two or more owners (the same guess) show discs only.
function ownersHtml(state, owners) {
  const disc = (player) => playerChipHtml(state, player, { size: 34, glyph: true, className: "cg-owner__disc", label: `${player.name || "A player"}'s guess` });
  if (owners.length === 1) return `<span class="cg-slot__owners"><span class="cg-owner">${disc(owners[0])}<span class="cg-owner__name">${e(upper(owners[0].name || ""))}</span></span></span>`;
  return `<span class="cg-slot__owners is-many">${owners.map(disc).join("")}</span>`;
}

// OFF BY and the number, or EXACT.
function offHtml(slot, format) {
  if (slot.off === null || slot.off === undefined) return "";
  if (slot.off === 0) return '<span class="cg-slot__off is-exact"><b class="cg-slot__offnum">EXACT</b></span>';
  return `<span class="cg-slot__off"><span class="cg-slot__offlabel">OFF BY</span><b class="cg-slot__offnum">${e(formatNumber(slot.off, format))}</b></span>`;
}

export function slotCardHtml(state, slot, { seq = 0, lineup = false, format = "plain", chips = false } = {}) {
  const classes = ["cg-slot", slot.result === "win" ? "is-win" : "", slot.result === "second" ? "is-second" : "", slot.off !== null && !slot.result ? "is-out" : ""].filter(Boolean).join(" ");
  const style = lineup && slot.delay !== null ? ` style="--d:${ms(slot.delay)}"` : "";
  const result = slot.result === "win" ? '<span class="cg-slot__result">CLOSEST</span>' : slot.result === "second" ? '<span class="cg-slot__result">SECOND</span>' : "";
  // CLOSEST / SECOND sits on the card's top edge but outside the card (the card clips its OFF BY band
  // and scales at the wins beat). What the tray pays at the reveal: its odds on a win, 1 on the
  // second; both double on the last question.
  const times = state?.question?.double ? 2 : 1;
  const pays = slot.result === "win" ? oddsOf(slot.odds) * times : slot.result === "second" ? times : 0;
  const tray = chips
    ? `<div class="cg-tray${slot.chips.length > 4 ? " is-full" : ""}">${pays && slot.chips.length ? `<span class="cg-tray__pays">PAYS ${pays} EACH</span>` : ""}<ul class="cg-tray__chips">${slot.chips.map((chip) => chipHtml(state, chip)).join("")}</ul></div>`
    : "";
  return `<li class="${classes}" data-key="cgs-${e(seq)}-${slot.index}"${style}>
    <span class="cg-slot__odds" data-odds="${oddsOf(slot.odds)}">${e(slot.oddsLabel)}</span>
    <div class="cg-slot__card">
      <b class="cg-slot__num">${e(slot.text)}</b>${slot.unit ? `<span class="cg-slot__unit">${e(upper(slot.unit))}</span>` : ""}
      ${ownersHtml(state, slot.owners)}${offHtml(slot, format)}
    </div>${result}${tray}
  </li>`;
}

// Long numbers on narrow cards drop from 46 to 36 stage px (the floor): a column's width on the
// 1600 stage (1540 across, 14 between, at most 250) less the card's padding and border, against the
// number's width in em (digits about 0.52, separators about 0.26).
export function lineIsTight(model) {
  const n = Math.max(1, model.count || model.slots.length);
  const card = Math.min(250, (1540 - 14 * (n - 1)) / n) - 18;
  const ems = Math.max(0, ...model.slots.map((slot) => [...String(slot.text)].reduce((sum, ch) => sum + (/\d/.test(ch) ? 0.52 : 0.26), 0)));
  return ems * 46 > card;
}

const LEGEND = `<div class="cg-legend" aria-hidden="true"><span class="cg-legend__item"><i class="cg-legend__chip"></i>A CHIP</span><span class="cg-legend__item"><b class="cg-chip__n">×2</b>BOTH CHIPS</span><span class="cg-legend__item"><i class="cg-legend__stake"></i>POINTS STAKED</span></div>`;

// `top` goes over the line inside the same box (the reveal's flag), so it lines up with the slots.
export function lineLook(model, state, { top = "" } = {}) {
  const seq = state?.question?.seq ?? 0;
  const lineup = state?.phase === "lineup";
  const n = Math.max(1, model.count);
  const reveal = Boolean(model.showReveal);
  // Trays from the chips phase on, except with one guess (no chips that question: it just wins).
  const chips = Boolean(model.showChips) && n > 1;
  const cards = model.slots.map((slot) => slotCardHtml(state, slot, { seq, lineup, format: model.format, chips })).join("");
  const classes = ["cg-line", lineup ? "is-lineup" : "", chips ? "has-chips" : "", lineIsTight(model) ? "is-tight" : "", n >= 7 ? "is-many" : ""].filter(Boolean).join(" ");
  const legend = chips && !reveal && n >= 3 ? LEGEND : "";
  return `<div class="cg-stage${reveal ? " is-reveal" : ""}" data-key="stage-${e(seq)}">${top}<ol class="${classes}" style="--n:${n}" data-key="line-${e(seq)}" aria-label="The guesses, low to high">${cards}</ol>${legend}</div>`;
}

// ---------------------------------------------------------------------------------------------
// Phone: the bet (Bet A)

export function betLook(model, state) {
  const me = viewerOf(state);
  const color = me ? playerColor(state, me) : "#F2C14E";
  const rows = model.rows.map((row) => {
    const discs = row.owners.map((player) => playerChipHtml(state, player, { size: 26, glyph: true, className: "cg-row__disc", label: player.name })).join("");
    const who = row.mine ? '<span class="cg-row__you">YOU</span>' : row.owners.length === 1 ? `<span class="cg-row__name">${e(row.owners[0].name || "")}</span>` : "";
    const stakeOn = row.stakeHere && model.stake > 0;
    const stakeChip = row.chipB ? 1 : 0;
    const chip = (which, on) => {
      const mark = on && stakeOn && which === stakeChip ? `<i class="cg-chipbtn__stake" aria-hidden="true"><b>${model.stake}</b></i>` : "";
      return `<button type="button" class="cg-chipbtn${on ? " is-on" : ""}" data-cg="chip" data-which="${which}" data-slot="${row.index}" aria-pressed="${on}" aria-label="${on ? `Chip ${which + 1} is on ${e(row.text)}` : `Put chip ${which + 1} on ${e(row.text)}`}"><span class="cg-chipbtn__disc">${on ? which + 1 : ""}</span>${mark}</button>`;
    };
    return `<li class="cg-row${row.chips ? " has-chip" : ""}${row.mine ? " is-mine" : ""}" data-key="cgb-${row.index}">
      <span class="cg-row__odds" data-odds="${oddsOf(row.odds)}">${e(row.oddsLabel)}</span>
      <span class="cg-row__what"><b class="cg-row__num">${e(row.text)}</b>${row.unit ? `<span class="cg-row__unit">${e(upper(row.unit))}</span>` : ""}</span>
      <span class="cg-row__who">${discs}${who}</span>
      ${chip(0, row.chipA)}${chip(1, row.chipB)}
    </li>`;
  }).join("");
  const staked = model.rows.find((row) => row.stakeHere) || null;
  const where = staked ? `on ${staked.text}${staked.unit ? ` ${upper(staked.unit)}` : ""}` : "";
  let stake;
  if (!model.canStake) {
    stake = '<p class="cg-stake cg-stake--none">No points to stake yet: your chips are free.</p>';
  } else {
    const sub = model.stake <= 0 ? `optional · up to ${model.stakeMax}` : model.split ? "on one of your chips" : where;
    const pick = model.stake > 0 && model.split
      ? `<div class="cg-stake__where" role="group" aria-label="Stake on"><span class="cg-stake__on">ON</span>${model.rows.filter((row) => row.chips).map((row) => `<button type="button" class="cg-stakebtn${row.stakeHere ? " is-on" : ""}" data-cg="stake-slot" data-slot="${row.index}" aria-pressed="${row.stakeHere}" aria-label="Stake on ${e(row.text)}">${e(row.text)}</button>`).join("")}</div>`
      : "";
    const risk = model.stake > 0
      ? `<p class="cg-stake__risk">+${e(model.stakeWins)} if closest · if not, <b>${e(model.risk)}</b></p>`
      : `<p class="cg-stake__risk">${e(model.risk.charAt(0).toUpperCase() + model.risk.slice(1))}.</p>`;
    stake = `<div class="cg-stake" role="group" aria-label="Stake">
        <div class="cg-stake__top">
          <span class="cg-stake__label"><span class="cg-stake__title">STAKE POINTS</span><b class="cg-stake__sub">${e(sub)}</b></span>
          <button type="button" class="cg-step" data-cg="stake-step" data-dir="-1" aria-label="Stake one less"${model.stake <= 0 ? " disabled" : ""}>${ICON_MINUS}</button>
          <output class="cg-stake__n" aria-live="polite">${model.stake}</output>
          <button type="button" class="cg-step" data-cg="stake-step" data-dir="1" aria-label="Stake one more (${model.stakeMax} is the most)"${model.stake >= model.stakeMax ? " disabled" : ""}>${ICON_PLUS}</button>
        </div>${pick}
        ${risk}
      </div>`;
  }
  return `<section class="cg-bet" aria-label="Your chips" style="--pc:${color};--pc-ink:${inkOn(color)}">
    <div class="cg-bet__head" aria-hidden="true"><span class="cg-bet__tip">Tap 1 or 2 to move a chip</span><span class="cg-bet__col">CHIP 1</span><span class="cg-bet__col">CHIP 2</span></div>
    <ol class="cg-rows">${rows}</ol>
    <div class="cg-bet__dock">${stake}
      <button type="button" class="btn btn--primary cg-confirm" data-cg="confirm"${model.check.ok ? "" : " disabled"}>${ICON_CHECK}<span class="btn__label">CONFIRM</span></button>
    </div>
  </section>`;
}

// ---------------------------------------------------------------------------------------------
// Table: the reveal (Reveal A)

// The phase line at the reveal (the table's pill and the phone's head): THE ANSWER first, then who
// is closest from the wins beat on, so the line never tells the result before the answer is out.
// Any other phase, nobody guessed, or the game over: the text as it is.
export function revealLineHtml(state, text, elapsed = Infinity) {
  const question = state?.question || {};
  if (state?.status === "finished" || state?.phase !== "reveal" || question.nobody || !text || text === "THE ANSWER") return `<b>${e(text)}</b>`;
  const wins = revealModel(state, { elapsed }).beats.wins;
  return `<span class="cg-swap" style="--wins:${ms(wins)}"><b class="cg-swap__a" aria-hidden="true">THE ANSWER</b><b class="cg-swap__b">${e(text)}</b></span>`;
}

// A plate's word at the reveal: what scored (CLOSEST, EXACT, 2ND), else a lost stake, else CHIPS.
function resultTag(row, parts) {
  const bonus = parts.find((part) => part.kind === "bonus");
  if (bonus) return /second/.test(bonus.label) ? { tag: "2ND", tone: "second" } : { tag: /exact/.test(bonus.label) ? "EXACT" : "CLOSEST", tone: "win" };
  const stake = Number(row.stakePoints) || 0;
  if (stake < 0) return { tag: `STAKE ${signedPoints(stake)}`, tone: "lose" };
  if ((Number(row.chipPoints) || 0) > 0) return { tag: "CHIPS", tone: "chips" };
  return { tag: "", tone: "" };
}

function resultPlateHtml(state, { player, row, breakdown }) {
  const delta = Number(row.delta) || 0;
  const { tag, tone } = resultTag(row, breakdown.parts || []);
  const before = Number.isFinite(Number(row.before)) ? Number(row.before) : Number(row.after) - delta;
  const classes = ["qz-plate", "cg-res", tone ? `is-${tone}` : "", delta > 0 ? "is-up" : delta < 0 ? "is-down" : ""].filter(Boolean).join(" ");
  return `<li class="${classes}" data-key="cgr-${e(player.id)}" style="--pc:${playerColor(state, player)}" aria-label="${e(`${player.name}: ${breakdown.text || "no points"}`)}">
    <span class="qz-plate__top">${playerTokenSvg(state, player, { size: 36 })}<b class="qz-plate__name cg-res__name">${e(upper(player.name))}</b></span>
    <span class="qz-plate__bottom"><span class="qz-plate__score cg-res__score"><b class="cg-res__was" aria-hidden="true">${fmtScore(before)}</b><b class="cg-res__now">${fmtScore(row.after)}</b></span><span class="qz-plate__delta cg-res__delta">${e(signedPoints(delta))}</span></span>${tag ? `<span class="qz-plate__tag cg-res__tag">${e(tag)}</span>` : ""}
  </li>`;
}

// The question as a strip with the answer at its right (the reveal owns the question here, so the
// answer can sit inside it).
function answerStripHtml(model, state) {
  const question = state?.question || {};
  const long = String(question.text || "").length > 90;
  const tag = model.double ? '<span class="cg-q__tag is-double">LAST QUESTION · DOUBLE POINTS</span>' : "";
  return `<section class="cg-q is-compact cg-q--answer" data-key="q-${e(question.seq ?? 0)}" aria-label="Question and answer">
    <div class="cg-q__main">${tag}<p class="cg-q__text${long ? " is-long" : ""}">${e(question.text || "")}</p></div>
    <div class="cg-answer"><span class="cg-answer__label">ANSWER</span><b class="cg-answer__num">${e(model.answerText)}</b></div>
  </section>`;
}

const factHtml = (fact) => (fact ? `<p class="cg-fact"><span class="cg-fact__tag">FACT</span><span class="cg-fact__text">${e(fact)}</span></p>` : "");

export function revealLook(model, state) {
  const seq = state?.question?.seq ?? 0;
  const beat = (name) => `--${name}:${ms(model.beats[name])}`;
  const timing = ["answer", "slide", "slideEnd", "off", "wins", "payouts", "fact"].map(beat).join(";");
  if (model.nobody) {
    return `<div class="cg-reveal cg-reveal--nobody" data-key="rv-${e(seq)}" style="${timing}">
      ${answerStripHtml(model, state)}
      <div class="cg-reveal__none"><p class="cg-reveal__nobody">Nobody guessed, so nobody scores.</p>${factHtml(model.fact)}</div>
    </div>`;
  }
  const n = Math.max(1, model.line.count);
  const at = Math.max(0, Math.min(1, (model.position + 0.5) / n));
  const flag = `<span class="cg-flag" aria-hidden="true"><b class="cg-flag__num">${e(formatNumber(state?.question?.answer, model.line.format))}</b><i class="cg-flag__caret"></i></span><span class="cg-flag__pole" aria-hidden="true"></span>`;
  const line = lineLook(model.line, state, { top: flag });
  const results = model.results.map((result) => resultPlateHtml(state, result)).join("");
  return `<div class="cg-reveal${model.double ? " is-double" : ""}" data-key="rv-${e(seq)}" style="${timing};--at:${at.toFixed(4)}">
    ${answerStripHtml(model, state)}
    ${line}
    ${factHtml(model.fact)}
    <ol class="cg-results" style="--n:${Math.max(4, model.results.length)}" aria-label="Points this question">${results}</ol>
  </div>`;
}
