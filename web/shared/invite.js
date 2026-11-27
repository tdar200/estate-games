// Inviting friends (0.11.1): the message they get, and a WhatsApp link that opens WhatsApp with the
// message ready to send. wa.me works everywhere: the WhatsApp app on a phone, WhatsApp Desktop or
// WhatsApp Web on a PC.

import { GAME_NAME } from "./closest-name.js";

const GAME_NAMES = { estate: "Estate", poker: "poker", ludo: "Ludo", quiz: "a quiz", closest: GAME_NAME };

// The words that go with the link, e.g. "Come and play Ludo with me! Table ABCDE".
export function inviteLine(gameType, code) {
  const game = GAME_NAMES[gameType] || GAME_NAMES.estate;
  return `Come and play ${game} with me!${code ? ` Table ${code}` : ""}`;
}

// The whole message, link included (WhatsApp and the clipboard take one block of text).
export function inviteText(gameType, code, url) {
  return `${inviteLine(gameType, code)}: ${url}`;
}

// https://wa.me/?text=… opens WhatsApp with the message filled in; the sender picks the chat.
export function whatsappUrl(text) {
  return `https://wa.me/?text=${encodeURIComponent(text)}`;
}

// Opens WhatsApp in a new tab or the app (a click must have started this). Falls back to this tab
// when a pop-up blocker stops the new one. Returns the URL it opened.
export function openWhatsApp(text, win = globalThis.window) {
  const url = whatsappUrl(text);
  const opened = win?.open?.(url, "_blank");
  if (!opened && win?.location) win.location.href = url;
  return url;
}
