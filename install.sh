#!/usr/bin/env bash
# SWIM — first installation on a fresh Ubuntu 24.04 LTS server.
#
#   git clone <this repository> /opt/swim && cd /opt/swim && ./install.sh
#
# Safe to run again: every step skips what already exists. It never overwrites
# builder-infra/.env, the TLS certificate or the signing keys.
#
# What it does
#   1. installs the host packages (Docker, KVM/libvirt, OVMF, swtpm, wimlib …)
#   2. prepares /srv/swim, generates the secrets (.env), a self-signed TLS certificate
#      and the firewall rules                       → builder-infra/scripts/setup-host.sh
#   3. builds and starts the stack (go vet + go test run inside the image build)
#   4. builds and publishes the Windows agent      → builder-infra/scripts/build-agent.sh
#   5. builds SWIM WinPE if a Windows ISO is already in /srv/swim/vms/iso/
set -euo pipefail
ROOT=$(cd "$(dirname "$0")" && pwd)
DATA=${SWIM_DATA:-/srv/swim}
say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

[ "$(id -u)" -ne 0 ] || { echo "run as a normal user with sudo rights, not as root"; exit 1; }
. /etc/os-release
[ "${ID:-}" = ubuntu ] || echo "warning: tested on Ubuntu 24.04 only (this is ${PRETTY_NAME:-unknown})"
if ! grep -Eq '(vmx|svm)' /proc/cpuinfo; then
  echo "warning: this CPU does not expose hardware virtualisation (vmx/svm)."
  echo "         The web console and agents will work, but the Builder (Golden Master VM, image capture) will not."
fi

say "1/5 host packages"
sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  docker.io docker-compose-v2 docker-buildx \
  qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-clients virtinst ovmf swtpm swtpm-tools bridge-utils \
  wimtools ntfs-3g libhivex-bin libwin-hivex-perl genisoimage xorriso dosfstools gdisk parted \
  ufw openssl rsync curl
sudo systemctl enable --now docker libvirtd
sudo usermod -aG docker,libvirt,kvm "$USER"
sudo virsh -c qemu:///system net-autostart default >/dev/null 2>&1 || true
sudo virsh -c qemu:///system net-start default >/dev/null 2>&1 || true

say "2/5 data folders, secrets, certificate, firewall"
bash "$ROOT/builder-infra/scripts/setup-host.sh"

say "3/5 build and start the stack"
cd "$ROOT/builder-infra"
sudo docker compose up -d --build
printf 'waiting for the API '
for _ in $(seq 1 60); do
  if curl -skf -o /dev/null https://127.0.0.1/v1/health; then echo ' ok'; break; fi
  printf '.'; sleep 2
done
curl -skf -o /dev/null https://127.0.0.1/v1/health || { echo; echo "the API did not come up — see: sudo docker compose logs backend"; exit 1; }

say "4/5 Windows agent"
bash "$ROOT/builder-infra/scripts/build-agent.sh"

say "5/5 SWIM WinPE"
ISO=$(ls "$DATA"/vms/iso/*.iso 2>/dev/null | head -1 || true)
if [ -n "$ISO" ]; then
  bash "$ROOT/builder-infra/scripts/build-winpe.sh" "$ISO"
else
  echo "skipped: no Windows ISO in $DATA/vms/iso/ yet."
  echo "copy one there (scp) and run:  ./builder-infra/scripts/build-winpe.sh $DATA/vms/iso/<file>.iso"
fi

IP=$(hostname -I | awk '{print $1}')
cat <<DONE

SWIM is running:  https://$IP/

First sign-in (no accounts exist yet): use an admin token from
  $ROOT/builder-infra/.env   (SWIM_ADMIN_TOKENS=name:token,…)   — show it with:
  sudo grep ^SWIM_ADMIN_TOKENS $ROOT/builder-infra/.env
Then create the first administrator account (password + authenticator app):
  web console → ตั้งค่า → ผู้ดูแลระบบ → เพิ่มผู้ดูแล
  or:  sudo docker exec swim-backend-1 swim-server user add <username>

Next steps and day-to-day operation: docs/INSTALL.md and docs/OPERATIONS.md
(log out and in again once so your user picks up the docker / libvirt groups)
DONE
