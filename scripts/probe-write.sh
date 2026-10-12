#!/usr/bin/env bash
# Can the scoped test token create a domain, and then read and delete it?
#
# THIS SCRIPT WRITES. It sends, in this order:
#   1. GET  /domain/read/<all-zero uuid>      what a missing id answers: 403 or 404
#   2. POST /domain/create/  [{"name":"tfacc-probe-<random>.<base>"}]
#   only if 2 answered 2xx and returned an id and a name starting with tfacc-probe-:
#   3. GET  /domain/read/<that id>            until ready, at most 2 minutes
#   4. POST /domain/delete/  [{"id":"<that id>"}]
#   5. GET  /domain/read/<that id>            until it stops answering 200, at most 3 minutes
#
# The only object it can delete is the one it created in the same run. Nothing
# else is addressed by a write. It stops at the first 429 or 5xx.
#
# Prints status codes, field names, state values and masked error bodies. Writes
# the same to explore-out/probe-write.txt (git-ignored).
#
#   OPALSTACK_TEST_DOMAIN_BASE   the base domain for tests; the name is not in this repository
#
# The token is read from pass (or OPALSTACK_TOKEN) and handed to curl on stdin.
#
# Usage: scripts/probe-write.sh
set -euo pipefail

readonly API="https://my.opalstack.com/api/v1"
readonly PASS_PATH="Opalstack/tokens/tfacc"
readonly ZERO_UUID="00000000-0000-0000-0000-000000000000"
readonly UUID_RE='[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}'

: "${OPALSTACK_TEST_DOMAIN_BASE:?set it to the base domain for tests}"
[[ "$OPALSTACK_TEST_DOMAIN_BASE" =~ ^[a-z0-9.-]+\.[a-z]+$ ]] || { echo "FATAL: the base domain has an unexpected shape" >&2; exit 1; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUT="$ROOT/explore-out/probe-write.txt"
mkdir -p "$(dirname "$OUT")"

TOKEN="${OPALSTACK_TOKEN:-}"
if [[ -z "$TOKEN" ]]; then
  TOKEN="$(pass show "$PASS_PATH" | head -n1)" || { echo "FATAL: cannot read token from pass" >&2; exit 1; }
fi
TOKEN="${TOKEN//[[:space:]]/}"
[[ "$TOKEN" =~ ^[A-Za-z0-9_-]{20,}$ ]] || { echo "FATAL: token has an unexpected shape (length ${#TOKEN})" >&2; exit 1; }

TMP="$(mktemp -d -p "${XDG_RUNTIME_DIR:-/tmp}" opal-probe.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
readonly BODY="$TMP/body"

# call <path> [json-body]  ->  prints the status code; body in $BODY. A second
# argument makes it a POST.
call() {
  local code args=()
  if [[ $# -eq 2 ]]; then args=(-H "Content-Type: application/json" --data "$2"); fi
  sleep 0.5
  code="$(curl -sS --max-time 60 -o "$BODY" -w '%{http_code}' \
    -H "Accept: application/json" "${args[@]}" -K - "$API$1" <<<"header = \"Authorization: Token $TOKEN\"")"
  if [[ "$code" == "429" || "$code" == 5* ]]; then
    echo "FATAL: $1 answered $code; stopping instead of sending more" | tee -a "$OUT" >&2
    exit 1
  fi
  printf '%s' "$code"
}

# The body with UUIDs and the base domain masked, cut short.
masked() {
  head -c 400 "$BODY" | tr '\n' ' ' | sed -E "s/$UUID_RE/<uuid>/g; s/${OPALSTACK_TEST_DOMAIN_BASE//./\\.}/<base>/g"
}
say() { echo "$*" | tee -a "$OUT"; }

: >"$OUT"
# No pipe from /dev/urandom here: under pipefail the reader closing early kills the script.
printf -v SUFFIX '%06x' $(((RANDOM << 15 | RANDOM) & 0xffffff))
NAME="tfacc-probe-$SUFFIX.$OPALSTACK_TEST_DOMAIN_BASE"

code="$(call "/domain/read/$ZERO_UUID")"
say "read missing id      $code  $(masked)"

code="$(call "/domain/create/" "$(jq -cn --arg n "$NAME" '[{name: $n}]')")"
if [[ "$code" != 2* ]]; then
  say "create               $code  $(masked)"
  say "the create was refused; nothing was created and nothing else was sent"
  exit 0
fi
ID="$(jq -r '.[0].id // empty' "$BODY")"
GOT="$(jq -r '.[0].name // empty' "$BODY")"
say "create               $code  $(jq -c '.[0] | {fields: keys, state, ready, delegated_dns_zone, is_valid_hostname}' "$BODY")"
if [[ ! "$ID" =~ ^$UUID_RE$ || "$GOT" != "$NAME" ]]; then
  say "FATAL: the create did not return the id and name expected; nothing is deleted. Look for a tfacc-probe- domain by hand."
  exit 1
fi

for i in $(seq 1 24); do
  code="$(call "/domain/read/$ID")"
  state="$(jq -r '"\(.state) ready=\(.ready)"' "$BODY" 2>/dev/null || masked)"
  say "read own, try $i      $code  $state"
  if [[ "$code" != 2* || "$state" == *"ready=true" ]]; then break; fi
  sleep 5
done

code="$(call "/domain/delete/" "$(jq -cn --arg id "$ID" '[{id: $id}]')")"
say "delete               $code  $(masked)"
if [[ "$code" != 2* ]]; then
  say "FATAL: the delete was refused. A domain named tfacc-probe-... under the base domain is left behind."
  exit 1
fi

for i in $(seq 1 36); do
  code="$(call "/domain/read/$ID")"
  if [[ "$code" != "200" ]]; then
    say "read after delete    $code  $(masked)  (after $i reads)"
    exit 0
  fi
  say "read after delete    $code  $(jq -r '.state' "$BODY" 2>/dev/null)"
  sleep 5
done
say "FATAL: the domain still answered 200 three minutes after its delete was queued"
exit 1
