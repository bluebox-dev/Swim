#!/usr/bin/env bash
# Define a Golden Master VM shell (spec 01 §3 / 02): OVMF + Secure Boot + swtpm TPM 2.0,
# thin qcow2, VNC on 127.0.0.1 only (reached through guacd). Attach a Windows ISO later:
#   ISO=/srv/swim/vms/iso/Win11_24H2_x64.iso ./create-golden-master.sh gm-win11-24h2
set -euo pipefail
NAME=${1:-gm-win11-24h2}
DATA=${SWIM_DATA:-/srv/swim}
DISK=$DATA/vms/$NAME.qcow2
RAM_MB=${RAM_MB:-8192}
VCPUS=${VCPUS:-4}
DISK_GB=${DISK_GB:-100}

if virsh -c qemu:///system dominfo "$NAME" >/dev/null 2>&1; then
  echo "$NAME already defined"; exit 0
fi

MEDIA=(--import)
if [ -n "${ISO:-}" ]; then MEDIA=(--cdrom "$ISO"); fi

sudo virt-install --connect qemu:///system \
  --name "$NAME" --osinfo win11 \
  --memory "$RAM_MB" --vcpus "$VCPUS" --cpu host-passthrough \
  --machine q35 \
  --boot uefi,firmware.feature0.name=secure-boot,firmware.feature0.enabled=yes,firmware.feature1.name=enrolled-keys,firmware.feature1.enabled=yes \
  --tpm backend.type=emulator,backend.version=2.0,model=tpm-crb \
  --disk path="$DISK",size="$DISK_GB",format=qcow2,bus=sata,discard=unmap \
  --network network=default,model=e1000e \
  --graphics vnc,listen=127.0.0.1 \
  --video qxl \
  "${MEDIA[@]}" \
  --noautoconsole --noreboot
# Persistent boot order: DVD first (setup asks "press any key", times out to disk), then disk.
# virt-install leaves only <boot dev='hd'/> in the saved config.
sudo virt-xml --connect qemu:///system "$NAME" --edit --boot cdrom,hd >/dev/null
echo "defined $NAME ($RAM_MB MB, $VCPUS vCPU, ${DISK_GB}G thin disk)"
