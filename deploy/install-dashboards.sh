#!/usr/bin/env bash
# Installs the read-only Estate AI training dashboard (cmd/mldash) as a systemd
# user service on the training machine, so it comes back after a reboot. Safe
# to run twice.
#
#   mldash  Estate AI training, port 3299, reading ~/estate-ml (docs/ai/EXPERT.md)
#
# Build it first:  go build -o ~/estate-ml/bin/mldash ./cmd/mldash
set -euo pipefail

[ -x "$HOME/estate-ml/bin/mldash" ] || { echo "build ~/estate-ml/bin/mldash first (see above)" >&2; exit 1; }
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
mkdir -p "$unit_dir"

cat > "$unit_dir/mldash.service" <<UNIT
[Unit]
Description=Estate AI training dashboard (read-only, port 3299)

[Service]
ExecStart=%h/estate-ml/bin/mldash -addr :3299 -dir %h/estate-ml
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
curl -s -m 4 -o /dev/null -w 'mldash http %{http_code}\n' localhost:3299/
