#!/bin/sh
# Grandmaster's network loop (estate-ml gm): league data -> train -> gate, per
# generation. Runs detached, niced, resumable (start it again with the same
# arguments after a stop or a reboot). Status: status/gm.json (dashboard).
# usage: gm.sh [extra estate-ml gm flags]
#   default: 4 generations of 8,000 league games on 20 cores
exec "$(dirname "$0")/launch.sh" gm gm -gens 4 -games 8000 -workers 20 "$@"
