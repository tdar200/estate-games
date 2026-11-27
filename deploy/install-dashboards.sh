#!/usr/bin/env bash
# Part of the author's AI training set-up (docs/ai/EXPERT.md); not needed to play
# or to run a game server.
#
# Installs the read-only Estate AI training dashboard (cmd/mldash) as a systemd
# user service on the training machine, so it comes back after a reboot. Safe
# to run twice.
#
#   mldash  Estate AI training, 127.0.0.1:3299, reading ~/estate-ml (docs/ai/EXPERT.md)
#
# It listens on localhost by default. To open it to the LAN, set MLDASH_ADDR=:3299.
#
# Build it first:  go build -o ~/estate-ml/bin/mldash ./cmd/mldash
set -euo pipefail

MLDASH_ADDR=${MLDASH_ADDR:-127.0.0.1:3299}

[ -x "$HOME/estate-ml/bin/mldash" ] || { echo "build ~/estate-ml/bin/mldash first (see above)" >&2; exit 1; }
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
mkdir -p "$unit_dir"

cat > "$unit_dir/mldash.service" <<UNIT
[Unit]
Description=Estate AI training dashboard (read-only, ${MLDASH_ADDR})

[Service]
ExecStart=%h/estate-ml/bin/mldash -addr ${MLDASH_ADDR} -dir %h/estate-ml
Nice=15
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
UNIT
systemctl --user daemon-reload

# Stop a copy started by hand, which holds the port.
pkill -f '[e]state-ml/bin/mldash' || true
sleep 1

systemctl --user enable --now mldash.service
sleep 2
echo "mldash: $(systemctl --user is-active mldash) / $(systemctl --user is-enabled mldash)"
curl -s -m 4 -o /dev/null -w 'mldash http %{http_code}\n' "http://127.0.0.1:${MLDASH_ADDR##*:}/"
