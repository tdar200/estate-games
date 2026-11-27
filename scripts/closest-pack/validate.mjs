#!/usr/bin/env node
// Checks every Near Thing question pack in internal/closest/packs/*.json against the rules in
// docs/CLOSEST-API.md §7 (the same rules the Go loader in internal/closest/packs.go enforces), plus
// the house rules for the pool, and prints a summary.
//
//   node scripts/closest-pack/validate.mjs            # check all packs
//   node scripts/closest-pack/validate.mjs --list     # also print every question, by pack
//   node scripts/closest-pack/validate.mjs --strict   # treat warnings as errors
//
// Exit code 1 when any error (or, with --strict, any warning) is found.
//
// Beyond §7 it checks: ids unique across packs; no near-duplicate questions; no answer equal to a
// number answer in the board quiz packs (internal/quiz/packs); the year share per pack (error above
// a third, warning above a quarter); topic counts; sources (two different sites unless defining,
// Wikipedia at most once); the unit is named in the question; the answer is not given away in it.

import { readdirSync, readFileSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(HERE, '..', '..');
// CLOSEST_PACK_DIR points the check at another folder (used to test the checker itself).
const PACK_DIR = process.env.CLOSEST_PACK_DIR ? resolve(process.env.CLOSEST_PACK_DIR) : resolve(ROOT, 'internal', 'closest', 'packs');
const BOARD_DIR = resolve(ROOT, 'internal', 'quiz', 'packs');

const TOPICS = ['geography', 'science', 'space', 'nature', 'history', 'people', 'sport', 'music',
  'screen', 'books', 'art', 'food', 'words', 'numbers', 'inventions'];
const LICENSES = ['own', 'CC0', 'CC BY-SA 4.0'];
const NO_ATTRIBUTION_NEEDED = ['own', 'CC0'];

// §7 limits (match internal/closest/packs.go).
const MAX_TEXT = 120;
const MAX_FACT = 140;
const MAX_UNIT = 16;
const MIN_ANSWER = 10;
const MAX_ANSWER = 999_999;
const MAX_GUESS = 9_999_999;
const MIN_YEAR = 1000;
const MAX_YEAR = 2025;
const SPREAD = { year: [1, 300], plain: [5, 300] };

// Pool targets (house rules, not engine rules).
const MIN_PER_TOPIC = 10;      // across all packs
const MAX_PER_TOPIC = 13;
const MIN_PER_TOPIC_PACK = 4;  // so each pack alone can still deal a Full game
const WIDE = 20;               // plain: warn when max > answer x 20 or min < answer / 20
const NEAR_DUP = 0.6;          // Jaccard similarity of content words that counts as a near-duplicate
const NEAR_DUP_SAME_ANSWER = 0.4;

const args = new Set(process.argv.slice(2));
const LIST = args.has('--list');
const STRICT = args.has('--strict');

const errors = [];
const warnings = [];
const notes = [];
const err = (where, msg) => errors.push(`${where}: ${msg}`);
const warn = (where, msg) => warnings.push(`${where}: ${msg}`);

const isStr = (s) => typeof s === 'string';
const len = (s) => [...s].length; // characters, not UTF-16 units
const fmtNum = (n) => String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ',');

// ---- board quiz number answers (§7: "the number answers already in the board quiz packs") ----
const boardNumbers = new Map(); // integer -> "core-1.json: text"
const looksNumeric = (s) => /^[~≈]?-?\d[\d,.]*\s*(°C|°F|°|%|[a-z/]{1,4})?$/i.test(String(s).trim());
for (const file of readdirSync(BOARD_DIR).filter((f) => f.endsWith('.json')).sort()) {
  let pack;
  try { pack = JSON.parse(readFileSync(join(BOARD_DIR, file), 'utf8')); } catch { continue; }
  const clues = [...(pack.categories || []).flatMap((c) => c.clues || []), ...(pack.finals || [])];
  for (const c of clues) {
    if (!isStr(c.answer) || !looksNumeric(c.answer)) continue;
    const n = Number(c.answer.replace(/[~≈,]/g, '').replace(/[^\d.-].*$/, ''));
    if (Number.isInteger(n) && !boardNumbers.has(n)) boardNumbers.set(n, `${file}: "${c.text}"`);
  }
}

