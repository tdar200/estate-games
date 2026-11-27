#!/bin/sh
# Log CPU package temperature, hottest core, NVMe temperature and load every 30 s during long training runs.
while true; do
  pkg=0; hot=0; nv=0
  for h in /sys/class/hwmon/hwmon*; do
    n=$(cat $h/name 2>/dev/null)
    for t in $h/temp*_input; do
      [ -f "$t" ] || continue
      v=$(( $(cat $t) / 1000 )); l=$(cat ${t%_input}_label 2>/dev/null)
      case "$n:$l" in
        coretemp:Package*) pkg=$v ;;
        coretemp:*) [ $v -gt $hot ] && hot=$v ;;
        nvme:Composite) nv=$v ;;
      esac
    done
  done
  echo "$(date +%H:%M:%S) pkg=${pkg}C hottest_core=${hot}C nvme=${nv}C load=$(cut -d" " -f1 /proc/loadavg)"
  sleep 30
done
