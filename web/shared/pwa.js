// Installable app (PWA). startApp registers the service worker (/sw.js, which only adds an offline
// screen) and keeps the browser's install offer, so the phone can show its own ADD TO HOME SCREEN
// button instead of the browser's pop-up covering the action buttons mid-game.
//
// Browsers only install over HTTPS (or localhost), and Chrome only offers an install after the
// person has tapped the page and spent about 30 seconds on it. Until then (and on browsers that
// never offer one, like Safari) installSteps() says where the browser's own menu item is.

let offer = null; // the deferred beforeinstallprompt event (Chrome, Edge, Samsung Internet)

// What this device can do:
//   "installed" - already running as the installed app (nothing to offer);
//   "prompt"    - the browser offered an install: one tap opens its dialog;
//   "ios"       - iPhone or iPad: Share > Add to Home Screen;
//   "android" | "samsung" | "firefox" - an Android browser's menu item;
//   "desktop"   - Chrome or Edge on a computer: the install icon in the address bar;
//   "insecure"  - plain http (the home Wi-Fi address): open the HTTPS link to install;
//   null        - a browser that can't install apps (Firefox or Safari on a computer, ...).
export function installState(win = globalThis) {
  const nav = win?.navigator || {};
  const mode = (query) => Boolean(win?.matchMedia?.(query)?.matches);
  if (mode("(display-mode: standalone)") || mode("(display-mode: fullscreen)") || nav.standalone === true) return "installed";
  if (!nav.userAgent) return null;
  if (!win.isSecureContext) return "insecure";
  if (offer) return "prompt";
  const ua = String(nav.userAgent);
  if (/iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && Number(nav.maxTouchPoints) > 1)) return "ios";
  if (/Android/.test(ua)) {
    if (/SamsungBrowser/.test(ua)) return "samsung";
    if (/Firefox/.test(ua)) return "firefox";
    return "android";
  }
  if (/Edg\/|Chrome\//.test(ua) && !/Mobile|OPR\//.test(ua)) return "desktop";
  return null;
}

// What to tell the person for each state (plain text). publicUrl: the server's HTTPS address.
export function installSteps(state, { publicUrl = "" } = {}) {
  const fromApp = "If you opened the link from WhatsApp or another app, open it in your browser first.";
  switch (state) {
    case "prompt":
      return "Puts Estate on your home screen. It opens full screen, like an app.";
    case "ios":
      return `Tap the Share button (the square with an arrow), then Add to Home Screen. ${fromApp.replace("your browser", "Safari")}`;
    case "android":
      return `Tap the ⋮ menu at the top right, then Add to Home screen (or Install app). ${fromApp.replace("your browser", "Chrome")}`;
    case "samsung":
      return "Tap the ☰ menu at the bottom, then Add page to, then Home screen.";
    case "firefox":
      return "Tap the ⋮ menu, then Install (or Add to Home screen).";
    case "desktop":
      return "Click the install icon at the right of the address bar, or open the browser menu and choose Install Estate.";
    case "insecure": {
      const link = String(publicUrl || "").replace(/\/+$/, "");
      return link ? `Installing needs the secure link. On this device, open ${link}/play and add it from there.` : "Installing needs the secure (https) link to Estate.";
    }
    default:
      return "";
  }
}

// Whether there is anything to offer (show the HOME SCREEN row and button).
export function canOfferInstall(state) {
  return Boolean(state) && state !== "installed";
}

// onChange runs when the install offer appears or goes (re-render whatever shows it).
export function startApp({ win = globalThis, onChange = () => {} } = {}) {
  if (!win?.addEventListener) return;
  win.addEventListener("beforeinstallprompt", (event) => {
    event.preventDefault();
    offer = event;
    onChange();
  });
  win.addEventListener("appinstalled", () => {
    offer = null;
    onChange();
  });
  const workers = win.navigator?.serviceWorker;
  if (!workers || !win.isSecureContext) return;
  const register = () => workers.register("/sw.js", { scope: "/" }).catch(() => {});
  if (win.document?.readyState === "complete") register();
  else win.addEventListener("load", register, { once: true });
}

// Shows the browser's install dialog; true when the person accepted.
export async function promptInstall() {
  if (!offer) return false;
  const event = offer;
  offer = null;
  try {
    await event.prompt();
    const choice = await event.userChoice;
    return choice?.outcome === "accepted";
  } catch {
    return false;
  }
}
