// Ivory dice with ink pips, laid out on a 3×3 grid like the approved designs.

const PIPS = { 1: [4], 2: [0, 8], 3: [0, 4, 8], 4: [0, 2, 6, 8], 5: [0, 2, 4, 6, 8], 6: [0, 2, 3, 5, 6, 8] };

export function pipsFor(value) {
  return PIPS[value] || [];
}

export function dieHtml(value, { rot = 0, className = "", label = true } = {}) {
  const on = pipsFor(value);
  const cells = Array.from({ length: 9 }, (_, index) => `<span class="${on.includes(index) ? "pip" : ""}"></span>`).join("");
  const a11y = label ? `role="img" aria-label="Die showing ${value}"` : 'aria-hidden="true"';
  return `<div class="die ${className}" style="--rot:${rot}deg" ${a11y}>${cells}</div>`;
}

export function diceHtml(dice, { className = "", rotations = [-8, 9] } = {}) {
  if (!Array.isArray(dice) || dice.length !== 2) return "";
  return `<div class="dice-pair" role="img" aria-label="Dice: ${dice[0]} and ${dice[1]}">${dieHtml(dice[0], { rot: rotations[0], className, label: false })}${dieHtml(dice[1], { rot: rotations[1], className, label: false })}</div>`;
}

export function diceTotal(dice) {
  return Array.isArray(dice) ? dice[0] + dice[1] : 0;
}

export function isDouble(dice) {
  return Array.isArray(dice) && dice[0] === dice[1];
}
