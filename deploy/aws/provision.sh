#!/usr/bin/env bash
# Creates Estate's own small AWS server for ESTATE_DOMAIN (e.g. games.example.com).
#
# Run it with AWS credentials for your account (aws login, or a profile). It
# only adds things; it never changes or deletes anything that was there
# before. Each step is skipped when its piece already exists, so running it
# again is safe. What it creates (all named ESTATE_AWS_NAME, default "estate",
# in your configured AWS region):
#   - key pair, saved to ~/.ssh/<name>.pem (mode 600)
#   - security group: 80 and 443 open to all, 22 from this machine's address only
#   - one t4g.nano Ubuntu 24.04 arm64 server (8 GB encrypted disk, standard
#     CPU credits so it can never bill extra), user data cloud-init.yaml
#   - an Elastic IP on that server
#   - the Route 53 record ESTATE_DOMAIN -> that IP (A, TTL 300), in the public
#     hosted zone ESTATE_ZONE (default: ESTATE_DOMAIN minus its first label)
# It stops if ESTATE_DOMAIN already points somewhere else.
#
# Usage: ESTATE_DOMAIN=games.example.com provision.sh [--dry-run]
#   --dry-run  look everything up and print what would be created; change nothing
# Then: update.sh <estate-linux-arm64 binary>
set -euo pipefail

export AWS_PAGER=""
export AWS_REGION=${AWS_REGION:-$(aws configure get region 2>/dev/null || true)}
NAME=${ESTATE_AWS_NAME:-estate}
TYPE=${ESTATE_AWS_TYPE:-t4g.nano}
DOMAIN=${ESTATE_DOMAIN:-}
ZONE=${ESTATE_ZONE:-${DOMAIN#*.}}
ZONE=${ZONE%.}.
KEY_FILE=$HOME/.ssh/$NAME.pem
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TEMPLATE=$HERE/cloud-init.yaml
AMI_PARAM=/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id

DRY=false
case "${1:-}" in
  "") ;;
  --dry-run) DRY=true ;;
  *) echo "usage: $0 [--dry-run]" >&2; exit 2 ;;
esac

stop() { echo "STOP: $*" >&2; exit 1; }
say() { echo "== $*"; }
none() { [ -z "$1" ] || [ "$1" = None ]; }

