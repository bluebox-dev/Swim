#!/usr/bin/env bash
# Build the SWIM WinPE — no Windows ADK needed: wimlib adds swim-pe.exe and
# winpeshl.ini (auto-start) to a stock WinPE. Base, in order of preference:
#   1. the Windows ISO's WinPE (sources/boot.wim index 1 + boot/boot.sdi)
#   2. a saved WinRE (/srv/swim/winpe-base/winre.wim + boot.sdi), e.g. copied from a
#      Golden Master's Recovery partition — kept outside VM storage on purpose
# Per-machine drivers are added later, per job, by the server.
#   ./build-winpe.sh [iso]      → /srv/swim/storage/winpe/<ver>/{boot.wim,boot.sdi} + winpe/latest.json
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd); ROOT=$(cd "$HERE/.." && pwd)
DATA=${SWIM_DATA:-/srv/swim}; ISO=${1:-$DATA/vms/iso/Windows11.iso}
VER=pe-$(grep -E '^const Version' "$ROOT/agent/internal/agent/state.go" | cut -d'"' -f2)
# fingerprint of the code that goes into swim-pe.exe; build-agent.sh rebuilds WinPE when it changes
SRC=$(cd "$ROOT" && find pe pkg -type f -name '*.go' | sort | xargs sha256sum | cat - go.mod go.sum | sha256sum | cut -c1-16)
OUT=$DATA/storage/winpe/$VER; WORK=$(mktemp -d)
trap 'sudo umount "$WORK/iso" 2>/dev/null || true; rm -rf "$WORK"' EXIT

echo "== build swim-pe.exe"
sudo docker run --rm -v "$ROOT":/src -v swim-gocache:/root/.cache -w /src -e CGO_ENABLED=0 -e GOOS=windows -e GOARCH=amd64 \
  golang:1.27-bookworm go build -trimpath -ldflags='-s -w' -o .build/swim-pe.exe ./pe/cmd/swim-pe
BASE=${WINPE_BASE:-$DATA/winpe-base}
if [ -f "$ISO" ]; then
  mkdir -p "$WORK/iso"; sudo mount -o loop,ro "$ISO" "$WORK/iso"
  SRC_WIM="$WORK/iso/sources/boot.wim"; SRC_SDI="$WORK/iso/boot/boot.sdi"; echo "== base: WinPE from $(basename "$ISO")"
elif [ -f "$BASE/winre.wim" ]; then
  SRC_WIM="$BASE/winre.wim"; SRC_SDI="$BASE/boot.sdi"; echo "== base: WinRE from $BASE (no ISO present)"
else
  echo "no Windows ISO and no $BASE/winre.wim — cannot build WinPE"; exit 1
fi
wimlib-imagex export "$SRC_WIM" 1 "$WORK/boot.wim" --boot >/dev/null
printf '[LaunchApps]\r\n%%SYSTEMROOT%%\\System32\\swim-pe.exe\r\n' > "$WORK/winpeshl.ini"
wimlib-imagex update "$WORK/boot.wim" 1 >/dev/null <<CMDS
add $ROOT/.build/swim-pe.exe /Windows/System32/swim-pe.exe
add $WORK/winpeshl.ini /Windows/System32/winpeshl.ini
CMDS
sudo install -d -m 755 "$OUT"
sudo install -m 644 "$WORK/boot.wim" "$OUT/boot.wim"
sudo install -m 644 "$SRC_SDI" "$OUT/boot.sdi"
# agents ≥ 0.5.11 put swim-pe.exe into the machine's own WinRE (see agent winpe_local_windows.go)
sudo install -m 644 "$ROOT/.build/swim-pe.exe" "$OUT/swim-pe.exe"
ref() { printf '{"path":"%s","sha256":"%s","size":%s}' "winpe/$VER/$1" "$(sha256sum "$OUT/$1" | cut -d' ' -f1)" "$(stat -c %s "$OUT/$1")"; }
printf '{"version":"%s","boot_wim":%s,"boot_sdi":%s,"swim_pe":%s,"src":"%s","built_at":"%s"}\n' "$VER" "$(ref boot.wim)" "$(ref boot.sdi)" "$(ref swim-pe.exe)" "$SRC" "$(date -u +%FT%TZ)" \
  | sudo tee "$DATA/storage/winpe/latest.json" >/dev/null
echo "published $VER ($(du -h "$OUT/boot.wim" | cut -f1) boot.wim)"
