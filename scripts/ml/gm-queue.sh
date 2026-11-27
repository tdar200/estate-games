#!/bin/sh
# Grandmaster work queue for the training machine: Phase 1 measurements,
# then the Phase 2 loop. Layout: ~/estate-ml/gm/{bin,arena,
# attack,done,logs} with bin/ holding linux builds of arena, gm-attack and
# estate-ml, and gm/status, gm/ledger and gm/data/{tune,gen1,gen2} symlinked
# to ~/estate-ml/status, ledger and data/... . Resumable: every finished step
# leaves a marker in gm/done/, every arena
# matchup runs in chunks of 60 games (gm/arena/<name>-<k>.json). Start it with
#   setsid nohup sh gm-queue.sh > ~/estate-ml/gm/logs/queue.log 2>&1 < /dev/null &
# and again with the same command after a stop or a reboot. Heavy work runs
# under nice 10 and pauses above 90°C.
G=$HOME/estate-ml/gm
B=$G/bin
SUM=$HOME/estate-ml/status/grandmaster.json
P=22
cd $G || exit 1
mkdir -p done arena attack logs
status() { nice -n 10 $B/estate-ml gmsummary -dir $HOME/estate-ml -phase "$1" -activity "$2" > /dev/null; }
step() { [ -f done/$1 ]; }
mark() { date > done/$1; }

# run_chunks NAME SEATS GAMES SEED [arena flags...]
run_chunks() {
  name=$1; seats=$2; total=$3; seed=$4; shift 4
  k=0
  while [ $((k*60)) -lt $total ]; do
    f=arena/$name-$k.json
    if [ ! -f $f ]; then
      status "Phase 1: measuring Grandmaster v1" "arena $name ($seats): games $((k*60+1))-$((k*60+60)) of $total"
      nice -n 10 $B/arena -seats $seats -games 60 -parallel $P -seed $((seed+k*60)) -max-temp 90 -quiet "$@" -json $f.tmp > arena/$name-$k.txt 2>&1 && mv $f.tmp $f
    fi
    k=$((k+1))
  done
}

# 1. Strength.
if ! step g-expert; then
  run_chunks g-expert grandmaster,expert,expert,expert 600 30000000
  $B/gm-attack arenasum -id g-expert -label "Grandmaster vs 3 Expert (4 players)" -summary $SUM arena/g-expert-*.json arena/laptop-g-expert.json && mark g-expert
fi
if ! step g-expert5; then
  run_chunks g-expert5 grandmaster,expert,expert,expert,expert 600 31000000
  $B/gm-attack arenasum -id g-expert5 -label "Grandmaster vs 4 Expert (5 players)" -summary $SUM arena/g-expert5-*.json && mark g-expert5
fi
if ! step g-mixed; then
  run_chunks g-mixed grandmaster,expert,hard,normal 600 32000000
  $B/gm-attack arenasum -id g-mixed -label "Grandmaster at a mixed table (Expert, Hard, Normal)" -summary $SUM arena/g-mixed-*.json && mark g-mixed
fi
if ! step g-hard; then
  run_chunks g-hard grandmaster,hard,hard,hard 480 33000000
  $B/gm-attack arenasum -id g-hard -label "Grandmaster vs 3 Hard (4 players)" -summary $SUM arena/g-hard-*.json && mark g-hard
fi
if ! step g-normal; then
  run_chunks g-normal grandmaster,normal,normal,normal 480 34000000
  $B/gm-attack arenasum -id g-normal -label "Grandmaster vs 3 Normal (4 players)" -summary $SUM arena/g-normal-*.json && mark g-normal
fi

# 2. Exploitability: the trade attack on Expert and on Grandmaster, same
# positions (24 Expert games), every accepted deal judged.
if ! step x-exploit; then
  status "Phase 1: measuring Grandmaster v1" "trade attack on Expert (24 games)"
  nice -n 10 $B/gm-attack trades -level expert -games 24 -parallel $P -rollouts 512 -rounds 8 -sample 1000000 \
    -summary $SUM -summary-prefix x -out attack/expert.jsonl > attack/expert.txt 2> attack/expert.err && mark x-exploit
fi
if ! step g-exploit; then
  status "Phase 1: measuring Grandmaster v1" "trade attack on Grandmaster (24 games)"
  nice -n 10 $B/gm-attack trades -level grandmaster -source expert -games 24 -parallel $P -rollouts 512 -rounds 8 -sample 1000000 \
    -summary $SUM -summary-prefix g -out attack/gm.jsonl > attack/gm.txt 2> attack/gm.err && mark g-exploit
fi

# 3. Thinking time as on the server: every core per decision, 2.5 s cap.
if ! step timing; then
  status "Phase 1: measuring Grandmaster v1" "decision timing on 24 cores"
  nice -n 10 $B/arena -seats grandmaster,expert,expert,expert -games 8 -parallel 1 -workers 24 -budget 2.5s -seed 31 -quiet \
    -json arena/timing.json > arena/timing.txt 2>&1 && mark timing
fi

# 4. A/B: opponent style mix off.
if ! step nostyles; then
  run_chunks nostyles grandmaster,expert,expert,expert 480 30000000 -gm-nostyles
  $B/gm-attack arenasum arena/nostyles-*.json > arena/nostyles-sum.txt && mark nostyles
fi

# 5. Phase 2: the network loop (its own resumable state in gm/gm/state.json).
status "Phase 2: Grandmaster network self-play loop" "starting"
nice -n 10 $B/estate-ml gm -dir $G -gens 4 -games 8000 -workers $P -max-temp 90 >> logs/gm.log 2>&1
