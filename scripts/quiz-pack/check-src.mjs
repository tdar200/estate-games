#!/usr/bin/env node
// Checks that every English Wikipedia `src` link in the quiz packs names a real article (not a
// redirect or a missing page), by asking Wikidata (CC0) which titles have an enwiki sitelink.
//
//   node scripts/quiz-pack/check-src.mjs
//
// Needs network access to query.wikidata.org. Prints the links it could not confirm.

import { readdirSync, readFileSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const PACK_DIR = resolve(HERE, '..', '..', 'internal', 'quiz', 'packs');
const ENDPOINT = 'https://query.wikidata.org/sparql';

const links = new Map(); // title -> [where]
const other = [];
for (const file of readdirSync(PACK_DIR).filter((f) => f.endsWith('.json')).sort()) {
  const pack = JSON.parse(readFileSync(join(PACK_DIR, file), 'utf8'));
  const items = [];
  for (const c of pack.categories || []) for (const q of c.clues || []) items.push([`${c.id} t${q.tier}`, q.src]);
  for (const f of pack.finals || []) items.push([f.id, f.src]);
  for (const [where, src] of items) {
    const m = /^https:\/\/en\.wikipedia\.org\/wiki\/(.+)$/.exec(src || '');
    if (!m) { other.push(`${where}: ${src}`); continue; }
    const title = decodeURIComponent(m[1]).replace(/_/g, ' ').replace(/#.*$/, '');
    if (!links.has(title)) links.set(title, []);
    links.get(title).push(where);
  }
}

const titles = [...links.keys()];
const found = new Set();
const esc = (s) => s.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
for (let i = 0; i < titles.length; i += 25) {
  if (i) await new Promise((r) => setTimeout(r, 1500)); // stay well under the rate limit
  const batch = titles.slice(i, i + 25);
  const query = `SELECT ?name WHERE { VALUES ?name { ${batch.map((t) => `"${esc(t)}"@en`).join(' ')} }
    ?article schema:isPartOf <https://en.wikipedia.org/> ; schema:name ?name . }`;
  const res = await fetch(ENDPOINT, {
    method: 'POST',
    headers: {
      'User-Agent': 'quiz-pack-src-check/1.0 (question-pack linter; node fetch)',
      Accept: 'application/sparql-results+json',
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: `query=${encodeURIComponent(query)}`,
  });
  if (!res.ok) { console.error(`Wikidata answered ${res.status}`); process.exit(2); }
  const data = await res.json();
  for (const b of data.results.bindings) found.add(b.name.value);
}

const missing = titles.filter((t) => !found.has(t));
console.log(`${titles.length} distinct Wikipedia articles linked; ${found.size} confirmed.`);
if (missing.length) {
  console.log('\nNot confirmed (missing page, or a redirect title):');
  for (const t of missing) console.log(`  ${t}   <- ${links.get(t).join(', ')}`);
}
if (other.length) {
  console.log('\nNon-Wikipedia sources (check by hand):');
  for (const o of other) console.log(`  ${o}`);
}
process.exit(missing.length ? 1 : 0);
