// Seat and table-screen approval (docs/API.md "Getting back in"; review S1/S2). A device that is
// not the one a seat or table screen belongs to must be let in: the host's phone and the table
// show a prompt with a 4-digit code (state.claims), and the asking device waits, or types that
// code. Self-contained so the apps need only two small hooks:
//   showClaimPrompts(state, {client, code, token})  - on every state: approve/deny prompts
//   awaitApproval(client, code, pending, retry)      - on a 202 {pending, claim}: wait for a yes
// One glance: whose seat, the code, LET IN / NO. Styles: .claim in tokens.css (both apps load it).

import { playerChipHtml } from "./tokens.js";

const ROOT_ID = "estate-claims";
const WAIT_ID = "estate-claim-wait";
const POLL_MS = 2000;

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

const LOCK = '<svg class="claim__icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><rect x="5" y="10" width="14" height="11" rx="2" fill="currentColor"/><path d="M8 10V7a4 4 0 0 1 8 0v3" fill="none" stroke="currentColor" stroke-width="2.4"/></svg>';

// What is being asked for, in a few words: "MAYA'S SEAT" / "THE TABLE SCREEN".
export function claimSubject(claim) {
  if (claim?.kind === "table") return "THE TABLE SCREEN";
  const who = String(claim?.playerName || "A player").toUpperCase();
  return `${who}${who.endsWith("S") ? "'" : "'S"} SEAT`;
}

function claimText(claim) {
  if (claim.kind === "table") return "A new screen wants to be the table for this game";
  return `${claim.playerName || "A player"}'s seat is wanted on a new device${claim.deviceName ? ` (named “${claim.deviceName}”)` : ""}`;
}

export function claimCardHtml(state, claim) {
  const player = claim.playerId ? (state?.players || []).find((candidate) => candidate.id === claim.playerId) : null;
  const where = claim.kind === "table" ? "on a new screen" : `on a new device${claim.deviceName ? ` · “${esc(claim.deviceName)}”` : ""}`;
  const digits = String(claim.code || "").split("").map((digit) => `<span>${esc(digit)}</span>`).join("");
  return `<div class="claim on-light" data-claim-card role="alertdialog" aria-label="${esc(claimText(claim))}. Code ${esc(claim.code)}">
    <div class="claim__head">${LOCK}LET THEM IN?</div>
    <div class="claim__body">
      <div class="claim__who">${player ? playerChipHtml(state, player, { size: 34, glyph: true }) : ""}<span><b>${esc(claimSubject(claim))}</b><small>${where}</small></span></div>
      <div class="claim__code" aria-hidden="true">${digits}</div>
      <p class="claim__note">Only if you know who it is. They can type this code instead.</p>
      <div class="claim__actions">
        <button type="button" class="claim__btn claim__btn--yes" data-claim="${esc(claim.id)}" data-decision="approve">LET IN</button>
        <button type="button" class="claim__btn" data-claim="${esc(claim.id)}" data-decision="deny">NO</button>
      </div>
      <div class="claim__error" data-claim-error role="alert"></div>
    </div>
  </div>`;
}

// Approver side: render state.claims as prompts. Safe to call on every state.
export function showClaimPrompts(state, { client, code, token } = {}) {
  if (typeof document === "undefined") return;
  const claims = Array.isArray(state?.claims) ? state.claims : [];
  let root = document.getElementById(ROOT_ID);
  if (!claims.length || !client || !code || !token) {
    root?.remove();
    return;
  }
  if (!root) {
    root = document.createElement("div");
    root.id = ROOT_ID;
    root.className = "claim-stack";
    root.setAttribute("role", "region");
    root.setAttribute("aria-label", "Requests to join");
    document.body.append(root);
    root.addEventListener("click", async (event) => {
      const button = event.target.closest("button[data-claim]");
      if (!button) return;
      const card = button.closest("[data-claim-card]");
      card?.querySelectorAll("button").forEach((b) => { b.disabled = true; });
      try {
        await client.answerClaim(root.dataset.code, root.dataset.token, button.dataset.claim, button.dataset.decision);
        card?.remove();
      } catch (error) {
        const note = card?.querySelector("[data-claim-error]");
        if (note) note.textContent = error.message || "Couldn't answer";
        card?.querySelectorAll("button").forEach((b) => { b.disabled = false; });
      }
    });
  }
  root.dataset.code = code;
  root.dataset.token = token;
  // Rebuilt only when the requests change, so a card never blinks on an unrelated update.
  const key = claims.map((c) => `${c.id}:${c.code}`).join(",");
  if (root.dataset.key === key) return;
  root.dataset.key = key;
  root.innerHTML = claims.map((claim) => claimCardHtml(state, claim)).join("");
}

