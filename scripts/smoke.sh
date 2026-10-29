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

# M2.3 router: recreate serve with a provider key and the Meta base URL
# pointed at the mock provider (`devcules mockllm`, loopback inside the
# serve container), mint a launch token, and prove a fake AWS key
# leaves serve masked and comes back restored, streamed or not.
META_API_KEY=mock-smoke-key DEVCULES_META_BASE_URL=http://127.0.0.1:7701 DEVCULES_PUBLIC_URL=http://localhost:7659 \
  $COMPOSE up -d --wait serve >/dev/null || fail "recreate serve with the mock provider"
$COMPOSE exec -d serve devcules mockllm || fail "start the mock provider"
token_body=$(curl -fsS -b "$jar2" -H 'Content-Type: application/json' \
  -d '{"label":"smoke","provider":"meta","ttlMinutes":10}' "$URL/api/launch-tokens") || fail "POST /api/launch-tokens"
llm_token=$(printf '%s' "$token_body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])') || fail "no token in $token_body"
llm_id=$(printf '%s' "$token_body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["launch"]["id"])')
start=$SECONDS
until [ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $llm_token" "$URL/llm/meta/v1/models")" = "200" ]; do
  [ $((SECONDS - start)) -lt 20 ] || fail "the router never reached the mock provider"
  sleep 0.5
done
chat='{"model":"mock-model","messages":[{"role":"user","content":"key '"$fake_key"' ok"}]}'
reply=$(curl -fsS -H "Authorization: Bearer $llm_token" -H 'Content-Type: application/json' -d "$chat" "$URL/llm/meta/v1/chat/completions") || fail "router chat call"
case "$reply" in
  *"echo: key $fake_key ok"*) ;;
  *) fail "router reply not restored: $reply" ;;
esac
streamed=$(curl -fsSN -H "Authorization: Bearer $llm_token" -H 'Content-Type: application/json' \
  -d '{"model":"mock-model","stream":true,"messages":[{"role":"user","content":"key '"$fake_key"' ok"}]}' \
  "$URL/llm/meta/v1/chat/completions" | python3 -c '
import json, sys
out = ""
for line in sys.stdin:
    line = line.strip()
    if line.startswith("data: ") and line != "data: [DONE]":
        for c in json.loads(line[6:])["choices"]:
            out += c["delta"].get("content") or ""
print(out)') || fail "router stream call"
[ "$streamed" = "echo: key $fake_key ok" ] || fail "streamed reply not restored: $streamed"
seen=$($COMPOSE exec -T serve cat /tmp/mockllm.log) || fail "read the mock provider log"
case "$seen" in
  *"$fake_key"*) fail "the provider saw the key unmasked" ;;
  *REDACTED_SECRET_*) ;;
  *) fail "the provider saw no placeholder: $seen" ;;
esac
if $COMPOSE logs --no-color serve | grep -qF -e "$fake_key" -e "$llm_token" -e mock-smoke-key; then
  fail "serve logs leak a key or a launch token"
fi
[ "$(curl -sS -o /dev/null -w '%{http_code}' -b "$jar2" -X DELETE "$URL/api/launch-tokens/$llm_id")" = "204" ] || fail "revoke launch token"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $llm_token" "$URL/llm/meta/v1/models")" = "401" ] || fail "a revoked launch token still works"

# M2.4 audit and traces: the launch's calls are traced with the key as
# its placeholder, and the audit log holds the mint and the revoke in
# a chain both the API and `devcules audit verify` accept.
trace=$(curl -fsS -b "$jar2" "$URL/api/traces/$llm_id") || fail "GET /api/traces/{id}"
case "$trace" in
  *"$fake_key"*) fail "the trace holds the key unmasked" ;;
  *'"question":"key REDACTED_SECRET_'*) ;;
  *) fail "the trace has no masked question: $trace" ;;
esac
calls=$(printf '%s' "$trace" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["calls"]))')
[ "$calls" -ge 3 ] || fail "the trace holds $calls calls, want the models poll, the chat and the stream"
audit_log=$(curl -fsS -b "$jar2" "$URL/api/audit/export") || fail "GET /api/audit/export"
for action in auth.sign_in launch.mint trace.read launch.revoke audit.export; do
  case "$audit_log" in
    *"\"action\":\"$action\""*) ;;
    *) fail "the audit log has no $action" ;;
  esac
done
case "$audit_log" in
  *"$llm_token"*|*"$fake_key"*) fail "the audit log leaks a token or a key" ;;
esac
curl -fsS -b "$jar2" "$URL/api/audit/export?verify=1" | grep -q '"ok":true' || fail "the audit chain does not verify through the API"
$COMPOSE exec -T serve devcules audit verify | grep -q '^audit chain ok' || fail "devcules audit verify failed"

# M3.1 MCP server: a fresh launch token reaches /mcp in both protocol
# eras, check_redaction masks the key without sending it anywhere, the
# call is audited as mcp.call, and the logs hold neither.
mcp_body=$(curl -fsS -b "$jar2" -H 'Content-Type: application/json' \
  -d '{"label":"smoke mcp","ttlMinutes":10}' "$URL/api/launch-tokens") || fail "mint the MCP launch token"
