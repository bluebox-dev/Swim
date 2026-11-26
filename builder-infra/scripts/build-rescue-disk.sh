#!/usr/bin/env bash
# SWIM rescue boot disk — recovers a machine that is stuck in an older SWIM WinPE after a
# reimage failed past the point of no return (its old Windows is already gone).
#
#   ./build-rescue-disk.sh <job_id> [windows-image.wim]
#
# The disk boots this job's WinPE (it carries the machine's own storage/network drivers)
# with the CURRENT swim-pe.exe. swim-pe finds the staging partition, sees the job and
# waits for the admin's "retry" (web console → machine → job → ลองใหม่); it then downloads
# the chosen image plus a refreshed WinPE to the staging partition, reboots, and the
# machine continues from its own disk.
#
# Attach the .img as an extra disk (KubeVirt: virtctl image-upload, then a disk without
# bootOrder) and choose it ONCE from the firmware boot menu (Esc / F2 at power-on).
#
# UEFI boot files (bootmgfw.efi, the WinPE media BCD, boot.sdi) come from a Windows image
# captured WITHOUT CompactOS files (default: newest published one with wof_files = 0) and
# are cached in /srv/swim/storage/rescue/bootfiles after the first run.
#
# Output: /srv/swim/storage/rescue/swim-rescue-<job>.img (+ .sha256) — raw GPT disk image
set -euo pipefail
JOB=${1:?usage: build-rescue-disk.sh <job_id> [windows-image.wim]}
HERE=$(cd "$(dirname "$0")/.." && pwd)
DATA=${SWIM_DATA:-/srv/swim}
ST=$DATA/storage
[[ $JOB =~ ^job_[a-z0-9]+$ ]] || { echo "bad job id"; exit 1; }

LATEST=$(jq -r .boot_wim.path "$ST/winpe/latest.json")
PEVER=$(jq -r .version "$ST/winpe/latest.json")
JOBWIM=$ST/winpe/jobs/$JOB/boot.wim
[ -f "$JOBWIM" ] || { echo "no per-job WinPE ($JOBWIM) — using the stock build (no extra drivers)"; JOBWIM=$ST/$LATEST; }

# boot files are kept in $ST/rescue/bootfiles once extracted (they do not change per image)
CACHE=$ST/rescue/bootfiles
IMG=${2:-}
if [ -z "$IMG" ] && [ -s "$CACHE/efisys.bin" ]; then
  IMG=cache
elif [ -z "$IMG" ]; then
  REL=$(cd "$HERE" && sudo docker compose exec -T postgres psql -U swim_owner -d swim -tAc \
    "SELECT file_path FROM images WHERE status='published' AND wof_files=0 ORDER BY published_at DESC NULLS LAST LIMIT 1")
  [ -n "$REL" ] || { echo "no published image without CompactOS files yet — pass one explicitly"; exit 1; }
  IMG=$ST/$REL
fi
echo "== job WinPE: $JOBWIM (+ swim-pe from $PEVER)"
echo "== boot files from: $([ "$IMG" = cache ] && echo "$CACHE" || echo "$IMG")"

WORK=$(sudo mktemp -d "$DATA/tmp-rescue-XXXX")
trap 'sudo rm -rf "$WORK" || true' EXIT
sudo mkdir -p "$WORK"/iso/{efi/boot,efi/microsoft/boot,boot,sources} "$WORK/pe" "$WORK/bf"

# 1. WinPE: the job's boot.wim (drivers) with the current swim-pe.exe
sudo cp "$JOBWIM" "$WORK/iso/sources/boot.wim"
sudo wimlib-imagex extract "$ST/$LATEST" 1 /Windows/System32/swim-pe.exe --dest-dir="$WORK/pe" --no-acls >/dev/null
echo "add $WORK/pe/swim-pe.exe /Windows/System32/swim-pe.exe" | sudo wimlib-imagex update "$WORK/iso/sources/boot.wim" 1 >/dev/null

# 2. UEFI boot files (must be real files, not CompactOS reparse points)
if [ "$IMG" = cache ]; then
  sudo cp "$CACHE"/{bootmgfw.efi,BCD,boot.sdi,efisys.bin} "$WORK/bf/"
else
  sudo wimlib-imagex extract "$IMG" 1 /Windows/Boot/EFI/bootmgfw.efi /Windows/Boot/DVD/EFI/BCD /Windows/Boot/DVD/EFI/boot.sdi \
    /Windows/Boot/DVD/EFI/en-US/efisys.bin --dest-dir="$WORK/bf" --no-acls --no-attributes >/dev/null
fi
for f in bootmgfw.efi BCD boot.sdi efisys.bin; do
  sz=$(sudo stat -c %s "$WORK/bf/$f" 2>/dev/null || echo 0)
  [ "$sz" -gt 4096 ] || { echo "$f missing or still CompactOS-compressed in $IMG (size $sz)"; exit 1; }
