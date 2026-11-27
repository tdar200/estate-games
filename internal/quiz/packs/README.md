# Quiz question packs

Every `*.json` file here is a question pack, embedded in the server binary (this folder is **not**
served to browsers; never move packs under `web/`). The format and the rules every pack must pass are
in `docs/QUIZ-API.md` §7. In short:

- `license` is `own`, `CC0` or `CC BY-SA 4.0`; anything but `own`/`CC0` needs `attribution`, which
  the game shows in its credits. CC BY-SA material stays in its own pack file.
- Each category has exactly 5 clues, tiers 1 (easiest) to 5 (hardest), on one theme.
- Clue `text` ≤ 140 characters; `answer` and each of the 3 `wrong` choices ≤ 32 characters, all
  different; category `title` ≤ 24 characters, upper case; `topic` from the fixed list.
- Every clue has a `src`: where the fact was checked.
- Ids are unique across all packs.
