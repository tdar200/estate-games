#!/bin/sh
# usage: detach.sh <logname> <command...>   run a command detached, appending to logs/<logname>.log
cd ~/estate-ml || exit 1
name=$1; shift
setsid nohup "$@" >> logs/$name.log 2>&1 < /dev/null &
echo "started $name pid $!"
