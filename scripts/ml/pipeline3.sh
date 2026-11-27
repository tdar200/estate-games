#!/bin/sh
# Iteration: EXPERT self-play data (gen2) with the gen1 net, fine-tune the net on all data (gen2 net),
# then A/B gen1 vs gen2 nets (EXPERT vs 3 x Hard, 50 ms, same deals).
# usage: pipeline3.sh <expert-horizon> [extra arena flags for both A/B arms]
cd ~/estate-ml || exit 1
H=$1; shift
D=$HOME/estate-ml
nice -n 10 bin/estate-ml data -dir $D -set gen2 -games 20000 -shard 2000 -sample 0.3 -workers 18 -params $D/tune/best.json -expert 0.5 -budget 10ms -net $D/net/gen1/best.bin -expert-horizon $H >> logs/data-gen2.log 2>&1 < /dev/null || exit 1
nice -n 10 bin/estate-ml train -dir $D -sets tune,gen1,gen2 -out gen2 -epochs 3 -lr 2e-3 -lr-final 1e-4 -wd 0.02 -dropout 0.5 -workers 18 >> logs/train-gen2.log 2>&1 < /dev/null || exit 1
nice -n 10 bin/estate-ml evalnet -dir $D -sets gen1,gen2 -workers 18 $D/net/gen1/best.bin $D/net/gen2/best.bin >> logs/evalnet.log 2>&1
ab() { name=$1; net=$2; shift 2; nice -n 10 bin/estate-ml eval -dir $D -file $D/eval/ab.json -name "$name" -- -games 600 -seats expert,hard,hard,hard -budget 50ms -parallel 18 -workers 1 -seed 778 -expert-net $net -expert-params $D/tune/best.json -expert-horizon $H "$@"; }
ab "AB2 net gen1" $D/net/gen1/best.bin "$@"
ab "AB2 net gen2" $D/net/gen2/best.bin "$@"
echo "pipeline3 done $(date)"
