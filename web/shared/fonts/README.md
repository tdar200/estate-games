# Fonts

Served by Estate itself (`/shared/fonts.css`, `/shared/fonts-tabletop.css`), so the apps work on a
network without internet. All files are the Latin subsets Google Fonts serves, unchanged: Oswald
and Inter as one variable file each, Barlow and Barlow Condensed as one static file per weight.

| File | Typeface | Copyright | Licence |
|---|---|---|---|
| `Oswald-latin.woff2` | Oswald | The Oswald Project Authors (https://github.com/googlefonts/OswaldFont) | SIL Open Font License 1.1 |
| `Inter-latin.woff2` | Inter | The Inter Project Authors (https://github.com/rsms/inter) | SIL Open Font License 1.1 |
| `BarlowCondensed-{500,600,700,800}-latin.woff2` | Barlow Condensed | The Barlow Project Authors (https://github.com/jpt/barlow) | SIL Open Font License 1.1 (`OFL-Barlow.txt`) |
| `Barlow-{400,500,600,700}-latin.woff2` | Barlow | The Barlow Project Authors (https://github.com/jpt/barlow) | SIL Open Font License 1.1 (`OFL-Barlow.txt`) |

`fonts.css` declares Oswald and Inter (every page). `fonts-tabletop.css` declares Barlow and Barlow
Condensed for the tabletop look (the TV table links and preloads it).

The licence text: https://openfontlicense.org/open-font-license-official-text/ (also in each
project's repository as `OFL.txt`, and for Barlow in `OFL-Barlow.txt` here). The fonts may be used,
bundled and redistributed with this software; they may not be sold on their own.
