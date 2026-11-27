#!/usr/bin/env bash
# Nightly copy of the games server's data to this machine.
# Keeps the last 14 days in ~/backups/estate. Meant for a crontab:
#   15 4 * * * ESTATE_DOMAIN=games.example.com $HOME/path/to/estate/deploy/aws/backup.sh >> $HOME/backups/estate/backup.log 2>&1
# Restore: stop estate on the server, untar into /var/lib/estate, chown estate:estate, start.
set -euo pipefail

HOST=${ESTATE_DOMAIN:?set ESTATE_DOMAIN to the server name, e.g. games.example.com}
KEY=${ESTATE_KEY:-$HOME/.ssh/estate.pem}
OUT=${ESTATE_BACKUPS:-$HOME/backups/estate}
mkdir -p "$OUT"
file=$OUT/estate-$(date +%F).tar.gz
ssh -i "$KEY" -o BatchMode=yes -o ConnectTimeout=20 -o StrictHostKeyChecking=accept-new "ubuntu@$HOST" \
  'sudo tar -C /var/lib/estate -czf - --exclude=server.log.1 .' > "$file.part"
tar -tzf "$file.part" >/dev/null
mv -f "$file.part" "$file"
find "$OUT" -name 'estate-*.tar.gz' -mtime +14 -delete
echo "$(date -Is) backed up $(du -h "$file" | cut -f1) to $file"