// ---- near-duplicate text ----
const STOP = new Set(('a an the of in on at to for from by with and or is are was were be been did does do ' +
  'how many much what which who when where whose that this its it his her their there as into about ' +
  'year years first total standard one each per in-the').split(/\s+/));
const words = (text) => new Set(String(text).toLowerCase()
  .replace(/[‘’']/g, '').replace(/[^\p{L}\p{N}]+/gu, ' ').trim().split(' ')
  .filter((w) => w && !STOP.has(w)));
const jaccard = (a, b) => {
  let inter = 0;
  for (const w of a) if (b.has(w)) inter++;
  const union = a.size + b.size - inter;
  return union ? inter / union : 0;
};

// ---- the unit should be named in the question ----
const UNIT_HINTS = {
  'km': ['kilometre', 'km'], 'm': ['metre'], 'cm': ['centimetre'], 'mm': ['millimetre'],
  '°C': ['°c', 'celsius'], 'm/s': ['metres per second'], 'km/h': ['km/h', 'kilometres per hour'],
  'kg': ['kilogram'], 'g': ['gram'], 'mg': ['milligram'], 'K': ['kelvin'], 'hPa': ['hectopascal'],
  '%': ['percent'], 'Hz': ['hertz'], 'ml': ['millilitre'], 'KB': ['kilobyte'],
  'frames/s': ['frames per second'], 'years old': ['how old'], 'US dollars': ['dollar'],
  'SHU': ['scoville'],
};
function unitNamed(text, unit) {
  const t = text.toLowerCase();
  if (UNIT_HINTS[unit]) return UNIT_HINTS[unit].some((h) => t.includes(h));
  const scale = /^(thousand|million)\b\s*(.*)$/.exec(unit);
  if (scale) {
    if (!t.includes(scale[1])) return false;
    return !scale[2] || unitNamed(text, scale[2]) || t.includes(scale[2].replace(/s$/, ''));
  }
  // "prefectures" -> "prefecture", "black keys" -> "black key"
  return t.includes(unit.toLowerCase().replace(/s$/, '').replace(/(ie)?$/, ''));
}

// ---- sources ----
const host = (u) => { try { return new URL(u).hostname.replace(/^www\./, ''); } catch { return ''; } };
const isWikipedia = (u) => /(^|\.)wikipedia\.org$/.test(host(u));

const KNOWN = new Set(['id', 'topic', 'text', 'answer', 'unit', 'format', 'min', 'max', 'spread', 'fact', 'src', 'defining']);

function checkQuestion(W, q) {
  if (!q || typeof q !== 'object') { err(W, 'not an object'); return; }
  for (const k of Object.keys(q)) if (!KNOWN.has(k)) warn(W, `unknown field "${k}"`);

  if (!TOPICS.includes(q.topic)) err(W, `topic "${q.topic}" is not one of the 15`);

  // text
  if (!isStr(q.text) || !q.text.trim()) err(W, 'text missing');
  else {
    if (len(q.text) > MAX_TEXT) err(W, `text is ${len(q.text)} characters (at most ${MAX_TEXT})`);
    if (!q.text.trim().endsWith('?')) err(W, 'text does not end with ?');
    if (q.text !== q.text.trim()) err(W, 'text has leading/trailing spaces');
    if (/\s{2,}/.test(q.text)) warn(W, 'text has a double space');
  }

  // fact
  if (!isStr(q.fact) || !q.fact.trim()) err(W, 'fact missing');
  else {
    if (len(q.fact) > MAX_FACT) err(W, `fact is ${len(q.fact)} characters (at most ${MAX_FACT})`);
    if (!/[.!"')]$/.test(q.fact.trim())) warn(W, 'fact does not end with a full stop');
  }

  // unit
  if (!isStr(q.unit)) err(W, 'unit missing ("" for none)');
  else if (len(q.unit) > MAX_UNIT) err(W, `unit "${q.unit}" is ${len(q.unit)} characters (at most ${MAX_UNIT})`);

  // numbers
  for (const k of ['answer', 'min', 'max']) {
    if (typeof q[k] !== 'number' || !Number.isInteger(q[k])) err(W, `${k} ${JSON.stringify(q[k])} is not a whole number`);
  }
  const { answer: a, min, max } = q;
  const nums = [a, min, max].every(Number.isInteger);
  if (Number.isInteger(a) && (a < MIN_ANSWER || a > MAX_ANSWER)) err(W, `answer ${a} is not between ${MIN_ANSWER} and ${fmtNum(MAX_ANSWER)}`);
  if (nums) {
    if (!(min < a && a < max)) err(W, `min ${min} < answer ${a} < max ${max} does not hold`);
    if (min < 0) err(W, `min ${min} is below 0`);
    if (max > MAX_GUESS) err(W, `max ${max} is above ${fmtNum(MAX_GUESS)}`);
  }

  // format
  if (q.format === 'plain') {
    if (nums) {
      if (max < 2 * a || min * 2 > a) err(W, `range ${min}-${max} is too narrow for ${a} (min at most half, max at least double)`);
      if (max > a * WIDE) warn(W, `max ${max} is over ${WIDE}x the answer ${a}`);
      if (min * WIDE < a && min > 0) warn(W, `min ${min} is under 1/${WIDE} of the answer ${a}`);
    }
    if (isStr(q.unit) && q.unit && isStr(q.text) && !unitNamed(q.text, q.unit)) warn(W, `unit "${q.unit}" is not named in the question`);
    if (isStr(q.unit) && /thousand|million/.test(q.unit) && isStr(q.text) && !/thousand|million/i.test(q.text)) {
      err(W, `unit "${q.unit}" needs "in thousands"/"in millions" in the question`);
    }
  } else if (q.format === 'year') {
    if (Number.isInteger(a) && (a < MIN_YEAR || a > MAX_YEAR)) err(W, `year answer ${a} is not between ${MIN_YEAR} and ${MAX_YEAR}`);
    if (q.unit !== '') err(W, 'a year question has no unit');
    if (nums && (min < MIN_YEAR - 1 || max > MAX_YEAR)) warn(W, `year range ${min}-${max} goes outside ${MIN_YEAR}-${MAX_YEAR}`);
    if (isStr(q.text) && !/\byear\b/i.test(q.text)) warn(W, 'a year question should say "what year"');
  } else {
    err(W, `format "${q.format}" is not plain or year`);
  }

  // spread
  const sp = SPREAD[q.format];
  if (sp && (typeof q.spread !== 'number' || !Number.isFinite(q.spread) || q.spread < sp[0] || q.spread > sp[1])) {
    err(W, `spread ${JSON.stringify(q.spread)} must be ${sp[0]} to ${sp[1]} for a ${q.format} question`);
  }

  // sources
  if ('defining' in q && typeof q.defining !== 'boolean') err(W, 'defining must be true or false');
  const src = Array.isArray(q.src) ? q.src : [];
  if (!Array.isArray(q.src)) err(W, 'src must be a list of web addresses');
  for (const s of src) if (!isStr(s) || !/^https?:\/\/\S+$/.test(s)) err(W, `source ${JSON.stringify(s)} is not a web address`);
  const good = src.filter((s) => isStr(s) && /^https?:\/\/\S+$/.test(s));
  if (new Set(good).size !== good.length) err(W, 'the same source is listed twice');
  if (q.defining === true) {
    if (good.length < 1) err(W, 'a defining question needs its source');
  } else {
    if (good.length < 2) err(W, `has ${good.length} sources (at least 2 that agree, or 1 with "defining": true)`);
    else if (new Set(good.map(host)).size < 2) err(W, 'its 2 sources are on the same site (need 2 independent sources)');
  }
  if (good.filter(isWikipedia).length > 1) err(W, 'Wikipedia may be one of the sources, never both');

  // the answer should not be in the question
  if (Number.isInteger(a) && isStr(q.text)) {
    const re = new RegExp(`(^|[^\\d,])(${a}|${fmtNum(a)})(?![\\d,]*\\d)`);
    if (re.test(q.text)) warn(W, `the answer ${a} appears in the question`);
  }

  // not a board quiz number
  if (Number.isInteger(a) && boardNumbers.has(a)) err(W, `answer ${a} is a number answer in the board quiz (${boardNumbers.get(a)})`);
}

// ---- read the packs ----
const files = readdirSync(PACK_DIR).filter((f) => f.endsWith('.json')).sort();
if (!files.length) { console.error(`no packs in ${PACK_DIR}`); process.exit(1); }

const ids = new Map();        // id -> file
const all = [];               // {W, q, file}
const perTopic = Object.fromEntries(TOPICS.map((t) => [t, 0]));
const packRows = [];

for (const file of files) {
  let pack;
  try { pack = JSON.parse(readFileSync(join(PACK_DIR, file), 'utf8')); } catch (e) { err(file, `bad JSON: ${e.message}`); continue; }
  const P = file;
  if (!isStr(pack.id) || !pack.id.trim()) err(P, 'pack id missing');
  else if (`${pack.id}.json` !== file) warn(P, `pack id "${pack.id}" does not match the file name`);
  if (!isStr(pack.title) || !pack.title.trim()) err(P, 'pack title missing');
  if (!Number.isInteger(pack.version) || pack.version < 1) err(P, 'version must be a positive whole number');
  if (!LICENSES.includes(pack.license)) err(P, `license "${pack.license}" is not one of ${LICENSES.join(', ')}`);
  if (!isStr(pack.attribution)) err(P, 'attribution field missing (use "" for own/CC0)');
  else if (!NO_ATTRIBUTION_NEEDED.includes(pack.license) && !pack.attribution.trim()) err(P, `license "${pack.license}" needs an attribution line`);

  const qs = Array.isArray(pack.questions) ? pack.questions : [];
  if (!qs.length) err(P, 'has no questions');
  const topics = Object.fromEntries(TOPICS.map((t) => [t, 0]));
  let years = 0;
  for (const [i, q] of qs.entries()) {
    const W = `${P} ${q?.id || `question[${i}]`}`;
    if (!isStr(q?.id) || !q.id.trim()) err(W, 'id missing');
    else {
      if (ids.has(q.id)) err(W, `id "${q.id}" already used in ${ids.get(q.id)}`);
      else ids.set(q.id, file);
      if (isStr(pack.id) && !q.id.startsWith(`${pack.id}/`)) warn(W, `id should start with "${pack.id}/"`);
    }
    checkQuestion(W, q);
    if (TOPICS.includes(q?.topic)) { topics[q.topic]++; perTopic[q.topic]++; }
    if (q?.format === 'year') years++;
    all.push({ W, q, file });
  }
  const n = qs.length;
  if (n && years * 3 > n) err(P, `${years} of its ${n} questions are years (at most a third)`);
  else if (n && years * 4 > n) warn(P, `${years} of its ${n} questions are years (aim for at most a quarter)`);
  for (const t of TOPICS) if (topics[t] < MIN_PER_TOPIC_PACK) warn(P, `topic ${t} has only ${topics[t]} questions in this pack`);
  packRows.push({ file, license: pack.license, n, years, topics });

  if (LIST) {
    console.log(`\n${file}`);
    for (const q of qs) {
      const shown = q.format === 'year' ? String(q.answer) : `${fmtNum(q.answer)}${q.unit ? ' ' + q.unit : ''}`;
      console.log(`  ${q.id}  [${q.topic}${q.format === 'year' ? ', year' : ''}]  ${q.text}\n      = ${shown}   (${q.min}-${q.max}, spread ${q.spread})${q.defining ? '  defining' : ''}`);
    }
  }
}

// ---- across packs: near-duplicates and repeated answers ----
const bag = all.map((x) => ({ ...x, w: words(x.q?.text) }));
for (let i = 0; i < bag.length; i++) {
  for (let j = i + 1; j < bag.length; j++) {
    const A = bag[i], B = bag[j];
    if (!isStr(A.q?.text) || !isStr(B.q?.text)) continue;
    if (A.q.text.trim().toLowerCase() === B.q.text.trim().toLowerCase()) { err(B.W, `same question as ${A.W}`); continue; }
    const s = jaccard(A.w, B.w);
    if (s >= NEAR_DUP) err(B.W, `near-duplicate of ${A.W} (similarity ${s.toFixed(2)})`);
    else if (s >= NEAR_DUP_SAME_ANSWER && A.q.answer === B.q.answer) err(B.W, `near-duplicate of ${A.W}: same answer and similar wording (${s.toFixed(2)})`);
  }
}
const byTopicAnswer = new Map();
for (const { q } of all) {
  if (!q || !TOPICS.includes(q.topic)) continue;
  const k = `${q.topic} ${q.answer}`;
  if (!byTopicAnswer.has(k)) byTopicAnswer.set(k, []);
  byTopicAnswer.get(k).push(q.id);
}
for (const [k, list] of byTopicAnswer) if (list.length > 1) notes.push(`${k.replace(' ', ': answer ')} used ${list.length} times (${list.join(', ')})`);

// ---- pool topic counts ----
for (const t of TOPICS) {
  if (perTopic[t] < MIN_PER_TOPIC || perTopic[t] > MAX_PER_TOPIC) err('pool', `topic ${t} has ${perTopic[t]} questions (target ${MIN_PER_TOPIC}-${MAX_PER_TOPIC})`);
}

// ---- summary ----
const pad = (s, n) => String(s).padEnd(n);
const lpad = (s, n) => String(s).padStart(n);
console.log('\nPacks');
console.log(`  ${pad('file', 18)}${pad('license', 9)}${lpad('questions', 10)}${lpad('years', 7)}${lpad('share', 7)}`);
let total = 0, totalYears = 0;
for (const r of packRows) {
  total += r.n; totalYears += r.years;
  console.log(`  ${pad(r.file, 18)}${pad(r.license, 9)}${lpad(r.n, 10)}${lpad(r.years, 7)}${lpad(r.n ? Math.round((100 * r.years) / r.n) + '%' : '-', 7)}`);
}
console.log(`  ${pad('total', 27)}${lpad(total, 10)}${lpad(totalYears, 7)}`);

console.log('\nTopics');
console.log(`  ${pad('topic', 12)}${packRows.map((r) => lpad(r.file.replace(/\.json$/, ''), 11)).join('')}${lpad('total', 7)}`);
for (const t of TOPICS) console.log(`  ${pad(t, 12)}${packRows.map((r) => lpad(r.topics[t], 11)).join('')}${lpad(perTopic[t], 7)}`);

console.log(`\nBoard quiz number answers avoided: ${[...boardNumbers.keys()].sort((x, y) => x - y).join(', ')}`);
if (notes.length) {
  console.log(`\nNotes: same answer twice in one topic (${notes.length}; fine across packs, but a Full game could deal both)`);
  for (const n of notes) console.log(`  - ${n}`);
}
if (warnings.length) {
  console.log(`\nWarnings (${warnings.length})`);
  for (const w of warnings) console.log(`  ! ${w}`);
}
if (errors.length) {
  console.log(`\nErrors (${errors.length})`);
  for (const e of errors) console.log(`  x ${e}`);
}
const failed = errors.length > 0 || (STRICT && warnings.length > 0);
console.log(`\n${failed ? 'FAILED' : 'OK'}: ${packRows.length} packs, ${total} questions, ${totalYears} years, ` +
  `${errors.length} errors, ${warnings.length} warnings`);
process.exit(failed ? 1 : 0);
