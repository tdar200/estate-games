#!/usr/bin/env node
// Checks every quiz question pack in internal/quiz/packs/*.json against the rules in
// docs/QUIZ-API.md §7, plus the house style for clues, and prints a summary.
//
//   node scripts/quiz-pack/validate.mjs            # check all packs
//   node scripts/quiz-pack/validate.mjs --list     # also print every clue, by category
//   node scripts/quiz-pack/validate.mjs --strict   # treat warnings as errors
//
// Exit code 1 when any error (or, with --strict, any warning) is found.

import { readdirSync, readFileSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const PACK_DIR = resolve(HERE, '..', '..', 'internal', 'quiz', 'packs');

const TOPICS = ['geography', 'science', 'space', 'nature', 'history', 'people', 'sport', 'music',
  'screen', 'books', 'art', 'food', 'words', 'numbers', 'inventions'];
const LICENSES = ['own', 'CC0', 'CC BY-SA 4.0'];
const NO_ATTRIBUTION_NEEDED = ['own', 'CC0'];

// Targets from the brief (not engine rules, but checked so the pool stays balanced).
const MIN_CATEGORIES = 60;
const MIN_FINALS = 15;
const MIN_PER_TOPIC = 3;
const MAX_PER_TOPIC = 6;
const MAX_CATEGORIES_PER_PACK = 40;

const MAX_TEXT = 140;
const MAX_CHOICE = 32;
const MAX_TITLE = 24;

const args = new Set(process.argv.slice(2));
const LIST = args.has('--list');
const STRICT = args.has('--strict');

const errors = [];
const warnings = [];
const err = (where, msg) => errors.push(`${where}: ${msg}`);
const warn = (where, msg) => warnings.push(`${where}: ${msg}`);

// Case- and space-insensitive comparison key (§7).
const norm = (s) => String(s).toLowerCase().replace(/\s+/g, '');
const isStr = (s) => typeof s === 'string';
const len = (s) => [...s].length; // count characters, not UTF-16 units

// "1,760", "2.54", "88 mph", "100 °C", "720°" -> numeric look (but not "7th Heaven"); used to
// check that the four choices are the same kind.
const looksNumeric = (s) => /^[~≈]?-?\d[\d,.]*\s*(°C|°F|°|%|[a-z/]{1,4})?$/i.test(s.trim());

function checkText(where, text) {
  if (!isStr(text) || !text.trim()) { err(where, 'text missing'); return; }
  if (len(text) > MAX_TEXT) err(where, `text is ${len(text)} characters (max ${MAX_TEXT})`);
  if (text !== text.trim()) err(where, 'text has leading/trailing spaces');
  if (/\s{2,}/.test(text)) warn(where, 'text has a double space');
  if (/which of (these|the following)/i.test(text)) err(where, 'text says "which of these" (ask directly)');
  if (!/[?]$/.test(text)) warn(where, 'text does not end with a question mark');
}

function checkChoice(where, label, s) {
  if (!isStr(s) || !s.trim()) { err(where, `${label} missing`); return; }
  if (len(s) > MAX_CHOICE) err(where, `${label} "${s}" is ${len(s)} characters (max ${MAX_CHOICE})`);
  if (s !== s.trim()) err(where, `${label} "${s}" has leading/trailing spaces`);
  if (/[.]$/.test(s) && !/\b[A-Z]\.$/.test(s)) err(where, `${label} "${s}" ends with a full stop`);
  if (/(all|none|both) of (the )?(above|these)/i.test(s)) err(where, `${label} "${s}" is an "of the above" choice`);
}

function checkClue(where, c, { needTier }) {
  if (!c || typeof c !== 'object') { err(where, 'not an object'); return; }
  checkText(where, c.text);
  checkChoice(where, 'answer', c.answer);
  if (!Array.isArray(c.wrong) || c.wrong.length !== 3) {
    err(where, `needs exactly 3 wrong choices (has ${Array.isArray(c.wrong) ? c.wrong.length : 'none'})`);
  } else {
    c.wrong.forEach((w, i) => checkChoice(where, `wrong[${i}]`, w));
    const keys = [c.answer, ...c.wrong].filter(isStr).map(norm);
    if (new Set(keys).size !== keys.length) err(where, 'answer and wrong choices are not all different (case/space-insensitive)');
    // Same kind of choice: if the answer is a number, the wrong ones must be numbers too (and vice versa).
    if (isStr(c.answer) && c.wrong.every(isStr)) {
      const a = looksNumeric(c.answer);
      const odd = c.wrong.filter((w) => looksNumeric(w) !== a);
      if (odd.length) warn(where, `choices mix numbers and words: ${[c.answer, ...c.wrong].join(' | ')}`);
      // A right answer much longer than every wrong one is a giveaway.
      const L = len(c.answer), maxW = Math.max(...c.wrong.map(len));
      if (L > 2 * maxW + 4) warn(where, `answer "${c.answer}" is far longer than the wrong choices`);
    }
    // The answer should not be spelt out in the clue.
    if (isStr(c.answer) && isStr(c.text) && len(c.answer) >= 4 &&
        norm(c.text).includes(norm(c.answer))) warn(where, `answer "${c.answer}" appears in the clue text`);
  }
  if (!isStr(c.src) || !/^https?:\/\/\S+$/.test(c.src)) err(where, 'src missing or not a URL');
  if (needTier && !Number.isInteger(c.tier)) err(where, 'tier missing');
  const known = new Set(['tier', 'text', 'answer', 'wrong', 'src', 'id', 'category', 'topic', 'note']);
  for (const k of Object.keys(c)) if (!known.has(k)) warn(where, `unknown field "${k}"`);
}

function checkTitle(where, title) {
  if (!isStr(title) || !title.trim()) { err(where, 'title missing'); return; }
  if (len(title) > MAX_TITLE) err(where, `title "${title}" is ${len(title)} characters (max ${MAX_TITLE})`);
  if (title !== title.toUpperCase()) err(where, `title "${title}" is not upper case`);
  if (title !== title.trim()) err(where, `title "${title}" has leading/trailing spaces`);
}

const files = readdirSync(PACK_DIR).filter((f) => f.endsWith('.json')).sort();
if (!files.length) { console.error(`no packs in ${PACK_DIR}`); process.exit(1); }

const ids = new Map();          // id -> where first seen
const clueTexts = new Map();    // normalised clue text -> where
const answersByTopic = new Map(); // topic -> Map(normalised answer -> [where])
const perTopic = Object.fromEntries(TOPICS.map((t) => [t, 0]));
const finalsPerTopic = Object.fromEntries(TOPICS.map((t) => [t, 0]));
const tierLen = { 1: [], 2: [], 3: [], 4: [], 5: [] };
const packRows = [];
let totalCats = 0, totalClues = 0, totalFinals = 0;

function claimId(id, where) {
  if (!isStr(id) || !id.trim()) { err(where, 'id missing'); return; }
  if (ids.has(id)) err(where, `id "${id}" already used at ${ids.get(id)}`);
  else ids.set(id, where);
}

function noteText(where, text) {
  if (!isStr(text)) return;
  const k = norm(text);
  if (clueTexts.has(k)) err(where, `same clue text as ${clueTexts.get(k)}`);
  else clueTexts.set(k, where);
}

function noteAnswer(topic, answer, where) {
  if (!isStr(answer)) return;
  if (!answersByTopic.has(topic)) answersByTopic.set(topic, new Map());
  const m = answersByTopic.get(topic);
  const k = norm(answer);
  if (!m.has(k)) m.set(k, []);
  m.get(k).push(where);
}

for (const file of files) {
  const path = join(PACK_DIR, file);
  let pack;
  try { pack = JSON.parse(readFileSync(path, 'utf8')); } catch (e) { err(file, `bad JSON: ${e.message}`); continue; }
  const P = file;
  if (!isStr(pack.id) || !pack.id.trim()) err(P, 'pack id missing');
  else if (`${pack.id}.json` !== file) warn(P, `pack id "${pack.id}" does not match the file name`);
  if (!isStr(pack.title) || !pack.title.trim()) err(P, 'pack title missing');
  if (!Number.isInteger(pack.version) || pack.version < 1) err(P, 'version must be a positive integer');
  if (!LICENSES.includes(pack.license)) err(P, `license "${pack.license}" not one of ${LICENSES.join(', ')}`);
  if (!NO_ATTRIBUTION_NEEDED.includes(pack.license) && !(isStr(pack.attribution) && pack.attribution.trim())) {
    err(P, `license "${pack.license}" needs an attribution line`);
  }
  if (pack.license === 'CC BY-SA 4.0' && isStr(pack.attribution)) {
    if (!/creativecommons\.org\/licenses\/by-sa\/4\.0/.test(pack.attribution)) err(P, 'CC BY-SA attribution must link the licence');
    if (!/changed|adapted|modified|edited/i.test(pack.attribution)) err(P, 'CC BY-SA attribution must say the material was changed');
  }
  if (!isStr(pack.attribution)) err(P, 'attribution field missing (use "" for own/CC0)');
  const cats = Array.isArray(pack.categories) ? pack.categories : [];
  const finals = Array.isArray(pack.finals) ? pack.finals : [];
  if (!Array.isArray(pack.categories)) err(P, 'categories missing');
  if (!Array.isArray(pack.finals)) err(P, 'finals missing (use [] for none)');
  if (cats.length > MAX_CATEGORIES_PER_PACK) warn(P, `${cats.length} categories (keep packs to ~${MAX_CATEGORIES_PER_PACK})`);

  for (const [ci, cat] of cats.entries()) {
    const C = `${P} ${cat?.id || `category[${ci}]`}`;
    claimId(cat?.id, C);
    if (isStr(cat?.id) && isStr(pack.id) && !cat.id.startsWith(`${pack.id}/`)) warn(C, `id should start with "${pack.id}/"`);
    checkTitle(C, cat?.title);
    if (!TOPICS.includes(cat?.topic)) err(C, `topic "${cat?.topic}" not in the list`);
    else perTopic[cat.topic]++;
    const clues = Array.isArray(cat?.clues) ? cat.clues : [];
    if (clues.length !== 5) err(C, `needs exactly 5 clues (has ${clues.length})`);
    const tiers = clues.map((c) => c?.tier).sort();
    if (tiers.join(',') !== '1,2,3,4,5') err(C, `tiers must be 1,2,3,4,5 once each (has ${tiers.join(',')})`);
    for (const [k, clue] of clues.entries()) {
      const W = `${C} tier ${clue?.tier ?? `#${k}`}`;
      checkClue(W, clue, { needTier: true });
      noteText(W, clue?.text);
      if (TOPICS.includes(cat?.topic)) noteAnswer(cat.topic, clue?.answer, W);
      if (tierLen[clue?.tier] && isStr(clue?.text)) tierLen[clue.tier].push(len(clue.text));
      totalClues++;
    }
    totalCats++;
    if (LIST) {
      console.log(`\n[${cat.topic}] ${cat.title}  (${cat.id})`);
      for (const c of [...clues].sort((a, b) => a.tier - b.tier)) {
        console.log(`  ${c.tier}. ${c.text}\n       = ${c.answer}   (x ${(c.wrong || []).join(' / ')})`);
      }
    }
  }

  for (const [fi, f] of finals.entries()) {
    const F = `${P} ${f?.id || `final[${fi}]`}`;
    claimId(f?.id, F);
    if (isStr(f?.id) && isStr(pack.id) && !f.id.startsWith(`${pack.id}/`)) warn(F, `id should start with "${pack.id}/"`);
    checkTitle(F, f?.category);
    if (!TOPICS.includes(f?.topic)) err(F, `topic "${f?.topic}" not in the list`);
    else finalsPerTopic[f.topic]++;
    if ('tier' in (f || {})) warn(F, 'finals have no tier');
    checkClue(F, f, { needTier: false });
    noteText(F, f?.text);
    totalFinals++;
    if (LIST) console.log(`\n[final/${f.topic}] ${f.category}  (${f.id})\n  ${f.text}\n       = ${f.answer}   (x ${(f.wrong || []).join(' / ')})`);
  }
  packRows.push([file, pack.license, cats.length, cats.length * 5, finals.length]);
}

// The same answer twice in one topic makes a repeat likely when that topic is dealt.
for (const [topic, m] of answersByTopic) {
  for (const [, where] of m) if (where.length > 1) warn(`topic ${topic}`, `same answer used ${where.length} times: ${where.join('; ')}`);
}

// Pool-wide targets.
if (totalCats < MIN_CATEGORIES) err('pool', `${totalCats} categories (target at least ${MIN_CATEGORIES})`);
if (totalFinals < MIN_FINALS) err('pool', `${totalFinals} finals (target at least ${MIN_FINALS})`);
for (const t of TOPICS) {
  if (perTopic[t] < MIN_PER_TOPIC || perTopic[t] > MAX_PER_TOPIC) {
    err('pool', `topic ${t} has ${perTopic[t]} categories (target ${MIN_PER_TOPIC}-${MAX_PER_TOPIC})`);
  }
}
const topicsWithCats = TOPICS.filter((t) => perTopic[t] > 0).length;
if (topicsWithCats < 6) err('pool', `only ${topicsWithCats} topics have categories; a board needs 6 different topics`);

// ---- summary ----
const pad = (s, n) => String(s).padEnd(n);
const lpad = (s, n) => String(s).padStart(n);
console.log('\nPacks');
console.log(`  ${pad('file', 22)}${pad('license', 14)}${lpad('categories', 11)}${lpad('clues', 7)}${lpad('finals', 8)}`);
for (const r of packRows) console.log(`  ${pad(r[0], 22)}${pad(r[1], 14)}${lpad(r[2], 11)}${lpad(r[3], 7)}${lpad(r[4], 8)}`);
console.log(`  ${pad('total', 36)}${lpad(totalCats, 11)}${lpad(totalClues, 7)}${lpad(totalFinals, 8)}`);

console.log('\nTopics');
console.log(`  ${pad('topic', 12)}${lpad('categories', 11)}${lpad('finals', 8)}`);
for (const t of TOPICS) console.log(`  ${pad(t, 12)}${lpad(perTopic[t], 11)}${lpad(finalsPerTopic[t], 8)}`);

console.log('\nClue length by tier (characters: average / longest)');
for (const t of [1, 2, 3, 4, 5]) {
  const a = tierLen[t];
  if (!a.length) continue;
  console.log(`  tier ${t}: ${Math.round(a.reduce((x, y) => x + y, 0) / a.length)} / ${Math.max(...a)}`);
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
console.log(`\n${failed ? 'FAILED' : 'OK'}: ${totalCats} categories, ${totalClues} clues, ${totalFinals} finals, ` +
  `${errors.length} errors, ${warnings.length} warnings`);
process.exit(failed ? 1 : 0);