mcp_token=$(printf '%s' "$mcp_body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
meta='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
mcp() { # method, name ("" for none), params without _meta
  curl -sS -H "Authorization: Bearer $mcp_token" -H 'Content-Type: application/json' \
    -H 'MCP-Protocol-Version: 2026-07-28' -H "Mcp-Method: $1" ${2:+-H "Mcp-Name: $2"} \
    -d '{"jsonrpc":"2.0","id":1,"method":"'"$1"'","params":{'"$3"'}}' "$URL/mcp"
}
mcp server/discover "" "$meta" | grep -q '"supportedVersions":\["2026-07-28"' || fail "MCP server/discover"
mcp tools/list "" "$meta" | grep -q '"name":"check_redaction"' || fail "MCP tools/list has no check_redaction"
checked=$(mcp tools/call check_redaction '"name":"check_redaction","arguments":{"text":"key '"$fake_key"'"},'"$meta") || fail "MCP tools/call"
case "$checked" in
  *"$fake_key"*) fail "check_redaction returned the key unmasked" ;;
  *'"text":"key REDACTED_SECRET_'*) ;;
  *) fail "check_redaction did not mask the key: $checked" ;;
esac
curl -fsS -H "Authorization: Bearer $mcp_token" -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' \
  "$URL/mcp" | grep -q '"protocolVersion":"2025-06-18"' || fail "MCP legacy initialize"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d '{}' "$URL/mcp")" = "401" ] || fail "/mcp without a launch token is not 401"
curl -fsS -b "$jar2" "$URL/api/audit/export" | grep -q '"action":"mcp.call","target":"check_redaction"' || fail "the MCP call is not audited"
if $COMPOSE logs --no-color serve | grep -qF -e "$fake_key" -e "$mcp_token"; then
  fail "serve logs leak the key or the MCP launch token"
fi

# M3.2 approvals: a launch on runner "smoke" in critical mode runs a
# plain command at once, waits on a force push until the owner allows
# it through the API, and gets a deny exit for a recursive delete. The
# hook runs in the runner container, as agents will. The audit has
# every step; neither it nor the logs hold the commands or the token.
curl -fsS -b "$jar2" -X PUT -H 'Content-Type: application/json' \
  -d '{"target":"runner:smoke","mode":"critical"}' "$URL/api/approvals/modes" >/dev/null || fail "set the smoke runner's access mode"
hook_body=$(curl -fsS -b "$jar2" -H 'Content-Type: application/json' \
  -d '{"label":"smoke hook","runner":"smoke","ttlMinutes":10}' "$URL/api/launch-tokens") || fail "mint the hook launch token"
hook_token=$(printf '%s' "$hook_body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
hook() { # command; prints the hook's stdout, returns its exit code
  printf '{"agent":"smoke","tool":"Bash","command":"%s"}' "$1" |
    $COMPOSE exec -T -e DEVCULES_LAUNCH_TOKEN="$hook_token" runner devcules hook 2>/dev/null
}
hook "ls -la" | grep -q '"decision":"allow"' || fail "a plain command was not allowed at once"
answer_next() { # decision: wait for the one pending approval and answer it
  local start=$SECONDS id=""
  while [ $((SECONDS - start)) -lt 30 ]; do
    id=$(curl -fsS -b "$jar2" "$URL/api/approvals?status=pending" |
      python3 -c 'import json,sys; a=json.load(sys.stdin)["approvals"]; print(a[0]["id"] if a else "")')
    [ -n "$id" ] && break
    sleep 0.5
  done
  [ -n "$id" ] || return 1
  curl -fsS -b "$jar2" -H 'Content-Type: application/json' -d '{"decision":"'"$1"'"}' "$URL/api/approvals/$id" >/dev/null
}
for case in "git push --force origin smoke-branch:allow:0" "rm -rf /workspace/smoke-dir:deny:2"; do
  cmd=${case%%:*}; rest=${case#*:}; want=${rest%%:*}; code=${rest#*:}
  out=$(mktemp)
  hook "$cmd" >"$out" & hook_pid=$!
  answer_next "$want" || fail "no pending approval for: $cmd"
  set +e; wait "$hook_pid"; got=$?; set -e
  [ "$got" = "$code" ] || fail "hook exit $got for '$cmd', want $code"
  grep -q "\"decision\":\"$want\"" "$out" || fail "hook printed $(cat "$out") for '$cmd'"
  rm -f "$out"
done
audit_log=$(curl -fsS -b "$jar2" "$URL/api/audit/export") || fail "GET /api/audit/export"
for action in approval.mode approval.ask approval.allow approval.deny; do
  case "$audit_log" in
    *"\"action\":\"$action\""*) ;;
    *) fail "the audit log has no $action" ;;
  esac
done
case "$audit_log" in
  *smoke-branch*|*smoke-dir*|*"$hook_token"*) fail "the audit log holds a command or the hook token" ;;
esac
if $COMPOSE logs --no-color serve runner | grep -qF -e smoke-branch -e smoke-dir -e "$hook_token"; then
  fail "the logs hold a command or the hook token"
fi
curl -fsS -b "$jar2" "$URL/api/push" | python3 -c 'import json,sys; k=json.load(sys.stdin)["publicKey"]; sys.exit(0 if len(k)==87 else 1)' || fail "GET /api/push has no 65-byte VAPID key"

serve_id=$($COMPOSE ps -q serve)
start=$SECONDS
$COMPOSE stop -t 20 serve >/dev/null
elapsed=$((SECONDS - start))
[ "$elapsed" -lt 20 ] || fail "drain took ${elapsed}s (limit 20s)"
# 0 means serve handled SIGTERM; 143 or 137 means it was killed instead.
[ "$(docker inspect -f '{{.State.ExitCode}}' "$serve_id")" = "0" ] || fail "serve did not exit 0 on SIGTERM"

echo "SMOKE OK"
