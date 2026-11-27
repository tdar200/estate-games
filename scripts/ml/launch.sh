#!/bin/sh
# usage: launch.sh <logname> <estate-ml args...>   (runs detached, niced, appending to logs/<logname>.log)
cd ~/estate-ml || exit 1
name=$1; shift
setsid nohup nice -n 10 bin/estate-ml "$@" >> logs/$name.log 2>&1 < /dev/null &
echo "started $name pid $!"
