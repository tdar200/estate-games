// Chips and cards that move on the poker table: bets slide out from a seat, sweep into the pot when
// the street ends, the pot slides to the winner, and two cards fly to every seat on a new hand.
// The table itself is only ever updated in place (morph); these are short-lived copies in a fixed
// layer above everything (`layer`), positioned from the real elements' screen rectangles, so they
// work on the phone and on the scaled table screen alike. Reduced motion: call nothing.

function rectOf(node) {
  if (!node) return null;
  const r = node.getBoundingClientRect();
  return r.width || r.height ? { x: r.left + r.width / 2, y: r.top + r.height / 2, w: r.width, h: r.height } : null;
}

const esc = (value) => (globalThis.CSS?.escape ? CSS.escape(value) : String(value).replace(/"/g, '\\"'));

export function createPokerFx(layer) {
  // Before the new state is drawn: where the bets and the pot are now, and how big a seat unit is.
  function snapshot(root) {
    const bets = new Map();
    for (const node of root.querySelectorAll(".pk-bet:not(.is-off)")) bets.set(node.dataset.player, rectOf(node));
    const pot = rectOf(root.querySelector(".pk-pots .pk-pot") || root.querySelector(".pk-board"));
    const unit = (root.querySelector(".pk-av")?.getBoundingClientRect().width || 40) / 40;
    return { bets, pot, unit };
  }

  function seatRect(root, id) {
    return rectOf(root.querySelector(`.pk-seat[data-player="${esc(id)}"] .pk-av`));
  }

  function flyChips(from, to, { unit = 1, delay = 0, count = 3, ms = 520 } = {}) {
    if (!from || !to || !layer) return;
    const size = Math.max(10, Math.round(15 * unit));
    for (let index = 0; index < count; index += 1) {
      const chip = document.createElement("span");
      chip.className = `pk-flychip ch ch--${["gold", "red", "navy", "ink"][index % 4]}`;
      chip.style.cssText = `position:fixed;left:${from.x - size / 2}px;top:${from.y - size / 2}px;width:${size}px;height:${size}px;z-index:45;font-size:${unit}px;pointer-events:none`;
      layer.append(chip);
      const dx = to.x - from.x + (index - 1) * size * 0.35;
      const dy = to.y - from.y + (index % 2) * size * 0.25;
      const anim = chip.animate(
        [{ transform: "translate(0,0) scale(1)", opacity: 1 }, { transform: `translate(${dx}px,${dy}px) scale(0.9)`, opacity: 1, offset: 0.85 }, { transform: `translate(${dx}px,${dy}px) scale(0.8)`, opacity: 0 }],
        { duration: ms, delay: delay + index * 50, easing: "cubic-bezier(.3,.7,.3,1)", fill: "both" },
      );
      const done = () => chip.remove();
      anim.onfinish = done;
      anim.oncancel = done;
      setTimeout(done, delay + ms + 800);
    }
  }

  function flyCards(from, to, { unit = 1, delay = 0 } = {}) {
    if (!from || !to || !layer) return;
    const w = Math.round(17 * unit);
    for (let index = 0; index < 2; index += 1) {
      const card = document.createElement("span");
      card.className = "pc pc--back pk-flycard";
      card.style.cssText = `--w:${w}px;position:fixed;left:${from.x - w / 2}px;top:${from.y - w * 0.7}px;z-index:45;pointer-events:none`;
      layer.append(card);
      const end = `translate(${to.x - from.x + index * w * 0.4}px,${to.y - from.y}px) rotate(${index ? 8 : -8}deg)`;
      const anim = card.animate(
        [{ transform: "translate(0,0) rotate(0deg)", opacity: 0.2 }, { transform: end, opacity: 1, offset: 0.9 }, { transform: end, opacity: 0 }],
        { duration: 420, delay: delay + index * 70, easing: "cubic-bezier(.25,.8,.25,1)", fill: "both" },
      );
      const done = () => card.remove();
      anim.onfinish = done;
      anim.oncancel = done;
      setTimeout(done, delay + 1200);
    }
  }

  // After the new state is drawn: play what changed between prev and next.
  function animate(root, prev, next, before) {
    if (!before || !root) return;
    const unit = before.unit;
    const potNow = rectOf(root.querySelector(".pk-pots .pk-pot, .pk-pots .pk-won") || root.querySelector(".pk-board"));
    const newHand = (next.handNumber || 0) !== (prev.handNumber || 0);
    const streetMoved = !newHand && (prev.street !== next.street || (!prev.result && next.result));
    if (streetMoved) for (const [, rect] of before.bets) flyChips(rect, potNow || before.pot, { unit, count: 2 });
    if (!newHand && !streetMoved) {
      for (const player of next.players || []) {
        const was = prev.players?.find((candidate) => candidate.id === player.id);
        if ((player.bet || 0) > (was?.bet || 0)) {
          const target = rectOf(root.querySelector(`.pk-bet[data-player="${esc(player.id)}"]:not(.is-off)`));
          flyChips(seatRect(root, player.id), target, { unit, count: 2, ms: 420 });
        }
      }
    }
    if (!prev.result && next.result && !next.result.cancelled) {
      const from = potNow || before.pot;
      const winners = new Set();
      for (const pot of next.result.pots || []) for (const winner of pot.winners || []) winners.add(winner.id);
      let delay = streetMoved ? 450 : 150;
      for (const id of winners) {
        flyChips(from, seatRect(root, id), { unit, delay, count: 4, ms: 650 });
        delay += 120;
      }
    }
    if (newHand && next.status === "playing") {
      const centre = rectOf(root.querySelector(".pk-board"));
      let delay = 0;
      for (const player of next.players || []) {
        if (!player.hasCards && !(player.id === next.viewerId && next.you?.cards?.length)) continue;
        flyCards(centre, seatRect(root, player.id), { unit, delay });
        delay += 60;
      }
    }
  }

  return { snapshot, animate };
}
