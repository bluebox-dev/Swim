#!/usr/bin/env bash
# End-to-end checks of the running stack. Run on the server from builder-infra/:
#   ./scripts/smoke-test.sh
# Creates test records (channel, site, host, image) — safe on a lab server only.
set -uo pipefail
HERE=$(cd "$(dirname "$0")/.." && pwd)
set -a; . "$HERE/.env"; set +a
BASE=${BASE:-https://127.0.0.1}
T1=$(echo "$SWIM_ADMIN_TOKENS" | tr ',' '\n' | sed -n 1p | cut -d: -f2)
T2=$(echo "$SWIM_ADMIN_TOKENS" | tr ',' '\n' | sed -n 2p | cut -d: -f2)
RUN=$(date +%s)
PASS=0; FAIL=0

c() { curl -sk -o /tmp/swim-body -w '%{http_code}' "$@"; }
check() { # name expected_code actual_code
  if [ "$2" = "$3" ]; then PASS=$((PASS+1)); printf '  ✔ %-58s %s\n' "$1" "$3"
  else FAIL=$((FAIL+1)); printf '  ✘ %-58s want %s got %s: %s\n' "$1" "$2" "$3" "$(head -c 300 /tmp/swim-body)"; fi
}
body() { jq -r "$1" /tmp/swim-body; }
A1=(-H "Authorization: Bearer $T1" -H 'Content-Type: application/json')
A2=(-H "Authorization: Bearer $T2" -H 'Content-Type: application/json')

echo "── system"
check "health" 200 "$(c $BASE/v1/health)"
echo "     status=$(body .status) db=$(body .checks.database) libvirt=$(body .checks.libvirt | cut -c1-60)"
check "http → https redirect" 301 "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1/)"
check "dashboard served" 200 "$(c $BASE/)"
check "/metrics not exposed via nginx" 404 "$(c $BASE/metrics)"
GA=$(curl -sk $BASE/guacamole/ | grep -oE 'src="[^"]+\.js"' | head -1 | cut -d'"' -f2)
check "guacamole JS asset served (not caught by UI cache rule)" 200 "$(c $BASE/guacamole/$GA)"
check "admin API without token → 401" 401 "$(c $BASE/v1/hosts)"
check "admin API with bad token → 401" 401 "$(c -H 'Authorization: Bearer nope' $BASE/v1/hosts)"
check "whoami admin1" 200 "$(c "${A1[@]}" $BASE/v1/whoami)"
check "keyset public" 200 "$(c $BASE/v1/agent/keyset)"
echo "     kid=$(body '.keys[0].kid')"
check "capacity" 200 "$(c "${A1[@]}" $BASE/v1/capacity/server)"
echo "     storage used=$(body '.storage.used_pct|floor')% level=$(body .storage.level)"
check "builder VMs (libvirt)" 200 "$(c "${A1[@]}" $BASE/v1/builder/vms)"
echo "     vms=$(jq -c '[.[]|.name+":"+.state]' /tmp/swim-body)"

echo "── channel / site"
check "create channel" 201 "$(c "${A1[@]}" -d "{\"name\":\"win11-x64-stable-$RUN\",\"os\":\"windows\",\"release\":\"24H2\",\"arch\":\"amd64\",\"edition\":\"Enterprise\",\"eol_date\":\"2026-10-13\"}" $BASE/v1/channels)"
CH=$(body .id)
check "duplicate channel name → 409" 409 "$(c "${A1[@]}" -d "{\"name\":\"win11-x64-stable-$RUN\",\"os\":\"windows\",\"release\":\"24H2\",\"arch\":\"amd64\",\"edition\":\"Enterprise\"}" $BASE/v1/channels)"
check "create site" 201 "$(c "${A1[@]}" -d "{\"name\":\"BKK-HQ-$RUN\"}" $BASE/v1/sites)"
SITE=$(body .id)

echo "── image import + two-person approve"
IMGDIR=$SWIM_DATA/storage/images/smoke
mkdir -p "$IMGDIR"
head -c 8388608 /dev/urandom > "$IMGDIR/v1-$RUN.wim"
head -c 8388608 /dev/urandom > "$IMGDIR/v2-$RUN.wim"
check "import v1 (admin1)" 201 "$(c "${A1[@]}" -d "{\"channel_id\":\"$CH\",\"version\":\"v1\",\"file\":\"smoke/v1-$RUN.wim\"}" $BASE/v1/images/import)"
IMG1=$(body .id)
[ "$(body .sha256)" = "$(sha256sum "$IMGDIR/v1-$RUN.wim" | cut -d' ' -f1)" ] && check "server sha256 matches file" ok ok || check "server sha256 matches file" ok bad
check "path traversal rejected" 422 "$(c "${A1[@]}" -d "{\"channel_id\":\"$CH\",\"version\":\"vx\",\"file\":\"../../keys/manifest-ed25519.wim\"}" $BASE/v1/images/import)"
check "builder approves own image → 409" 409 "$(c "${A1[@]}" -X POST $BASE/v1/images/$IMG1/approve)"
check "admin2 approves v1 → published" 200 "$(c "${A2[@]}" -X POST $BASE/v1/images/$IMG1/approve)"
check "import v2 (admin1)" 201 "$(c "${A1[@]}" -d "{\"channel_id\":\"$CH\",\"version\":\"v2\",\"file\":\"smoke/v2-$RUN.wim\"}" $BASE/v1/images/import)"
IMG2=$(body .id)
check "admin2 approves v2" 200 "$(c "${A2[@]}" -X POST $BASE/v1/images/$IMG2/approve)"
c "${A1[@]}" "$BASE/v1/images?channel=$CH" >/dev/null
check "v1 superseded, v2 published (one per channel)" "superseded,published" \
  "$(jq -r --arg a "$IMG1" --arg b "$IMG2" '[(.[]|select(.id==$a).status),(.[]|select(.id==$b).status)]|join(",")' /tmp/swim-body)"
check "re-approve published → 409" 409 "$(c "${A2[@]}" -X POST $BASE/v1/images/$IMG2/approve)"

echo "── enrollment"
check "create enrollment token (max 2 uses)" 201 "$(c "${A1[@]}" -d "{\"site_id\":\"$SITE\",\"max_uses\":2,\"ttl_hours\":1}" $BASE/v1/enrollment-tokens)"
ET=$(body .token)
UUID=$(cat /proc/sys/kernel/random/uuid)
XPUB=$(head -c 32 /dev/urandom | basenc --base64url | tr -d '=')
INV="{\"smbios_uuid\":\"$UUID\",\"serial\":\"SN$RUN\",\"vendor\":\"Dell Inc.\",\"model\":\"Latitude 5440\",\"hostname\":\"BKK-FIN-$RUN\",\"arch\":\"amd64\",\"firmware\":\"uefi\",\"secure_boot\":true,\"tpm_version\":\"2.0\"}"
check "enroll with bad token → 401" 401 "$(c -H 'Content-Type: application/json' -d "{\"token\":\"swim_et_bad\",\"inventory\":$INV}" $BASE/v1/agent/enroll)"
check "enroll host" 200 "$(c -H 'Content-Type: application/json' -d "{\"token\":\"$ET\",\"x25519_pub\":\"$XPUB\",\"inventory\":$INV}" $BASE/v1/agent/enroll)"
HOST=$(body .host_id); HT=$(body .host_token)
echo "     host=$HOST reenrolled=$(body .reenrolled)"
check "re-enroll same machine → same host_id" 200 "$(c -H 'Content-Type: application/json' -d "{\"token\":\"$ET\",\"inventory\":$INV}" $BASE/v1/agent/enroll)"
[ "$(body .host_id)" = "$HOST" ] && [ "$(body .reenrolled)" = "true" ] && check "  history kept (manual_install)" ok ok || check "  history kept" ok bad
HT=$(body .host_token)
check "token used up → 401" 401 "$(c -H 'Content-Type: application/json' -d "{\"token\":\"$ET\",\"inventory\":$INV}" $BASE/v1/agent/enroll)"

echo "── heartbeat + kill switch"
HB=(-H "Authorization: Bearer $HT" -H 'Content-Type: application/json' -d '{"agent_version":"0.1.0","svc_version":"0.1.0","state":"idle","stage_free":53687091200}')
check "heartbeat, no switch" 200 "$(c "${HB[@]}" $BASE/v1/agent/heartbeat)"
check "  halt=false" false "$(body .halt)"
check "set site kill switch without reason → 400" 400 "$(c "${A1[@]}" -X PUT -d '{}' $BASE/v1/kill-switches/site/$SITE)"
check "set site kill switch" 200 "$(c "${A1[@]}" -X PUT -d '{"reason":"smoke test"}' $BASE/v1/kill-switches/site/$SITE)"
c "${HB[@]}" $BASE/v1/agent/heartbeat >/dev/null
check "  heartbeat halt=true (site)" true "$(body .halt)"
check "clear site switch (one admin ok)" 200 "$(c "${A1[@]}" -X DELETE $BASE/v1/kill-switches/site/$SITE)"
c "${HB[@]}" $BASE/v1/agent/heartbeat >/dev/null
check "  heartbeat halt=false" false "$(body .halt)"
check "set GLOBAL kill switch" 200 "$(c "${A1[@]}" -X PUT -d '{"reason":"smoke test global"}' $BASE/v1/kill-switches/global/x)"
c "${HB[@]}" $BASE/v1/agent/heartbeat >/dev/null
check "  heartbeat halt=true (global)" true "$(body .halt)"
check "clear global alone → 403" 403 "$(c "${A1[@]}" -X DELETE $BASE/v1/kill-switches/global/x)"
check "clear global with own token as approver → 403" 403 "$(c "${A1[@]}" -H "X-Approver-Token: $T1" -X DELETE $BASE/v1/kill-switches/global/x)"
check "clear global with admin2 approval" 200 "$(c "${A1[@]}" -H "X-Approver-Token: $T2" -X DELETE $BASE/v1/kill-switches/global/x)"
c "${HB[@]}" $BASE/v1/agent/heartbeat >/dev/null
check "  heartbeat halt=false" false "$(body .halt)"
check "heartbeat bad host token → 401" 401 "$(c -H 'Authorization: Bearer swim_ht_bad' -d '{}' $BASE/v1/agent/heartbeat)"

echo "── audit log"
check "audit list" 200 "$(c "${A1[@]}" $BASE/v1/audit)"
echo "     entries=$(jq length /tmp/swim-body), latest=$(body '.[0].action')"
check "audit chain verifies" 200 "$(c "${A1[@]}" $BASE/v1/audit/verify)"
check "  ok=true" true "$(body .ok)"
cd "$HERE"
OUT=$(docker compose exec -T postgres psql -U swim_app -d swim -tAc "UPDATE audit_log SET actor='evil' WHERE id=1" 2>&1)
echo "$OUT" | grep -q "permission denied" && check "swim_app cannot UPDATE audit_log" denied denied || check "swim_app cannot UPDATE audit_log" denied "$OUT"
OUT=$(docker compose exec -T postgres psql -U swim_app -d swim -tAc "DELETE FROM audit_log" 2>&1)
echo "$OUT" | grep -q "permission denied" && check "swim_app cannot DELETE audit_log" denied denied || check "swim_app cannot DELETE audit_log" denied "$OUT"
# Tamper as the DB owner (simulates someone with direct DB access), expect verify to catch it, then restore.
PSQL_OWNER=(docker compose exec -T postgres psql -U swim_owner -d swim -tAc)
TID=$("${PSQL_OWNER[@]}" "SELECT min(id) FROM audit_log" | tr -d '[:space:]')
ORIG=$("${PSQL_OWNER[@]}" "SELECT actor FROM audit_log WHERE id=$TID" | tr -d '[:space:]')
"${PSQL_OWNER[@]}" "UPDATE audit_log SET actor='user:mallory' WHERE id=$TID" >/dev/null
c "${A1[@]}" $BASE/v1/audit/verify >/dev/null
check "tampered row detected by verify" "false@$TID" "$(body .ok)@$(body .broken_at)"
"${PSQL_OWNER[@]}" "UPDATE audit_log SET actor='$ORIG' WHERE id=$TID" >/dev/null
c "${A1[@]}" $BASE/v1/audit/verify >/dev/null
check "  chain valid again after restore" true "$(body .ok)"

rm -f "$IMGDIR"/*-"$RUN".wim
echo
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" = 0 ]
