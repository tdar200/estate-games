// The installed app's offline screen (offline.html): reloads the page the person was opening as
// soon as the server answers, on TRY AGAIN, and when the phone comes back online.
(() => {
  const status = document.getElementById("status");
  let busy = false;
  async function attempt(manual) {
    if (busy) return;
    busy = true;
    if (manual && status) status.textContent = "Trying…";
    try {
      const res = await fetch("/api/server", { cache: "no-store" });
      if (res.ok) {
        location.reload();
        return;
      }
    } catch {
      // still offline
    }
    busy = false;
    if (manual && status) status.textContent = "Still can't reach it. Trying again every few seconds.";
  }
  document.getElementById("retry")?.addEventListener("click", () => attempt(true));
  addEventListener("online", () => attempt(false));
  setInterval(() => {
    if (!document.hidden) attempt(false);
  }, 5000);
})();
