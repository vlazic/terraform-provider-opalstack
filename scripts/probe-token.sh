#!/usr/bin/env bash
# What can the scoped test token see? GET requests only, by construction: the
# one curl call below has no -X, -d or --data.
#
# It answers three questions, as status codes and counts:
#   - is a list filtered to the objects the token's ACL names, empty, or refused?
#   - can the token read the objects its ACL names?
#   - does an object outside the ACL answer 403 or 404?
#
# Writes explore-out/probe-token.txt (git-ignored) and prints the same text.
# No object name, UUID or other value of the account is printed: a 2xx body is
# reduced to its field names, and any other body has its UUIDs masked.
#
# The ids come from the environment, so none is written in this repository:
#   OPALSTACK_TEST_OSUSER_ID   the sandbox shell user (inside the ACL)
#   OPALSTACK_TEST_DOMAIN_ID   the base domain for tests (inside the ACL)
#   OPALSTACK_PROBE_OUTSIDE    optional, space separated kind=uuid pairs of
#                              objects the ACL does not name, e.g. "osuser=... app=..."
#
# The token is read from pass (or OPALSTACK_TOKEN) and handed to curl on stdin,
# never on the command line and never to a file.
#
# Usage: scripts/probe-token.sh
set -euo pipefail

readonly API="https://my.opalstack.com/api/v1"
readonly PASS_PATH="Opalstack/tokens/tfacc"
readonly KINDS=(server ip osuser domain app site dnsrecord)
readonly UUID_RE='[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}'

: "${OPALSTACK_TEST_OSUSER_ID:?set it to the id of the sandbox shell user}"
: "${OPALSTACK_TEST_DOMAIN_ID:?set it to the id of the base domain for tests}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUT="$ROOT/explore-out/probe-token.txt"
mkdir -p "$(dirname "$OUT")"

TOKEN="${OPALSTACK_TOKEN:-}"
if [[ -z "$TOKEN" ]]; then
  TOKEN="$(pass show "$PASS_PATH" | head -n1)" || { echo "FATAL: cannot read token from pass" >&2; exit 1; }
fi
TOKEN="${TOKEN//[[:space:]]/}"
[[ "$TOKEN" =~ ^[A-Za-z0-9_-]{20,}$ ]] || { echo "FATAL: token has an unexpected shape (length ${#TOKEN})" >&2; exit 1; }

TMP="$(mktemp -d -p "${XDG_RUNTIME_DIR:-/tmp}" opal-probe.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
readonly BODY="$TMP/body" REPORT="$TMP/report"

# get <path>  ->  prints the status code; body in $BODY
get() {
  local code
  sleep 0.5
  code="$(curl -sS --max-time 60 -o "$BODY" -w '%{http_code}' \
    -H "Accept: application/json" -K - "$API$1" <<<"header = \"Authorization: Token $TOKEN\"")"
  if [[ "$code" == "429" || "$code" == 5* ]]; then
    echo "FATAL: GET $1 answered $code; stopping instead of sending more" >&2
    exit 1
  fi
  printf '%s' "$code"
}

# body_summary <code>: field names for a 2xx answer, the masked body otherwise.
body_summary() {
  if [[ "$1" == 2* ]]; then
    jq -c 'if type == "object" then keys else type end' "$BODY" 2>/dev/null || echo "(not JSON)"
  else
    head -c 300 "$BODY" | tr '\n' ' ' | sed -E "s/$UUID_RE/<uuid>/g"
    echo
  fi
}

# read_one <label> <kind> <id>
read_one() {
  local code
  [[ "$3" =~ ^$UUID_RE$ ]] || { echo "FATAL: the id given for $1 is not a UUID" >&2; exit 1; }
  code="$(get "/$2/read/$3")"
  printf 'read %-7s %-12s %s  %s\n' "$2" "$1" "$code" "$(body_summary "$code")"
}

{
  echo "token length ${#TOKEN}"
  echo
  echo "lists: kind, status, count, how many of the two sandbox ids are in it"
  for kind in "${KINDS[@]}"; do
    code="$(get "/$kind/list/")"
    if [[ "$code" == 2* ]]; then
      summary="$(jq -r --arg a "$OPALSTACK_TEST_OSUSER_ID" --arg b "$OPALSTACK_TEST_DOMAIN_ID" \
        '"count \(length)  sandbox ids \([.[] | select(.id == $a or .id == $b)] | length)"' "$BODY" 2>/dev/null \
        || echo "(not a JSON array)")"
    else
      summary="$(body_summary "$code")"
    fi
    printf 'list %-10s %s  %s\n' "$kind" "$code" "$summary"
  done

  echo
  echo "reads: kind, which object, status, field names or error body"
  read_one "in the ACL" osuser "$OPALSTACK_TEST_OSUSER_ID"
  read_one "in the ACL" domain "$OPALSTACK_TEST_DOMAIN_ID"
  for pair in ${OPALSTACK_PROBE_OUTSIDE:-}; do
    read_one "outside" "${pair%%=*}" "${pair#*=}"
  done
} >"$REPORT"

mv "$REPORT" "$OUT"
cat "$OUT"
