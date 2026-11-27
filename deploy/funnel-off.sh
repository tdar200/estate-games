#!/usr/bin/env bash
# Stops the Tailscale Funnel for Estate on HTTPS port 8443 only. The funnel or
# serve config on port 443 (and any other port) is not touched.
set -euo pipefail

ts="${TAILSCALE:-tailscale}"

"$ts" funnel --https=8443 off
"$ts" funnel status || true
