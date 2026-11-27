#!/usr/bin/env bash
# Exposes the local Estate server (127.0.0.1:3210) to the internet through
# Tailscale Funnel on HTTPS port 8443 ONLY. Any existing serve/funnel config
# on port 443 (or other ports) is left untouched.
#
# Afterwards set ESTATE_PUBLIC_URL=https://<machine>.<tailnet>.ts.net:8443 in
# ~/.config/estate/estate.env and restart: systemctl --user restart estate
#
# Needs Tailscale >= 1.52 and Funnel enabled for this node in the tailnet ACLs.
# If the tailscale CLI needs root, run: sudo tailscale set --operator="$USER"
set -euo pipefail

port="${ESTATE_PORT:-3210}"
ts="${TAILSCALE:-tailscale}"

"$ts" funnel --bg --https=8443 "http://127.0.0.1:${port}"
"$ts" funnel status

dns="$("$ts" status --json 2>/dev/null | sed -n 's/.*"DNSName": *"\([^"]*\)\.".*/\1/p' | head -n 1 || true)"
if [[ -n "$dns" ]]; then
  echo
  echo "Public URL: https://${dns}:8443"
  echo "Set ESTATE_PUBLIC_URL=https://${dns}:8443 in ~/.config/estate/estate.env"
fi
