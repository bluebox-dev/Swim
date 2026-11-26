#!/usr/bin/env bash
# One-time host preparation for the SWIM server (idempotent). Run as a sudoer.
set -euo pipefail
DATA=${SWIM_DATA:-/srv/swim}
HERE=$(cd "$(dirname "$0")/.." && pwd)     # builder-infra/

sudo mkdir -p "$DATA"/{storage/{images,winpe,drivers,agent,logs},keys,certs,vms/{nvram,iso},backups,run,audit-archive,plugins,winpe-base}
sudo chmod 750 "$DATA/audit-archive" "$DATA/plugins"
sudo chown -R "$(id -u):$(id -g)" "$DATA"/{storage,certs,backups}
sudo chmod 700 "$DATA/keys"

# Secrets → builder-infra/.env (0600, never committed)
if [ ! -f "$HERE/.env" ]; then
  umask 077
  cat > "$HERE/.env" <<ENV
SWIM_DATA=$DATA
PG_OWNER_PASSWORD=$(openssl rand -hex 24)
PG_APP_PASSWORD=$(openssl rand -hex 24)
# two operators so two-person rules can be exercised before SSO exists
SWIM_ADMIN_TOKENS=admin1:$(openssl rand -hex 24),admin2:$(openssl rand -hex 24)
ENV
  echo "created $HERE/.env"
fi

# Added after the first release: Guacamole JSON-auth shared secret (16 bytes hex)
if ! grep -q '^GUAC_JSON_SECRET=' "$HERE/.env"; then
  echo "GUAC_JSON_SECRET=$(openssl rand -hex 16)" >> "$HERE/.env"
  echo "added GUAC_JSON_SECRET"
fi

# nbd for WIM capture from qcow2 (builder container)
echo nbd | sudo tee /etc/modules-load.d/swim-nbd.conf >/dev/null
echo "options nbd max_part=16" | sudo tee /etc/modprobe.d/swim-nbd.conf >/dev/null
sudo modprobe nbd max_part=16
sudo mkdir -p "$DATA/vms/snapshots"

# Self-signed TLS until the step-ca intermediate exists (spec 08 PKI)
if [ ! -f "$DATA/certs/server.crt" ]; then
  IP=$(hostname -I | awk '{print $1}')
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 825 \
    -keyout "$DATA/certs/server.key" -out "$DATA/certs/server.crt" \
    -subj "/CN=swim.corp.local" \
    -addext "subjectAltName=DNS:swim.corp.local,DNS:$(hostname),IP:$IP" 2>/dev/null
  chmod 640 "$DATA/certs/server.key"
  echo "created self-signed cert for $IP"
fi

# Firewall: 443/80 + SSH only (spec 08 hardening)
if command -v ufw >/dev/null; then
  sudo ufw allow 22/tcp >/dev/null
  sudo ufw allow 80/tcp >/dev/null
  sudo ufw allow 443/tcp >/dev/null
  sudo ufw --force enable >/dev/null
  echo "ufw: $(sudo ufw status | head -1)"
fi
