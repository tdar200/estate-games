// API client for docs/API.md: REST calls, a device identity in localStorage, and live room updates
// over WebSocket with automatic fallback to polling GET …/state every 500 ms (so the apps also work
// against the old Node server, which only has the §1 endpoints).

import { PING_SLOW_MS, createClockSync, pingInterval, pingMessage } from "./quiz-clock.js";
import { hostRequest } from "./standin.js";

const DEVICE_KEY = "estate.device";
// How long a socket action waits for its actionResult before it counts as unanswered.
const SOCKET_ACTION_MS = 6_000;

export class ApiError extends Error {
  constructor(message, status = 0) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

function storageGet(key) {
  try {
    return JSON.parse(globalThis.localStorage?.getItem(key) || "null");
  } catch {
    return null;
  }
}

function storageSet(key, value) {
  try {
    if (value === null || value === undefined) globalThis.localStorage?.removeItem(key);
    else globalThis.localStorage?.setItem(key, JSON.stringify(value));
  } catch {
    // Private mode or storage disabled: the session simply won't survive a reload.
  }
}

export const storage = { get: storageGet, set: storageSet };

export function normaliseCode(code) {
  return String(code || "").trim().toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 5);
}

export class EstateClient {
  constructor({ base = "" } = {}) {
    this.base = base;
    this.device = storageGet(DEVICE_KEY);
    this.serverInfo = undefined;
  }

  deviceHeader() {
    return this.device?.deviceId && this.device?.deviceSecret ? `${this.device.deviceId}:${this.device.deviceSecret}` : null;
  }

  async request(method, path, body = undefined) {
    const headers = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const device = this.deviceHeader();
    if (device) headers["X-Estate-Device"] = device;
    let response;
    try {
      response = await fetch(`${this.base}${path}`, {
        method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
        cache: "no-store",
      });
    } catch {
      throw new ApiError("Can't reach the table. Check the Wi-Fi and try again.", 0);
    }
    const text = await response.text();
    let payload = null;
    try {
      payload = text ? JSON.parse(text) : null;
    } catch {
      payload = null;
    }
    if (!response.ok) {
      const message = payload?.error || (response.status === 404 ? "Not available on this server" : `Request failed (${response.status})`);
      throw new ApiError(message, response.status);
    }
    return payload;
  }

  // GET /api/server → {lanURL, publicURL, version}; null on the old Node server.
  async server() {
    if (this.serverInfo !== undefined) return this.serverInfo;
    try {
      this.serverInfo = await this.request("GET", "/api/server");
    } catch {
      this.serverInfo = null;
    }
    return this.serverInfo;
  }

  async partyMode() {
    return Boolean(await this.server());
  }

  // ---- Devices -------------------------------------------------------------------------------
  async ensureDevice(profile = {}) {
    if (this.device?.deviceId) return this.device;
    if (!(await this.partyMode())) return null;
    try {
      const result = await this.request("POST", "/api/device", profile);
      if (result?.deviceId) {
        this.device = { deviceId: result.deviceId, deviceSecret: result.deviceSecret };
        storageSet(DEVICE_KEY, this.device);
      }
    } catch {
      return null;
    }
    return this.device;
  }

  async updateDevice(profile) {
    if (!this.deviceHeader()) return null;
    try {
      return await this.request("PATCH", "/api/device", profile);
    } catch {
      return null;
    }
  }

  // ---- §1 compatibility endpoints -------------------------------------------------------------
  createRoom(name) {
    return this.request("POST", "/api/rooms", { name });
  }