done
# the media BCD must boot [boot]\sources\boot.wim from a ramdisk (the path sits UTF-16 inside a binary device element)
if ! sudo hivexregedit --export --unsafe-printable-strings "$WORK/bf/BCD" '\' 2>/dev/null | python3 -c '
import re, sys
t = sys.stdin.read()
blobs = [bytes.fromhex(re.sub(r"[^0-9a-f]", "", b)) for b in re.findall(r"hex(?:\(\d+\))?:([0-9a-f,\\\s]+)", t)]
ok = any("sources\\boot.wim" in x[o:].decode("utf-16le", "ignore").lower() for x in blobs for o in (0, 1))
sys.exit(0 if ok else 1)'; then
  echo "BCD does not boot \\sources\\boot.wim — unexpected template"; exit 1
fi
sudo mkdir -p "$CACHE" && sudo cp "$WORK/bf"/{bootmgfw.efi,BCD,boot.sdi,efisys.bin} "$CACHE/"
# Secure Boot: bootmgr also needs the trust list (boot.stl), CI policies and its MUI/fonts —
# the whole \EFI\Microsoft\Boot folder of an installed Windows' EFI partition (cached as bootfiles/esp,
# taken once from the Golden Master's ESP, which is plain FAT), minus that machine's own BCD.
if [ -s "$CACHE/esp/boot.stl" ]; then
  sudo cp -r "$CACHE/esp/." "$WORK/iso/efi/microsoft/boot/"
  sudo rm -f "$WORK/iso/efi/microsoft/boot/"{BCD,BCD.LOG,BCD.LOG1,BCD.LOG2,BOOTSTAT.DAT}
  sudo cp "$CACHE/esp/bootmgfw.efi" "$WORK/bf/bootmgfw.efi"
else
  echo "WARNING: $CACHE/esp (boot.stl, CIPolicies…) missing — the disk will only boot with Secure Boot off"
fi
sudo cp "$WORK/bf/bootmgfw.efi" "$WORK/iso/efi/boot/bootx64.efi"
# efisys.bin's CD loader (cdboot) chain-loads \bootmgr.efi from the media root
sudo cp "$WORK/bf/bootmgfw.efi" "$WORK/iso/bootmgr.efi"
sudo cp "$WORK/bf/BCD" "$WORK/iso/efi/microsoft/boot/bcd"
sudo cp "$WORK/bf/boot.sdi" "$WORK/iso/boot/boot.sdi"
sudo cp "$WORK/bf/efisys.bin" "$WORK/iso/efi/microsoft/boot/efisys.bin"
printf 'SWIM rescue for %s\r\nWinPE %s, built %s\r\n' "$JOB" "$PEVER" "$(date -u +%FT%TZ)" | sudo tee "$WORK/iso/swim-rescue.txt" >/dev/null

# 3. a FAT32 boot disk (what MakeWinPEMedia /UFD writes to a USB stick): every UEFI firmware
#    reads FAT, so no ISO/UDF quirks. Attach it as an extra disk and pick it ONCE in the firmware
#    boot menu; later reboots use the system disk again (its WinPE is replaced by the retry).
sudo mkdir -p "$ST/rescue"
OUT=$ST/rescue/swim-rescue-$JOB.img
MB=$(( $(sudo du -sm "$WORK/iso" | cut -f1) + 64 ))
sudo rm -f "$OUT.tmp"; sudo truncate -s ${MB}M "$OUT.tmp"
sudo sfdisk -q "$OUT.tmp" <<'PT'
label: gpt
start=2048, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B, name="SWIM RESCUE"
PT
LOOP=$(sudo losetup --find --show --partscan "$OUT.tmp")
# set -e also applies inside the EXIT trap: every step must tolerate "already done"
trap 'sudo umount "$WORK/mnt" 2>/dev/null || true; sudo losetup -d "$LOOP" 2>/dev/null || true; sudo rm -rf "$WORK" || true' EXIT
sudo mkfs.vfat -F 32 -n SWIMRESCUE "${LOOP}p1" >/dev/null
sudo mkdir -p "$WORK/mnt" && sudo mount "${LOOP}p1" "$WORK/mnt"
sudo cp -r "$WORK/iso/." "$WORK/mnt/"
sudo rm -f "$WORK/mnt/bootmgr.efi" "$WORK/mnt/efi/microsoft/boot/efisys.bin"   # CD-only pieces
sudo umount "$WORK/mnt"; sudo losetup -d "$LOOP"
sudo mv "$OUT.tmp" "$OUT"
( cd "$ST/rescue" && sha256sum "$(basename "$OUT")" | sudo tee "$(basename "$OUT").sha256" >/dev/null )
sudo rm -f "$ST/rescue/swim-rescue-$JOB.iso" "$ST/rescue/swim-rescue-$JOB.iso.sha256"
ls -la "$OUT"
echo "published $OUT (raw GPT disk, FAT32 EFI partition)"
