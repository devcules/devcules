#!/usr/bin/env bash
# M0.5 smoke test against an already-up compose stack. No sleeps:
# `docker compose up -d --wait` gates on health before this runs.
set -euo pipefail

COMPOSE="docker compose -f deploy/compose.yaml"
URL="http://127.0.0.1:7659"

fail() {
  echo "SMOKE FAIL: $*"
  $COMPOSE logs --no-color serve runner || true
  exit 1
}

[ "$(curl -fsS "$URL/healthz")" = "ok" ] || fail "/healthz"
[ "$(curl -fsS "$URL/readyz")" = "ok" ] || fail "/readyz"
[ "$($COMPOSE exec -T serve id -u)" = "10001" ] || fail "serve is not uid 10001"
for svc in serve runner; do
  cid=$($COMPOSE ps -q "$svc")
  [ -n "$cid" ] || fail "$svc is not running"
  [ "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$cid")" = "true" ] || fail "$svc root filesystem is writable"
  [ "$(docker inspect -f '{{.State.Running}}' "$cid")" = "true" ] || fail "$svc is not running"
done
[ "$($COMPOSE exec -T runner id -u)" = "10001" ] || fail "runner is not uid 10001"
if $COMPOSE exec -T runner printenv DEVCULES_VAULT_KEY >/dev/null 2>&1; then
  fail "runner has a vault key"
fi
[ "$($COMPOSE port serve 7659)" = "127.0.0.1:7659" ] || fail "port not bound to 127.0.0.1 only"

serve_id=$($COMPOSE ps -q serve)
start=$SECONDS
$COMPOSE stop -t 20 serve >/dev/null
elapsed=$((SECONDS - start))
[ "$elapsed" -lt 20 ] || fail "drain took ${elapsed}s (limit 20s)"
# 0 means serve handled SIGTERM; 143 or 137 means it was killed instead.
[ "$(docker inspect -f '{{.State.ExitCode}}' "$serve_id")" = "0" ] || fail "serve did not exit 0 on SIGTERM"

echo "SMOKE OK"
