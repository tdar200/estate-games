# Estate on a small AWS server

Scripts that put Estate on its own AWS server with HTTPS: one `t4g.nano` (arm64, about $3-4 a month),
Caddy in front for the Let's Encrypt certificate, and a Route 53 name. Optional: Estate runs anywhere
Go runs (see `deploy/install-linux.sh` for a plain Linux box).

You need: the AWS CLI with credentials, a public Route 53 hosted zone for your domain, and `ssh`.

```sh
export ESTATE_DOMAIN=games.example.com    # the name players open
# export ESTATE_ZONE=example.com          # the hosted zone, if it isn't ESTATE_DOMAIN minus its first label

./provision.sh --dry-run                  # look everything up, change nothing
./provision.sh                            # key pair, security group, server, Elastic IP, DNS record

# build for the server and install it (again for every new version)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "-X main.version=1.2.3" -o dist/estate-linux-arm64 ./cmd/estate
./update.sh dist/estate-linux-arm64
./update.sh --rollback                    # put the previous build back

./backup.sh                               # copy the server's data here (~/backups/estate, 14 days kept)
```

- `provision.sh` only adds things and skips any piece that already exists, so it is safe to run again.
  It stops if `ESTATE_DOMAIN` already points somewhere else. ssh (port 22) is open only to the address
  you ran it from; the key is saved to `~/.ssh/estate.pem` (`ESTATE_AWS_NAME` changes the name).
- `cloud-init.yaml` is the server's first-boot setup: Caddy, the `estate` user, a locked-down systemd
  unit, and 1 GB of swap. `provision.sh` fills in `__ESTATE_DOMAIN__`.
- The site sends `noindex` and a robots.txt that disallows everything, so search engines leave it out;
  anyone with the link can still play. Remove those lines from `cloud-init.yaml` if you want it listed.
- Games in progress survive an update or a restart (`/var/lib/estate/rooms.json`); phones reconnect by
  themselves.
- `update.sh --data <dir>` copies an existing data folder onto a new, empty server once.