  join(code, { name, tokenKind } = {}) {
    const body = { name };
    if (tokenKind) body.tokenKind = tokenKind;
    if (this.device?.deviceId) body.deviceId = this.device.deviceId;
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/join`, body);
  }

  watch(code, name) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/watch`, { name });
  }

  state(code, token) {
    return this.request("GET", `/api/rooms/${encodeURIComponent(code)}/state?token=${encodeURIComponent(token)}`);
  }

  setMode(code, token, role) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(code)}/mode`, { token, role });
  }

  act(code, token, action, seatId = null) {
    const body = { token, action };
    if (seatId) body.seatId = seatId;
    return this.request("POST", `/api/rooms/${encodeURIComponent(code)}/action`, body);
  }

  // The Node server reads offerProperties/requestProperties (see public/app.js).
  tradeQuote(code, token, { targetId, offerProperties = [], requestProperties = [] }, seatId = null) {
    const body = { token, targetId, offerProperties, requestProperties };
    if (seatId) body.seatId = seatId;
    return this.request("POST", `/api/rooms/${encodeURIComponent(code)}/trade-quote`, body);
  }

  // Mark one of your deeds as not for sale to computers (on = false: for sale again) → state.
  notSelling(code, token, spaceIndex, on = true, seatId = null) {
    const body = { token, spaceIndex, on };
    if (seatId) body.seatId = seatId;
    return this.request("POST", `/api/rooms/${encodeURIComponent(code)}/not-selling`, body);
  }

  addAi(code, token, difficulty = null) {
    const body = { token };
    if (difficulty) body.difficulty = difficulty;
    return this.request("POST", `/api/rooms/${encodeURIComponent(code)}/ai`, body);
  }

  removePlayer(code, token, playerId) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(code)}/remove`, { token, playerId });
  }

  // The host's Players controls mid-game (docs/API.md §3c): kind "standin" (Computer plays), "skip"
  // (Skip turn) or "remove". The table screen sends its tableToken, the host's phone its token.
  hostControl(code, token, kind, playerId) {
    const request = hostRequest(kind, code, token, playerId);
    return this.request(request.method, request.path, request.body);
  }

  // ---- §2 party mode --------------------------------------------------------------------------
  // The table screen gets a device identity too, so the same browser can reopen its table
  // without anyone's approval (docs/API.md "Getting back in").
  // gameType: "estate" (default), "poker", "ludo", "quiz" or "closest" (docs/API.md "Game types").
  // replaces: the session of the table this screen is leaving; the server closes it if it is an
  // empty lobby, so switching games leaves nothing behind.
  async createTable(gameType = "estate", replaces = null) {
    await this.ensureDevice({});
    const body = { gameType };
    if (replaces?.code && replaces?.token) {
      body.replaceCode = replaces.code;
      body.replaceToken = replaces.token;
    }
    return this.request("POST", "/api/tables", body);
  }

  addSeat(code, tableToken, { input, name, tokenKind, pad = null }) {
    const body = { tableToken, input, name, tokenKind };
    if (pad !== null && pad !== undefined) body.pad = pad;
    return this.request("POST", `/api/rooms/${encodeURIComponent(code)}/seats`, body);
  }

  rejoin(code) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/rejoin`, {});
  }

  // Start a game from this phone, as its host (no PC needed) → {code, token, playerId, state}.
  // gameType: "estate" (default), "poker", "ludo", "quiz" or "closest"; their states have state.game === that type.
  createGame(name, tokenKind = null, gameType = "estate") {
    const body = { name, gameType };
    if (tokenKind) body.tokenKind = tokenKind;
    return this.request("POST", "/api/games", body);
  }

  // Every game on this server worth showing → {games: [summary + mine, playerId?, online, updatedAt]}.
  // Each summary has gameType ("estate" | "poker" | "ludo" | "quiz" | "closest"); for poker, turn is the hand number and
  // rules are the poker settings.
  listGames() {
    return this.request("GET", "/api/games");
  }

  // Same code, same seats, fresh game (host player or table, after the game ends).
  rematch(code, token) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/rematch`, { token });
  }

  // End the game for everyone now; the richest player wins (host player or table).
  endGame(code, token) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/end`, { token });
  }

  // Room-wide settings (host player or table): { computerSpeed: "slow" | "normal" | "fast",
  // listed: true | false } (listed shows the game to everyone in /api/games).
  roomSettings(code, token, settings) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/settings`, { token, ...settings });
  }

  // Take over an offline seat from a new browser/phone (docs/API.md "Getting back in") →
  // {code, token, playerId, state}, or (HTTP 202) {pending: true, claim} until the host's phone
  // or the table lets this device in; pass the 4-digit code they show as approvalCode.
  reclaim(code, playerId, approvalCode = "") {
    const body = { playerId };
    if (approvalCode) body.approvalCode = String(approvalCode);
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/reclaim`, body);
  }

  // Open an existing table's PC screen in this browser → {code, tableToken, state}, or (202)
  // {pending: true, claim} until the host lets this screen in (same approvalCode as reclaim).
  async resumeTable(code, approvalCode = "") {
    await this.ensureDevice({});
    const body = {};
    if (approvalCode) body.approvalCode = String(approvalCode);
    return this.request("POST", `/api/tables/${encodeURIComponent(normaliseCode(code))}/resume`, body);
  }

  // A pending seat/table request of this device → {id, kind, playerId, status, expiresAt};
  // status is "pending", "approved" (call reclaim/resumeTable again) or "denied".
  claimStatus(code, claimId) {
    return this.request("GET", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/claims/${encodeURIComponent(claimId)}`);
  }

  // Host player or table: answer a request in state.claims → state.
  answerClaim(code, token, claimId, decision) {
    return this.request("POST", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/claims/${encodeURIComponent(claimId)}`, { token, decision });
  }

  summary(code) {
    return this.request("GET", `/api/rooms/${encodeURIComponent(normaliseCode(code))}/summary`);
  }

  friends() {
    return this.request("GET", "/api/friends");
  }

  invite(toDeviceId, code) {
    return this.request("POST", "/api/invites", { toDeviceId, code });
  }

  qrUrl(code) {
    return `${this.base}/api/rooms/${encodeURIComponent(code)}/qr.svg`;
  }
}

// Live updates for one room session. Tries the WebSocket first (party server only); on failure or
// on the old server it polls GET …/state every `pollMs`. `onEvent` receives WS animation events;
// they always arrive just before the state they belong to.
// How long the connection may be gone before the screens say so and stop taking taps, and the
// shorter wait after an action failed to reach the server.
export const OFFLINE_GRACE_MS = 4_000;

// The server can't be reached: no answer at all, or (over the public link) the HTTPS proxy in
// front of it answering 502/503/504 because the game server behind it is down or restarting.
export function unreachable(error) {
  const status = Number(error?.status);
  return status === 0 || status === 502 || status === 503 || status === 504;
}
export const ACTION_GRACE_MS = 1_500;

export class LiveRoom {
  constructor(client, { code, token, onState, onEvent = () => {}, onStatus = () => {}, onFatal = () => {}, pollMs = 500 }) {
    this.client = client;
    this.code = code;
    this.token = token;
    this.onState = onState;
    this.onEvent = onEvent;
    this.onStatus = onStatus;
    this.onFatal = onFatal;
    this.pollMs = pollMs;
    this.version = null;
    this.socket = null;
    this.pollTimer = null;
    this.retryTimer = null;
    this.pingTimer = null;
    this.retryDelay = 4000;
    this.stopped = false;
    this.busy = false;
    this.status = "connecting";
    this.watchTimer = null;
    this.offlineTimer = null;
    // Clock sync (docs/QUIZ-API.md §1.1): every ping carries t, every pong the server's now; the
    // quiz shows its choices on this synced clock. Pings run every 2 s while a quiz plays.
    this.clock = createClockSync();
    this.pingMs = PING_SLOW_MS;
    // Socket actions waiting for their actionResult: id → {resolve, reject, timer}.
    this.actionId = 0;
    this.pending = new Map();
    this.onVisible = () => {
      if (!globalThis.document?.hidden) this.wake();
    };
  }

  async start() {
    this.stopped = false;
    globalThis.document?.addEventListener?.("visibilitychange", this.onVisible);
    const party = await this.client.partyMode();
    if (party && "WebSocket" in globalThis) this.openSocket();
    else this.startPolling();
  }

  stop() {
    this.stopped = true;
    globalThis.document?.removeEventListener?.("visibilitychange", this.onVisible);
    clearInterval(this.pollTimer);
    clearTimeout(this.retryTimer);
    clearInterval(this.pingTimer);
    clearInterval(this.watchTimer);
    clearTimeout(this.offlineTimer);
    this.pollTimer = null;
    this.failPending("The connection closed.");
    if (this.socket) {
      this.socket.onclose = null;
      this.socket.close();
      this.socket = null;
    }
  }

  setStatus(status) {
    if (this.status !== status) {
      this.status = status;
      this.onStatus(status);
    }
  }

  // Feed a state that came back from an action (or anywhere else).
  accept(state) {
    if (!state) return;
    if (this.version !== null && typeof state.version === "number" && state.version === this.version) return;
    this.version = state.version ?? null;
    this.clock.seen(Number(state.now));
    this.setPingRate(pingInterval(state));
    this.onState(state);
  }

  // Ping (with the clock-sync fields) on the open socket. False when there is none.
  ping() {
    const socket = this.socket;
    if (!socket || socket.readyState !== WebSocket.OPEN) return false;
    try {
      socket.send(JSON.stringify(pingMessage(this.clock, Date.now())));
      return true;
    } catch {
      return false;
    }
  }

  // 2 s while a quiz room plays, 15 s otherwise. A change pings at once and restarts the timer.
  setPingRate(ms) {
    if (ms === this.pingMs) return;
    this.pingMs = ms;
    if (this.socket?.readyState === WebSocket.OPEN) this.startPings();
  }

  startPings() {
    clearInterval(this.pingTimer);
    this.ping();
    this.pingTimer = setInterval(() => this.ping(), this.pingMs);
  }

  // Send an action over the room socket (docs/QUIZ-API.md §1.1). Returns a promise that resolves on
  // {ok:true} and rejects with an ApiError on {ok:false} (or no answer in 6 s), or false when the
  // socket is not open: the caller then POSTs. The new state still arrives as a state message.
  send(action) {
    const socket = this.socket;
    if (!socket || socket.readyState !== WebSocket.OPEN || this.stopped) return false;
    this.actionId += 1;
    const id = this.actionId;
    try {
      socket.send(JSON.stringify({ type: "action", action, id }));
    } catch {
      return false;
    }
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new ApiError("The table didn't answer.", 0));
      }, SOCKET_ACTION_MS);
      this.pending.set(id, { resolve, reject, timer });
    });
  }

  settle(data) {
    const entry = this.pending.get(data.id);
    if (!entry) return;
    this.pending.delete(data.id);
    clearTimeout(entry.timer);
    if (data.ok) entry.resolve(true);
    else entry.reject(new ApiError(data.error || "That didn't work.", 400));
  }

  failPending(message) {
    for (const entry of this.pending.values()) {
      clearTimeout(entry.timer);
      entry.reject(new ApiError(message, 0));
    }
    this.pending.clear();
  }

  openSocket() {
    if (this.stopped) return;
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${location.host}/ws/rooms/${encodeURIComponent(this.code)}?token=${encodeURIComponent(this.token)}`;
    let opened = false;
    let socket;
    try {
      socket = new WebSocket(url);
    } catch {
      this.startPolling();
      return;
    }
    this.socket = socket;
    socket.onopen = () => {
      opened = true;
      this.retryDelay = 4000;
      this.lastSeen = Date.now();
      this.stopPolling();
      clearTimeout(this.offlineTimer);
      this.setStatus("live");
      // Ping at once (the quiz wants a clock sample before its first clue), then on the timer.
      this.startPings();
      // Heartbeat (review B9): the server answers every ping, so a socket that has said nothing
      // for 45 s (three pings) is dead even if it never closed (Wi-Fi dropped, phone slept).
      // Drop it and catch up by polling until a new socket is open. A phone that wakes up is
      // checked at once (wake), so this is only the backstop.
      clearInterval(this.watchTimer);
      this.watchTimer = setInterval(() => {
        if (this.socket === socket && Date.now() - this.lastSeen > 45_000) this.dropSocket(socket);
      }, 5_000);
    };
    socket.onmessage = (message) => {
      this.lastSeen = Date.now();
      let data;
      try {
        data = JSON.parse(message.data);
      } catch {
        return;
      }
      if (data?.type === "pong") this.clock.pong(data);
      else if (data?.type === "actionResult") this.settle(data);
      else if (data?.type === "event" && data.event) this.onEvent(data.event);
      else if (data?.type === "state" && data.state) this.accept(data.state);
      else if (data?.type === "error" && /session|does not exist|removed you/i.test(data.error || "")) this.onFatal(new ApiError(data.error, 401));
    };
    socket.onclose = () => {
      clearInterval(this.pingTimer);
      clearInterval(this.watchTimer);
      this.socket = null;
      this.failPending("The connection closed.");
      if (this.stopped) return;
      this.startPolling();
      // Try the socket again later; back off so a server without WebSockets isn't hammered.
      this.retryTimer = setTimeout(() => this.openSocket(), opened ? 2000 : this.retryDelay);
      this.retryDelay = Math.min(this.retryDelay * 2, 60_000);
    };
  }

  // A socket that went silent: close it without waiting for TCP, poll, and open a new one.
  dropSocket(socket) {
    if (this.socket !== socket) return;
    clearInterval(this.pingTimer);
    clearInterval(this.watchTimer);
    socket.onclose = null;
    socket.onmessage = null;
    try { socket.close(); } catch { /* already gone */ }
    this.socket = null;
    this.failPending("The connection dropped.");
    if (this.stopped) return;
    this.softOffline();
    this.startPolling();
    clearTimeout(this.retryTimer);
    this.retryTimer = setTimeout(() => this.openSocket(), 1000);
  }

  // A short gap (a phone waking up, a mobile radio switching cells) usually reconnects within a
  // second or two, so only say "offline" if the connection is still not back after a grace period
  // (4 s: at 8 s the buttons stayed live long after the server had gone, review F10).
  softOffline(ms = OFFLINE_GRACE_MS) {
    if (this.offlineTimer) return;
    this.offlineTimer = setTimeout(() => {
      this.offlineTimer = null;
      if (!this.stopped && this.status !== "live") this.setStatus("offline");
    }, ms);
  }

  // An action couldn't reach the server: the person is waiting on it, so check now and say
  // "offline" after a short grace instead of the full one (unless the socket is still live).
  actionFailed() {
    if (this.stopped) return;
    clearTimeout(this.offlineTimer);
    this.offlineTimer = null;
    this.softOffline(ACTION_GRACE_MS);
    this.poll();
  }

  // The page came back to the foreground (the phone woke up or the app was reopened). Don't wait for
  // the heartbeat to notice a socket the phone killed while asleep: catch up at once and test the
  // socket with a ping; if nothing answers within 4 s, open a new one.
  wake() {
    if (this.stopped) return;
    this.poll();
    const socket = this.socket;
    if (!socket) {
      clearTimeout(this.retryTimer);
      this.openSocket();
      return;
    }
    if (socket.readyState !== WebSocket.OPEN) return;
    const sent = Date.now();
    try {
      socket.send(JSON.stringify(pingMessage(this.clock, sent)));
    } catch {
      this.dropSocket(socket);
      return;
    }
    setTimeout(() => {
      if (this.socket === socket && (this.lastSeen ?? 0) < sent) this.dropSocket(socket);
    }, 4_000);
  }

  startPolling() {
    if (this.pollTimer || this.stopped) return;
    this.setStatus("polling");
    this.poll();
    this.pollTimer = setInterval(() => this.poll(), this.pollMs);
  }

  stopPolling() {
    clearInterval(this.pollTimer);
    this.pollTimer = null;
  }

  async poll() {
    if (this.busy || this.stopped || globalThis.document?.hidden) return;
    this.busy = true;
    try {
      const state = await this.client.state(this.code, this.token);
      clearTimeout(this.offlineTimer);
      this.offlineTimer = null;
      if (this.status === "offline") this.setStatus(this.socket ? "live" : "polling");
      this.accept(state);
    } catch (error) {
      if (unreachable(error)) this.softOffline();
      else if (/does not exist|session is invalid|restarted|removed you/i.test(error.message)) {
        this.stop();
        this.onFatal(error);
      }
    } finally {
      this.busy = false;
    }
  }
}