[[ $DOMAIN =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?(.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$ ]] || stop "set ESTATE_DOMAIN to the server's name, e.g. games.example.com"
[ -n "$AWS_REGION" ] || stop "no AWS region: set AWS_REGION or run aws configure"
[ -f "$TEMPLATE" ] || stop "missing $TEMPLATE"
USER_DATA=$(mktemp)
trap 'rm -f "$USER_DATA"' EXIT
sed "s/__ESTATE_DOMAIN__/$DOMAIN/g" "$TEMPLATE" > "$USER_DATA"
aws sts get-caller-identity --query Arn --output text >/dev/null 2>&1 || stop "no AWS credentials: run aws login or set AWS_PROFILE"

# DNS first: refuse before creating anything if the name is already taken.
zone_id=$(aws route53 list-hosted-zones-by-name --dns-name "$ZONE" \
  --query "HostedZones[?Name=='$ZONE' && Config.PrivateZone==\`false\`].Id" --output text)
[ "$(wc -w <<<"$zone_id")" = 1 ] || stop "expected one public hosted zone for $ZONE, found: '$zone_id'"
existing=$(aws route53 list-resource-record-sets --hosted-zone-id "$zone_id" \
  --query "ResourceRecordSets[?Name=='$DOMAIN.'].[Type, join(',', ResourceRecords[].Value || \`[]\`)]" --output text)

vpc=$(aws ec2 describe-vpcs --filters Name=isDefault,Values=true --query 'Vpcs[0].VpcId' --output text)
none "$vpc" && stop "no default VPC in $AWS_REGION"
my_ip=$(curl -fsS -m 10 https://checkip.amazonaws.com | tr -d '[:space:]')
[[ $my_ip =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || stop "could not find this machine's public address"
ami=$(aws ssm get-parameter --name "$AMI_PARAM" --query Parameter.Value --output text)

instance=$(aws ec2 describe-instances \
  --filters "Name=tag:Name,Values=$NAME" Name=instance-state-name,Values=pending,running,stopping,stopped \
  --query 'Reservations[].Instances[].InstanceId' --output text)
[ "$(wc -w <<<"$instance")" -le 1 ] || stop "more than one $NAME server: $instance"
eip_alloc=$(aws ec2 describe-addresses --filters "Name=tag:Name,Values=$NAME" --query 'Addresses[0].AllocationId' --output text)
eip=$(aws ec2 describe-addresses --filters "Name=tag:Name,Values=$NAME" --query 'Addresses[0].PublicIp' --output text)

if [ -n "$existing" ]; then
  none "$eip" && stop "$DOMAIN already exists ($existing) and no $NAME address is ours: not touching it"
  [ "$existing" = "A"$'\t'"$eip" ] || stop "$DOMAIN already points to: $existing (ours would be $eip): not touching it"
fi

say "account ok, region $AWS_REGION, zone $zone_id, default VPC $vpc, this machine $my_ip, AMI $ami"
if $DRY; then
  key_now=$(aws ec2 describe-key-pairs --key-names "$NAME" --query 'KeyPairs[0].KeyName' --output text 2>/dev/null || true)
  sg_now=$(aws ec2 describe-security-groups --filters "Name=group-name,Values=$NAME" "Name=vpc-id,Values=$vpc" \
    --query 'SecurityGroups[0].GroupId' --output text)
  echo "key pair:       $(none "$key_now" && echo "create $NAME -> $KEY_FILE" || echo "exists")"
  echo "security group: $(none "$sg_now" && echo "create $NAME (80, 443 all; 22 from $my_ip/32)" || echo "exists $sg_now")"
  echo "server:         $(none "$instance" && echo "create $TYPE from $ami" || echo "exists $instance")"
  echo "Elastic IP:     $(none "$eip" && echo "allocate and attach" || echo "exists $eip")"
  echo "DNS:            $([ -n "$existing" ] && echo "already $existing" || echo "create A $DOMAIN -> (the Elastic IP)")"
  echo "dry run: nothing changed"
  exit 0
fi

# Key pair.
if ! aws ec2 describe-key-pairs --key-names "$NAME" >/dev/null 2>&1; then
  [ -e "$KEY_FILE" ] && stop "$KEY_FILE exists but AWS has no key pair $NAME: move the file aside first"
  say "creating key pair $NAME"
  umask 077
  aws ec2 create-key-pair --key-name "$NAME" --key-type ed25519 --key-format pem \
    --tag-specifications "ResourceType=key-pair,Tags=[{Key=Name,Value=$NAME},{Key=project,Value=estate}]" \
    --query KeyMaterial --output text > "$KEY_FILE"
  chmod 600 "$KEY_FILE"
fi
[ -s "$KEY_FILE" ] || stop "AWS has key pair $NAME but $KEY_FILE is missing"

# Security group.
sg=$(aws ec2 describe-security-groups --filters "Name=group-name,Values=$NAME" "Name=vpc-id,Values=$vpc" \
  --query 'SecurityGroups[0].GroupId' --output text)
if none "$sg"; then
  say "creating security group $NAME"
  sg=$(aws ec2 create-security-group --group-name "$NAME" --description "Estate games server: web open, ssh from the admin address only" \
    --vpc-id "$vpc" --tag-specifications "ResourceType=security-group,Tags=[{Key=Name,Value=$NAME},{Key=project,Value=estate}]" \
    --query GroupId --output text)
  aws ec2 authorize-security-group-ingress --group-id "$sg" --ip-permissions \
    "IpProtocol=tcp,FromPort=80,ToPort=80,IpRanges=[{CidrIp=0.0.0.0/0}],Ipv6Ranges=[{CidrIpv6=::/0}]" \
    "IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=0.0.0.0/0}],Ipv6Ranges=[{CidrIpv6=::/0}]" \
    "IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=$my_ip/32,Description=admin}]" >/dev/null
fi

# Server.
if none "$instance"; then
  say "creating $TYPE server"
  instance=$(aws ec2 run-instances --image-id "$ami" --instance-type "$TYPE" --key-name "$NAME" \
    --security-group-ids "$sg" --user-data "file://$USER_DATA" \
    --block-device-mappings 'DeviceName=/dev/sda1,Ebs={VolumeSize=8,VolumeType=gp3,Encrypted=true,DeleteOnTermination=true}' \
    --metadata-options HttpTokens=required,HttpEndpoint=enabled \
    --credit-specification CpuCredits=standard \
    --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=$NAME},{Key=project,Value=estate}]" \
      "ResourceType=volume,Tags=[{Key=Name,Value=$NAME},{Key=project,Value=estate}]" \
    --query 'Instances[0].InstanceId' --output text)
fi
say "waiting for $instance to run"
aws ec2 wait instance-running --instance-ids "$instance"

# Elastic IP.
if none "$eip_alloc"; then
  say "allocating Elastic IP"
  eip_alloc=$(aws ec2 allocate-address --domain vpc \
    --tag-specifications "ResourceType=elastic-ip,Tags=[{Key=Name,Value=$NAME},{Key=project,Value=estate}]" \
    --query AllocationId --output text)
  eip=$(aws ec2 describe-addresses --allocation-ids "$eip_alloc" --query 'Addresses[0].PublicIp' --output text)
fi
attached=$(aws ec2 describe-addresses --allocation-ids "$eip_alloc" --query 'Addresses[0].InstanceId' --output text)
if [ "$attached" != "$instance" ]; then
  none "$attached" || stop "Elastic IP $eip is attached to $attached, not $instance"
  aws ec2 associate-address --allocation-id "$eip_alloc" --instance-id "$instance" >/dev/null
fi

# DNS.
if [ -z "$existing" ]; then
  say "adding $DOMAIN -> $eip"
  change=$(aws route53 change-resource-record-sets --hosted-zone-id "$zone_id" --change-batch "{
    \"Comment\": \"Estate games server\",
    \"Changes\": [{\"Action\": \"CREATE\", \"ResourceRecordSet\": {\"Name\": \"$DOMAIN.\", \"Type\": \"A\", \"TTL\": 300,
      \"ResourceRecords\": [{\"Value\": \"$eip\"}]}}]}" --query ChangeInfo.Id --output text)
  aws route53 wait resource-record-sets-changed --id "$change"
fi

say "done: $instance at $eip, $DOMAIN"
echo "ssh: ssh -i $KEY_FILE ubuntu@$eip"
echo "next: ESTATE_DOMAIN=$DOMAIN ESTATE_KEY=$KEY_FILE $HERE/update.sh <estate-linux-arm64 binary>"
