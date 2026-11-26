#!/usr/bin/env bash
# Build swim-agent for Windows (and Linux, for tests), publish it to <storage>/agent/,
# and refresh latest.json + install.ps1 served at https://<server>/dl/agent/.
# Run on the server from builder-infra/.
set -euo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd); ROOT=$(cd "$HERE/.." && pwd)
DATA=${SWIM_DATA:-/srv/swim}; OUT=$DATA/storage/agent
VER=$(grep -E '^const Version' "$ROOT/agent/internal/agent/state.go" | cut -d'"' -f2)
GO="sudo docker run --rm -v $ROOT:/src -v swim-gocache:/root/.cache -w /src -e CGO_ENABLED=0 golang:1.24-bookworm"

echo "== vet + test"
$GO sh -c 'go vet ./agent/... && GOOS=windows go vet ./agent/... && go test ./agent/...'
echo "== build $VER"
$GO sh -c "GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o .build/swim-agent-$VER-windows-amd64.exe ./agent/cmd/swim-agent && \
           GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o .build/swim-agent-$VER-linux-amd64 ./agent/cmd/swim-agent"

sudo install -d -m 755 "$OUT"
FILE=swim-agent-$VER-windows-amd64.exe
sudo install -m 644 "$ROOT/.build/$FILE" "$OUT/$FILE"
sudo install -m 755 "$ROOT/.build/swim-agent-$VER-linux-amd64" "$OUT/swim-agent-$VER-linux-amd64"
sudo install -m 644 "$ROOT/agent/install/install.ps1" "$OUT/install.ps1"
sudo install -m 644 "$ROOT/agent/install/Set-SwimTrust.ps1" "$OUT/Set-SwimTrust.ps1"
# public server certificate, for IT to import via GPO (Trusted Root) — not a secret
sudo openssl x509 -in "$DATA/certs/server.crt" -outform DER -out "$OUT/swim-server.cer"
SUM=$(sha256sum "$OUT/$FILE" | cut -d' ' -f1)
printf '{"version":"%s","file":"%s","sha256":"%s","size_bytes":%s,"built_at":"%s"}\n' \
  "$VER" "$FILE" "$SUM" "$(stat -c %s "$OUT/$FILE")" "$(date -u +%FT%TZ)" | sudo tee "$OUT/latest.json" >/dev/null
echo "published $FILE ($SUM)"