// Presence socket for the phone (invites and friend status). Quietly does nothing on old servers.
export class DeviceSocket {
  constructor(client, { onInvite = () => {}, onFriends = () => {} } = {}) {
    this.client = client;
    this.onInvite = onInvite;
    this.onFriends = onFriends;
    this.socket = null;
    this.delay = 5000;
    this.stopped = false;
  }

  async start() {
    this.stopped = false;
    if (!(await this.client.partyMode()) || !this.client.deviceHeader() || !("WebSocket" in globalThis)) return;
    this.open();
  }

  open() {
    if (this.stopped) return;
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${protocol}//${location.host}/ws/device?device=${encodeURIComponent(this.client.deviceHeader())}`);
    this.socket = socket;
    socket.onmessage = (message) => {
      let data;
      try {
        data = JSON.parse(message.data);
      } catch {
        return;
      }
      if (data?.type === "invite") this.onInvite(data.invite);
      if (data?.type === "friends") this.onFriends(data.friends || []);
    };
    socket.onopen = () => { this.delay = 5000; };
    socket.onclose = () => {
      if (this.stopped) return;
      setTimeout(() => this.open(), this.delay);
      this.delay = Math.min(this.delay * 2, 120_000);
    };
  }

  stop() {
    this.stopped = true;
    if (this.socket) {
      this.socket.onclose = null;
      this.socket.close();
    }
  }
}

// ---- Demo mode ---------------------------------------------------------------------------------
// ?demo=<fixture> renders web/fixtures/<fixture>.json with no server. Actions are logged instead of
// sent. ?demo=replay steps through consecutive real states to exercise the animations.

export async function loadFixture(name) {
  const safe = String(name || "").replace(/[^a-z0-9-]/gi, "");
  const response = await fetch(`/fixtures/${safe}.json`, { cache: "no-store" });
  if (!response.ok) throw new Error(`Demo fixture "${safe}" not found`);
  return response.json();
}

export function expandReplay(fixture) {
  if (!Array.isArray(fixture?.replay)) return null;
  return fixture.replay.map((state) => ({ ...state, board: fixture.board, groups: fixture.groups }));
}

export function demoParam() {
  const value = new URLSearchParams(location.search).get("demo");
  return value ? value.replace(/[^a-z0-9-]/gi, "") : null;
}
