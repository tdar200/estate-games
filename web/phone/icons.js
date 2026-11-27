// Small inline SVG icons for phone cues and badges. Drawn on a 16 × 16 grid with currentColor so
// they follow the text colour and render the same on every phone (no emoji fonts involved).

const PATHS = {
  dice: '<rect x="2" y="2" width="12" height="12" rx="2.5" fill="none" stroke="currentColor" stroke-width="1.6"/><circle cx="5.5" cy="5.5" r="1.2"/><circle cx="10.5" cy="10.5" r="1.2"/><circle cx="8" cy="8" r="1.2"/>',
  go: '<path d="M2 8h9M8 4.5 11.5 8 8 11.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M14 3v10" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>',
  bank: '<path d="M8 1.8 14 5H2z"/><path d="M3 6.5h2v5H3zM7 6.5h2v5H7zM11 6.5h2v5h-2z"/><path d="M2 12.5h12V14H2z"/>',
  swap: '<path d="M2.5 5.5h10M10 3l2.5 2.5L10 8M13.5 10.5h-10M6 8l-2.5 2.5L6 13" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>',
  coin: '<circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M8 4.5v7M10 6c-.4-.7-1.2-1-2-1-1.1 0-2 .6-2 1.5S7 8 8 8s2 .5 2 1.5S9.1 11 8 11c-.9 0-1.6-.3-2-1" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>',
  house: '<path d="M8 2 14 7.2V14H2V7.2z"/>',
  hammer: '<path d="M2.5 13.5 9 7" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/><path d="M7.2 3.6 10 1l5 5-2.6 2.6z"/>',
  card: '<rect x="3" y="1.8" width="10" height="12.4" rx="1.6" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M6.4 6.2c0-1 .7-1.6 1.6-1.6s1.6.6 1.6 1.4c0 1.2-1.6 1.2-1.6 2.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/><circle cx="8" cy="11" r=".9"/>',
  lock: '<rect x="3" y="7" width="10" height="7.5" rx="1.5"/><path d="M5.2 7V5a2.8 2.8 0 0 1 5.6 0v2" fill="none" stroke="currentColor" stroke-width="1.7"/>',
  unlock: '<rect x="3" y="7" width="10" height="7.5" rx="1.5"/><path d="M5.2 7V5a2.8 2.8 0 0 1 5.5-.7" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/>',
  deal: '<path d="M1.5 7.5 4.5 4.5l3 1.5 2-1.5 5 3.5-3 3.5z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><path d="M6 9.5l1.6 1.6M7.8 8.2l1.8 1.8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>',
  check: '<path d="M2.5 8.5 6.2 12 13.5 4" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>',
  cross: '<path d="M3.5 3.5l9 9M12.5 3.5l-9 9" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/>',
  warn: '<path d="M8 1.5 15 14H1z"/><path d="M8 6v3.8" stroke="#15191E" stroke-width="1.7" stroke-linecap="round"/><circle cx="8" cy="11.8" r="1" fill="#15191E"/>',
  trophy: '<path d="M4 2h8v4a4 4 0 0 1-8 0z"/><path d="M4 3.5H2a2 2 0 0 0 2 3M12 3.5h2a2 2 0 0 1-2 3" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M7 10h2v2.5h2.5V14h-7v-1.5H7z"/>',
  out: '<circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M4 12 12 4" stroke="currentColor" stroke-width="1.6"/>',
  park: '<rect x="2" y="2" width="12" height="12" rx="2.5" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M6 12V4.5h2.6a2.2 2.2 0 0 1 0 4.4H6" fill="none" stroke="currentColor" stroke-width="1.7"/>',
  loan: '<path d="M2 11h3l3 2 6-4-1.4-1.4L9 9.5H7" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><circle cx="10" cy="4.5" r="2.6" fill="none" stroke="currentColor" stroke-width="1.4"/>',
  wrench: '<path d="M10.5 1.8a3.6 3.6 0 0 0-3.3 4.9L2 11.9 4.1 14l5.2-5.2a3.6 3.6 0 0 0 4.9-3.3l-2.1 2.1-2-.5-.5-2z"/>',
  bolt: '<path d="M9.5 1 3.5 9H8l-1.5 6L12.5 7H8z"/>',
  rail: '<rect x="3.5" y="1.5" width="9" height="9" rx="2"/><rect x="5" y="3" width="6" height="3" rx=".6" fill="#DCE6EE"/><path d="M4.5 14.5 6 11M11.5 14.5 10 11" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>',
  pin: '<path d="M8 15s5-4.6 5-8.5a5 5 0 0 0-10 0C3 10.4 8 15 8 15z"/><circle cx="8" cy="6.5" r="1.9" fill="#15191E"/>',
  addhome: '<rect x="4" y="1.2" width="8" height="13.6" rx="1.7" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M8 5.2v5.2M5.4 7.8h5.2" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>',
  screen: '<rect x="1.5" y="2.5" width="13" height="8.5" rx="1.3" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M5.5 14h5M8 11v3" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/>',
  sound: '<path d="M2 6h3l4-3.5v11L5 10H2z"/><path d="M11 5.5a3.5 3.5 0 0 1 0 5M12.8 3.5a6 6 0 0 1 0 9" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>',
  gear: '<path d="M8 5.2a2.8 2.8 0 1 0 0 5.6 2.8 2.8 0 0 0 0-5.6z" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M8 1v2M8 13v2M1 8h2M13 8h2M3 3l1.4 1.4M11.6 11.6 13 13M3 13l1.4-1.4M11.6 4.4 13 3" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/>',
  bot: '<path d="M8 1.2v2.3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="1.6" r="1.1"/><rect x="2.5" y="4" width="11" height="9" rx="2.4"/><circle cx="5.9" cy="8.2" r="1.3" fill="#15191E"/><circle cx="10.1" cy="8.2" r="1.3" fill="#15191E"/><path d="M1 7.5v3M15 7.5v3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>',
  clock: '<circle cx="8" cy="8" r="6.2" fill="none" stroke="currentColor" stroke-width="1.7"/><path d="M8 4.3V8l2.6 1.8" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>',
  person: '<circle cx="8" cy="5" r="3" fill="none" stroke="currentColor" stroke-width="1.7"/><path d="M2.5 14.5a5.5 5.5 0 0 1 11 0" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/>',
  crown: '<path d="M2 5.2 5 8l3-4.5L11 8l3-2.8-1.3 7.3H3.3z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"/>',
};

export function icon(name, { size = 16, label = "" } = {}) {
  const body = PATHS[name] || PATHS.coin;
  const a11y = label ? `role="img" aria-label="${label.replaceAll('"', "&quot;")}"` : 'aria-hidden="true" focusable="false"';
  return `<svg class="ic ic--${name}" width="${size}" height="${size}" viewBox="0 0 16 16" fill="currentColor" ${a11y}>${body}</svg>`;
}

export const ICON_NAMES = Object.keys(PATHS);
