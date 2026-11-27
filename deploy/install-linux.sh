#!/usr/bin/env bash
# Installs the estate binary and its systemd user service for the current user.
#
#   deploy/install-linux.sh [path/to/estate-linux-amd64]
#
# Build first (from the repo root):
#   GOOS=linux GOARCH=amd64 go build -o dist/estate-linux-amd64 ./cmd/estate
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
binary="${1:-$here/../dist/estate-linux-amd64}"
bin_dir="$HOME/.local/bin"
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
conf_dir="${XDG_CONFIG_HOME:-$HOME/.config}/estate"
data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/estate"

if [[ ! -f "$binary" ]]; then
  echo "estate binary not found at $binary" >&2
  echo "build it with: GOOS=linux GOARCH=amd64 go build -o dist/estate-linux-amd64 ./cmd/estate" >&2
  exit 1
fi

mkdir -p "$bin_dir" "$unit_dir" "$conf_dir" "$data_dir"
install -m 0755 "$binary" "$bin_dir/estate.new"
mv -f "$bin_dir/estate.new" "$bin_dir/estate"
install -m 0644 "$here/estate.service" "$unit_dir/estate.service"

if [[ ! -f "$conf_dir/estate.env" ]]; then
  cat > "$conf_dir/estate.env" <<CONF
# Estate server settings (read by the estate systemd user service).
ESTATE_ADDR=:3210
ESTATE_DATA=$data_dir
# Public URL for invite links and QR codes (Tailscale Funnel on port 8443,
# see deploy/funnel-on.sh). Leave empty to use the LAN address.
ESTATE_PUBLIC_URL=
CONF
  echo "wrote $conf_dir/estate.env (set ESTATE_PUBLIC_URL there)"
fi

systemctl --user daemon-reload
systemctl --user enable estate.service
systemctl --user restart estate.service
systemctl --user --no-pager status estate.service || true

if command -v loginctl >/dev/null && [[ "$(loginctl show-user "$USER" -p Linger --value 2>/dev/null)" != "yes" ]]; then
  echo
  echo "To keep Estate running while you are logged out:"
  echo "  sudo loginctl enable-linger $USER"
fi
echo
echo "Estate is installed: http://localhost:3210/ (logs: journalctl --user -u estate -f)"
