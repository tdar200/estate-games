# Near Thing question packs

Every `*.json` file here is a question pack, embedded in the server binary (this folder is **not**
served to browsers; never move packs under `web/`). The format and the rules every pack must pass are
in `docs/CLOSEST-API.md` §7. The Go loader (`packs.go`) refuses a pack that breaks them, and
`node scripts/closest-pack/validate.mjs` checks the same rules plus the house rules below.

```json
{
  "id": "closest-1", "title": "Near Thing 1", "version": 1, "license": "own", "attribution": "",
  "questions": [
    {"id": "closest-1/001", "topic": "geography",
     "text": "The Trans-Siberian Railway runs from Moscow to Vladivostok. How long is it, in kilometres?",
     "answer": 9289, "unit": "km", "format": "plain", "min": 900, "max": 90000, "spread": 40,
     "fact": "The main line from Moscow to Vladivostok is 9,289 km (5,772 miles) long.",
     "src": ["https://…", "https://…"]}
  ]
}
```

Add `"defining": true` when one source fixes the answer by definition (a law, a rule book, a standard).

## Rules (§7)

- `license` is `own` or `CC0`; anything else needs `attribution`, which the game shows in its credits.
- `text` ≤ 120 characters and ends with `?`; `fact` ≤ 140 characters; `unit` ≤ 16 characters.
- `answer` is a whole number from 10 to 999,999. Ask "in thousands" or "in millions" for anything
  bigger, and say so in `unit` too (`"thousand km"`).
- `format` is `plain` (shown with thousands separators) or `year` (1000–2025, `unit` is `""`).
- `min` < `answer` < `max`, `min` ≥ 0, `max` ≤ 9,999,999. For `plain`: `min` ≤ answer / 2 and
  `max` ≥ 2 × answer; aim for about ×10 either side, never silly. A percentage answer therefore has
  to be 50 or less (its `max` is 100).
- `spread` is the honest typical error that the computer players guess with: years for `year`
  (1–300), a percentage of the answer for `plain` (5–300).
- `src` has at least 2 independent sources that agree, or 1 defining source with `"defining": true`.
- `topic` is one of the quiz's 15: geography, science, space, nature, history, people, sport, music,
  screen, books, art, food, words, numbers, inventions.
- Ids are unique across all packs (`<pack id>/NNN`). `year` questions are at most a third of a pack.

## House rules (checked by the validator)

- Our own wording only: never copy question text from trivia sites, TV shows, apps or other games.
- The answer is fixed by history or by definition. Nothing that drifts (populations, prices,
  records, "tallest"), nothing disputed or re-measured unless one defining source settles it.
- Nobody should know it exactly, but everyone can reason toward it. Avoid answers people know by
  heart, and every number answer in the board quiz packs (`internal/quiz/packs`).
- The unit is named in the question as well as in `unit`; the answer is not in the question.
- Wikipedia may be one of the two sources, never both; two sources must be on different sites.
- No near-duplicate questions across packs; aim for at most a quarter `year` questions per pack and
  10–13 questions per topic across all packs.
- Nothing offensive, nothing about private people, nothing grim (no death tolls).

```sh
node scripts/closest-pack/validate.mjs            # all rules; --list prints every question
node scripts/closest-pack/check-src.mjs           # Wikipedia links name real articles (network)
go test ./internal/closest/ -run Pack             # the Go loader's checks
```
