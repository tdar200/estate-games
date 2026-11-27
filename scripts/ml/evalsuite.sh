#!/bin/sh
# Final evaluation suite: every matchup through cmd/arena (via estate-ml eval, which records
# status/eval.json and the game ledger). Usage: evalsuite.sh <budget> <games> <loan-games> [extra arena flags]
cd ~/estate-ml || exit 1
B=$1; G=$2; GL=$3; shift 3
E="bin/estate-ml eval -dir $HOME/estate-ml"
A="-budget $B -parallel 18 -workers 1 -seed 20261102 $*"
run() { name=$1; shift; nice -n 10 $E -name "$name" -- "$@" $A; }
run "HARD vs 3×NORMAL"            -games $G  -seats hard,normal,normal,normal
run "EXPERT vs 3×NORMAL"          -games $G  -seats expert,normal,normal,normal
run "EXPERT vs 3×HARD"            -games $G  -seats expert,hard,hard,hard
run "Mixed: EXPERT, HARD, NORMAL, EASY" -games $G -seats expert,hard,normal,easy
run "Ablation: HARD + tuned knobs vs 3×NORMAL" -games $GL -seats hard,normal,normal,normal -hard-params $HOME/estate-ml/tune/best.json
run "HARD vs 3×NORMAL (loans)"    -games $GL -seats hard,normal,normal,normal -rules loans
run "EXPERT vs 3×NORMAL (loans)"  -games $GL -seats expert,normal,normal,normal -rules loans
run "EXPERT vs 3×HARD (loans)"    -games $GL -seats expert,hard,hard,hard -rules loans
run "Mixed (loans)"               -games $GL -seats expert,hard,normal,easy -rules loans
echo "suite done $(date)"
