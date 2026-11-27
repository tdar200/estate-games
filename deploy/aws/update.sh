#!/usr/bin/env bash
# Puts a new Estate build on the games server (ESTATE_DOMAIN), or goes back to
# the one before. Run where provision.sh ran; uses ~/.ssh/estate.pem (ESTATE_KEY).
#
# Usage:
#   update.sh <estate-linux-arm64 binary>   install it, keeping the old one as estate.prev
#   update.sh --rollback                    put estate.prev back
#   update.sh --data <dir>                  copy a data folder (rooms.json, estate.json, games/)
#                                           into an empty /var/lib/estate, once, before first start
# Build the binary on any machine with:
#   GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "-X main.version=X.Y.Z" -o dist/estate-X.Y.Z-linux-arm64 ./cmd/estate
# After a restart, games in progress carry on (rooms.json); phones reconnect by themselves.
set -euo pipefail

HOST=${ESTATE_DOMAIN:?set ESTATE_DOMAIN to the server name, e.g. games.example.com}
KEY=${ESTATE_KEY:-$HOME/.ssh/estate.pem}
SSH=(ssh -i "$KEY" -o BatchMode=yes -o ConnectTimeout=20 -o StrictHostKeyChecking=accept-new "ubuntu@$HOST")

stop() { echo "STOP: $*" >&2; exit 1; }
[ -s "$KEY" ] || stop "missing $KEY"

check() {
  "${SSH[@]}" 'set -e
    sleep 2
    systemctl is-active estate
    /opt/estate/estate version
    curl -fsS -o /dev/null -w "local %{http_code}\n" http://127.0.0.1:3210/'
  curl -fsS -o /dev/null -w "https://$HOST %{http_code}\n" "https://$HOST/"
}

case "${1:-}" in
  --rollback)
    "${SSH[@]}" 'set -e
      [ -x /opt/estate/estate.prev ] || { echo "no estate.prev to go back to" >&2; exit 1; }
      sudo mv -f /opt/estate/estate.prev /opt/estate/estate
      sudo systemctl restart estate'
    check
    ;;
  --data)
    dir=${2:-}
    [ -f "$dir/rooms.json" ] || [ -f "$dir/estate.json" ] || stop "$dir has no rooms.json or estate.json"
    "${SSH[@]}" 'cloud-init status --wait >/dev/null; sudo find /var/lib/estate -mindepth 1 -maxdepth 1 | grep -q . && { echo "/var/lib/estate is not empty" >&2; exit 1; }; true'
    tar -C "$dir" -czf - --exclude='*.pre-*' --exclude='server.log*' . | "${SSH[@]}" 'set -e
      sudo systemctl stop estate || true
      sudo tar -C /var/lib/estate -xzf - --no-same-owner
      sudo chown -R estate:estate /var/lib/estate
      sudo chmod 0750 /var/lib/estate
      [ -x /opt/estate/estate ] && sudo systemctl start estate || true'
    echo "data copied"
    ;;
  ""|-*)
    echo "usage: $0 <estate-linux-arm64 binary> | --rollback | --data <dir>" >&2; exit 2
    ;;
  *)
    bin=$1
    [ -f "$bin" ] || stop "no file $bin"
    file "$bin" | grep -q "ARM aarch64" || stop "$bin is not a linux arm64 build"
    "${SSH[@]}" 'cloud-init status --wait >/dev/null'
    scp -q -i "$KEY" -o BatchMode=yes "$bin" "ubuntu@$HOST:/tmp/estate.new"
    "${SSH[@]}" 'set -e
      sudo install -m 0755 /tmp/estate.new /opt/estate/estate.new
      rm -f /tmp/estate.new
      [ -f /opt/estate/estate ] && sudo cp -p /opt/estate/estate /opt/estate/estate.prev
      sudo mv -f /opt/estate/estate.new /opt/estate/estate
      sudo systemctl restart estate'
    check
    ;;
esac
