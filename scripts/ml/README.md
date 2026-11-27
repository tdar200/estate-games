# AI training scripts

The author's AI training set-up: the job scripts that tuned and trained the Expert and Grandmaster
computer players on one Linux training machine (see [docs/ai/EXPERT.md](../../docs/ai/EXPERT.md) and
[docs/ai/GRANDMASTER.md](../../docs/ai/GRANDMASTER.md)). They are not needed to play, to run a game
server or to work on the games; the trained weights ship in the repo.

They expect the layout under `~/estate-ml/` that those docs describe. The dashboard that watches a run
is `cmd/mldash` (installed as a service by `deploy/install-dashboards.sh`).
