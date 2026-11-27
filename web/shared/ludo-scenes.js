// Ludo demo states for the screens (?demo=ludo-<name> on the table screen and the phone): the real
// server states in web/fixtures/ludo (written by internal/server/ludo_fixtures_test.go).

export async function fetchLudoFixture(name) {
  const safe = String(name || "").replace(/[^a-z0-9-]/gi, "");
  const response = await fetch(`/fixtures/ludo/${safe}.json`, { cache: "no-store" });
  if (!response.ok) throw new Error(`Ludo demo "${safe}" not found`);
  return response.json();
}

// A demo state: the fixture named, or (for a name without -table / -phone) its table version.
export async function buildLudoScene(name, load = fetchLudoFixture) {
  try {
    const fixture = await load(name);
    return { state: fixture.state, title: fixture.meta?.description || name };
  } catch (error) {
    if (/-(table|phone)$/.test(name)) throw error;
    const fixture = await load(`${name}-table`);
    return { state: fixture.state, title: fixture.meta?.description || name };
  }
}
