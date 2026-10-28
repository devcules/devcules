#!/usr/bin/env bash
# Prints the bootstrap admin password from the serve container logs.
# It only exists on a fresh volume (first start creates the admin);
# on a reused volume this fails with next steps instead of an empty
# password.
set -euo pipefail

line=$(docker compose -f deploy/compose.yaml logs --no-color serve 2>/dev/null | awk '/shown once/{print $NF}' | tail -n 1)
if [ -z "$line" ]; then
  echo "get-admin-password: no bootstrap password in the serve logs (admin already exists? reset with 'docker compose -f deploy/compose.yaml down -v')" >&2
  exit 1
fi
printf '%s' "$line"
