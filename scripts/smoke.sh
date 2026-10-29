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

# M1.3 link: readyz is JSON with the linked runner count. Poll bounded
# (compose --wait only gates on /healthz); the runner links shortly
# after serve turns healthy.
poll_readyz() {
  local want=$1 timeout=$2 start=$SECONDS
  while [ $((SECONDS - start)) -lt "$timeout" ]; do
    if [ "$(curl -fsS "$URL/readyz")" = "{\"status\":\"ok\",\"runners_linked\":$want}" ]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}
poll_readyz 1 60 || fail "/readyz never showed runners_linked 1"

# The token file is minted 0400 and both /link and the file belong to
# uid 10001, so the read-only runner mount can read it.
[ "$($COMPOSE exec -T serve stat -c '%a %u' /link/runner.token)" = "400 10001" ] || fail "token file not 0400/uid 10001"
[ "$($COMPOSE exec -T serve stat -c '%u' /link)" = "10001" ] || fail "/link not owned by uid 10001"
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

# M2.1 vault: serve generated its key file 0600 in the data volume,
# opened the vault, and never logged the key itself.
[ "$($COMPOSE exec -T serve stat -c '%a %u' /data/vault.key)" = "600 10001" ] || fail "vault key file not 0600/uid 10001"
$COMPOSE logs --no-color serve | grep -q '"msg":"vault open"' || fail "serve did not open the vault"
vault_key=$($COMPOSE exec -T serve cat /data/vault.key | tr -d '\r\n')
if $COMPOSE logs --no-color serve | grep -qF "$vault_key"; then
  fail "serve logs leak the vault key"
fi
if $COMPOSE exec -T runner test -e /data/vault.key 2>/dev/null; then
  fail "runner can see the vault key file"
fi

# M1.1 sign-in flow: the bootstrap password comes from the serve logs
# (fresh volumes only). curl sends no Origin header, so the CSRF check
# lets it through like any non-browser client.
password="$(./scripts/get-admin-password.sh)" || fail "no bootstrap password in serve logs"
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT
[ "$(curl -sS -o /dev/null -w '%{http_code}' -c "$jar" -d "username=admin&password=$password" "$URL/auth/sign-in")" = "204" ] || fail "sign-in did not 204"
# The app shell is embedded: the entry point serves without a session.
curl -fsS -H 'Accept: text/html' "$URL/" | grep -q '<div id="root"></div>' || fail "app shell not served"
[ "$(curl -fsS -b "$jar" "$URL/auth/me")" = '{"username":"admin"}' ] || fail "/auth/me after sign-in"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -b "$jar" -X POST "$URL/auth/sign-out")" = "200" ] || fail "sign-out"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -b "$jar" "$URL/auth/me")" = "401" ] || fail "/auth/me after sign-out is not 401"

# M1.4 terminals: POST opens (201 with the id), GET lists it, DELETE
# closes it (204), and the list is empty again. A fresh sign-in keeps
# this independent of the sign-out above.
jar2=$(mktemp)
trap 'rm -f "$jar" "$jar2"' EXIT
[ "$(curl -sS -o /dev/null -w '%{http_code}' -c "$jar2" -d "username=admin&password=$password" "$URL/auth/sign-in")" = "204" ] || fail "sign-in for terminals did not 204"
open_body=$(curl -fsS -b "$jar2" -X POST "$URL/api/terminals") || fail "POST /api/terminals"
term_id=$(printf '%s' "$open_body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])') || fail "open body has no id: $open_body"
case "$(curl -fsS -b "$jar2" "$URL/api/terminals")" in
  '{"terminals":[{"term":"'"$term_id"'","created":'*'}]}') ;;
  *) fail "GET /api/terminals does not list $term_id" ;;
esac
[ "$(curl -sS -o /dev/null -w '%{http_code}' -b "$jar2" -X DELETE "$URL/api/terminals/$term_id")" = "204" ] || fail "DELETE /api/terminals/$term_id"
[ "$(curl -fsS -b "$jar2" "$URL/api/terminals")" = '{"terminals":[]}' ] || fail "GET /api/terminals not empty after delete"
[ "$(curl -sS -o /dev/null -w '%{http_code}' "$URL/api/terminals")" = "401" ] || fail "GET /api/terminals without session is not 401"

# M2.2 redaction: serve ran the self-test before listening, and the
# test box masks an AWS-shaped key (made up, assembled from parts so
# the repo scan stays clean) without echoing it.
$COMPOSE logs --no-color serve | grep -q '"msg":"redaction self-test passed"' || fail "serve did not log the redaction self-test"
fake_key="AKIA""Z7QWERTY4UIOP3XZ"
redacted=$(curl -fsS -b "$jar2" -H 'Content-Type: application/json' -d '{"text":"key '"$fake_key"'"}' "$URL/api/redact/test") || fail "POST /api/redact/test"
case "$redacted" in
  *"$fake_key"*) fail "redaction test echoed the key: $redacted" ;;
  *'"text":"key REDACTED_SECRET_'*'"rule":"aws-access-token"'*) ;;
  *) fail "redaction test did not mask the key: $redacted" ;;
esac

# M1.5 passkeys: without DEVCULES_PUBLIC_URL the stack reports
# disabled with a reason (password sign-in above already proved the
# fallback works).
case "$(curl -fsS "$URL/auth/passkeys/status")" in
  '{"enabled":false,"reason":"'?*'"}') ;;
  *) fail "passkey status without a public URL is not disabled with a reason" ;;
esac

# M1.3 serve restart: the runner's dialer backs off and relinks on its
# own; readyz returns to 1 with a fresh session underneath.
$COMPOSE restart serve >/dev/null
poll_readyz 1 90 || fail "/readyz never returned to runners_linked 1 after restart"

# M1.5 passkeys: with DEVCULES_PUBLIC_URL=http://localhost:7659 the
# status reports enabled. Recreating serve changes nothing else (the
# token and bootstrap stay on their volumes), and the stack is torn
# down below, so no restore is needed.
DEVCULES_PUBLIC_URL=http://localhost:7659 $COMPOSE up -d --wait serve >/dev/null || fail "recreate serve with a public URL"
[ "$(curl -fsS "$URL/auth/passkeys/status")" = '{"enabled":true}' ] || fail "passkey status with a localhost URL is not enabled"
poll_readyz 1 90 || fail "/readyz never returned to runners_linked 1 after the public URL recreate"

serve_id=$($COMPOSE ps -q serve)
start=$SECONDS
$COMPOSE stop -t 20 serve >/dev/null
elapsed=$((SECONDS - start))
[ "$elapsed" -lt 20 ] || fail "drain took ${elapsed}s (limit 20s)"
# 0 means serve handled SIGTERM; 143 or 137 means it was killed instead.
[ "$(docker inspect -f '{{.State.ExitCode}}' "$serve_id")" = "0" ] || fail "serve did not exit 0 on SIGTERM"

echo "SMOKE OK"