// Asking side: given a 202 {pending, claim} answer, wait for a yes (polling the request) or a
// code typed here, then call retry() / retry(code) for the real entry. Resolves with that entry
// result, or null if the person cancels; rejects with the API error when the answer is no.
export function awaitApproval(client, code, pending, retry) {
  if (typeof document === "undefined" || !pending?.claim?.id) return Promise.resolve(null);
  document.getElementById(WAIT_ID)?.remove();
  const claimId = pending.claim.id;
  const table = pending.claim.kind === "table";
  const box = document.createElement("div");
  box.id = WAIT_ID;
  box.className = "claim-backdrop";
  box.innerHTML = `<form class="claim claim--wait on-light" role="dialog" aria-modal="true" aria-labelledby="${WAIT_ID}-title">
    <div class="claim__head" id="${WAIT_ID}-title">${LOCK}WAITING FOR A YES<span class="claim__dots" aria-hidden="true"><i></i><i></i><i></i></span></div>
    <div class="claim__body">
      <p class="claim__lead">${table ? "The host's phone must let this screen in." : "The host's phone or the table must let you in."}</p>
      <label class="claim__label" for="${WAIT_ID}-code">OR TYPE THE CODE THEY SEE</label>
      <div class="claim__row">
        <input id="${WAIT_ID}-code" class="claim__input" inputmode="numeric" autocomplete="one-time-code" maxlength="4" pattern="[0-9]*" placeholder="····" aria-describedby="${WAIT_ID}-note">
        <button type="submit" class="claim__btn claim__btn--yes">USE CODE</button>
      </div>
      <div class="claim__error" id="${WAIT_ID}-note" data-wait-note role="status"></div>
      <div class="claim__actions claim__actions--end"><button type="button" class="claim__btn" data-wait-cancel>CANCEL</button></div>
    </div>
  </form>`;
  document.body.append(box);
  const form = box.querySelector("form");
  const input = box.querySelector("input");
  const note = box.querySelector("[data-wait-note]");
  input.focus();
  return new Promise((resolve, reject) => {
    let done = false;
    let timer = null;
    const finish = (fn, value) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      box.remove();
      fn(value);
    };
    const enter = async (approvalCode) => {
      try {
        const result = await retry(approvalCode);
        if (result?.pending) {
          note.textContent = "Still waiting…";
          return;
        }
        finish(resolve, result);
      } catch (error) {
        if (error.status === 403 && /code/i.test(error.message || "")) {
          note.textContent = error.message;
          input.select();
          return;
        }
        finish(reject, error);
      }
    };
    const poll = async () => {
      if (done) return;
      try {
        const claim = await client.claimStatus(code, claimId);
        if (claim?.status === "approved") return enter();
        if (claim?.status === "denied") return enter(); // the server answers with the refusal
      } catch (error) {
        // Expired or gone: report it as 410 so callers don't mistake it for an old server's 404.
        if (error.status === 404) return finish(reject, Object.assign(new Error(error.message || "That request has expired. Ask again."), { status: 410 }));
      }
      timer = setTimeout(poll, POLL_MS);
    };
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const value = input.value.replace(/\D/g, "");
      if (value.length === 4) enter(value);
      else note.textContent = "The code has 4 digits.";
    });
    box.querySelector("[data-wait-cancel]").addEventListener("click", () => finish(resolve, null));
    timer = setTimeout(poll, POLL_MS);
  });
}
