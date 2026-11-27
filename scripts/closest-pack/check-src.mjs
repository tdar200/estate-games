#!/usr/bin/env node
// Checks that every English Wikipedia `src` link in the Near Thing packs names a real article
// (not a redirect or a missing page), by asking Wikidata (CC0) which titles have an enwiki
// sitelink, and lists every other source for checking by hand. Adapted from
// scripts/quiz-pack/check-src.mjs (Near Thing questions have a list of sources, not one).
//
//   node scripts/closest-pack/check-src.mjs            # needs network access to query.wikidata.org
//   node scripts/closest-pack/check-src.mjs --offline  # only list the links, no network
//
// Exit code 1 when a Wikipedia link could not be confirmed.

import { readdirSync, readFileSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const PACK_DIR = resolve(HERE, '..', '..', 'internal', 'closest', 'packs');
const ENDPOINT = 'https://query.wikidata.org/sparql';
const OFFLINE = process.argv.includes('--offline');

const links = new Map(); // title -> [question id]
const other = [];        // "id: url"
for (const file of readdirSync(PACK_DIR).filter((f) => f.endsWith('.json')).sort()) {
  const pack = JSON.parse(readFileSync(join(PACK_DIR, file), 'utf8'));
  for (const q of pack.questions || []) {
    for (const src of q.src || []) {
      const m = /^https:\/\/en\.wikipedia\.org\/wiki\/(.+)$/.exec(src || '');
      if (!m) { other.push(`${q.id}${q.defining ? ' (defining)' : ''}: ${src}`); continue; }
      const title = decodeURIComponent(m[1]).replace(/_/g, ' ').replace(/#.*$/, '');
      if (!links.has(title)) links.set(title, []);
      links.get(title).push(q.id);
    }
  }
}

const titles = [...links.keys()];
let missing = [];
if (OFFLINE) {
  console.log(`${titles.length} distinct Wikipedia articles linked (not checked: --offline).`);
  for (const t of titles) console.log(`  ${t}   <- ${links.get(t).join(', ')}`);
} else {
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
        'User-Agent': 'closest-pack-src-check/1.0 (question-pack linter; node fetch)',
        Accept: 'application/sparql-results+json',
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body: `query=${encodeURIComponent(query)}`,
    });
    if (!res.ok) { console.error(`Wikidata answered ${res.status}`); process.exit(2); }
    const data = await res.json();
    for (const b of data.results.bindings) found.add(b.name.value);
  }
  missing = titles.filter((t) => !found.has(t));
  console.log(`${titles.length} distinct Wikipedia articles linked; ${found.size} confirmed.`);
  if (missing.length) {
    console.log('\nNot confirmed (missing page, or a redirect title):');
    for (const t of missing) console.log(`  ${t}   <- ${links.get(t).join(', ')}`);
  }
}
if (other.length) {
  console.log(`\nNon-Wikipedia sources (${other.length}; check by hand):`);
  for (const o of other) console.log(`  ${o}`);
}
process.exit(missing.length ? 1 : 0);
