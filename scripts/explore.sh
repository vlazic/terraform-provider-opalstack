#!/usr/bin/env bash
# Read-only exploration of the Opalstack API. GET requests only, by construction:
# neither curl call below has -X, -d or --data.
#
# Writes to explore-out/ (git-ignored). Every list response is reduced to field
# names, JSON types and enum values before it is written there, so no object
# name, UUID, IP address or domain of the real account reaches that directory.
# Raw responses exist only in a private temporary directory, in memory where the
# system offers one, which is removed when the script exits.
#
# It stops at the first 429 or 5xx answer rather than keep sending.
#
# The token is read from pass (or OPALSTACK_TOKEN) and handed to curl on stdin,
# never on the command line and never to a file.
#
# Usage: scripts/explore.sh
set -euo pipefail

readonly API="https://my.opalstack.com/api/v1"
readonly PASS_PATH="Opalstack/tokens/full-access"
readonly ZERO_UUID="00000000-0000-0000-0000-000000000000"
readonly KINDS=(server ip osuser domain app site dnsrecord)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUT="$ROOT/explore-out"
mkdir -p "$OUT"
rm -f "$OUT"/*.new   # left behind by an interrupted run

TOKEN="${OPALSTACK_TOKEN:-}"
if [[ -z "$TOKEN" ]]; then
  TOKEN="$(pass show "$PASS_PATH" | head -n1)" || { echo "FATAL: cannot read token from pass" >&2; exit 1; }
fi
# A pass entry can end in a carriage return or a space, which is not part of the token.
TOKEN="${TOKEN//[[:space:]]/}"
# The length is not assumed: tokens issued by the newer dashboard are not 40 characters.
[[ "$TOKEN" =~ ^[A-Za-z0-9_-]{20,}$ ]] || { echo "FATAL: token has an unexpected shape (length ${#TOKEN})" >&2; exit 1; }
echo "explore: token length ${#TOKEN}" >&2

TMP="$(mktemp -d -p "${XDG_RUNTIME_DIR:-/tmp}" opal-explore.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
readonly BODY="$TMP/body" HDRS="$TMP/headers"

# get <path> [accept]  ->  prints the status code; body in $BODY, headers in $HDRS
get() {
  local path="$1" accept="${2:-application/json}" code
  sleep 0.5
  code="$(curl -sS --max-time 60 -o "$BODY" -D "$HDRS" -w '%{http_code}' \
    -H "Accept: $accept" -K - "$API$path" <<<"header = \"Authorization: Token $TOKEN\"")"
  if [[ "$code" == "429" || "$code" == 5* ]]; then
    echo "FATAL: GET $path answered $code; stopping instead of sending more" >&2
    exit 1
  fi
  printf '%s' "$code"
}

# shape <jq-filter>: prints the reduced form of $BODY, or a marker. On failure it
# prints neither the body nor jq's message, since both can quote real values.
shape() {
  jq "$1" "$BODY" 2>/dev/null || echo "(could not reduce the body: $(wc -c <"$BODY") bytes)"
}

# Field names and types, merged across every element. Values never appear, except
# for state/type/ready/primary, which are enums and booleans.
# shellcheck disable=SC2016
readonly SHAPE='
def t: if type == "array" then "array<" + ((map(type) | unique | join("|")) // "") + ">" else type end;
def shape: (map(to_entries) | add // []) | group_by(.key)
  | map({key: .[0].key, value: (map(.value | t) | unique | join("|"))}) | from_entries;
def enums: [.[] | {state, type, ready, primary} | with_entries(select(.value != null))]
  | (map(to_entries) | add // []) | group_by(.key)
  | map({key: .[0].key, value: (map(.value) | unique)}) | from_entries;
def nested($k): [.[] | .[$k]? | select(type == "array") | .[] | select(type == "object")] | shape;
def dupes(f): (group_by(f) | map(select(length > 1)) | length);
def report: {count: length, fields: shape, enums: enums,
  nested: {routes: nested("routes")},
  duplicate_names: (if (.[0]? | has("name")) then dupes(.name) else null end)};
if type == "array" then report
else with_entries(.value |= (if type == "array" then report else type end)) end'

echo "explore: writing to $OUT" >&2

# 1. The live schema, in each form it might be served.
for variant in "plain|application/json|" "json|application/json|?format=json" \
               "oai-json|application/vnd.oai.openapi+json|" "oai-yaml|application/vnd.oai.openapi|"; do
  IFS='|' read -r label accept query <<<"$variant"
  code="$(get "/schema/$query" "$accept")"
  printf '%s %s %s\n' "$label" "$code" "$(wc -c <"$BODY")" >>"$OUT/schema-status.txt.new"
  if [[ "$code" == "200" ]]; then cp "$BODY" "$OUT/schema.$label"; fi
done
mv "$OUT/schema-status.txt.new" "$OUT/schema-status.txt"

# 2. Each kind: list shape, embed shape, not-found and malformed-id behaviour.
for kind in "${KINDS[@]}"; do
  code="$(get "/$kind/list/")"
  if [[ "$kind" == "${KINDS[0]}" ]]; then
    grep -viE '^(set-cookie|authorization):' "$HDRS" >"$OUT/list-headers.txt" || true
  fi
  { echo "status: $code"; shape "$SHAPE"; } >"$OUT/$kind.list.txt"

  for id in "$ZERO_UUID" "not-a-uuid"; do
    code="$(get "/$kind/read/$id")"
    { echo "GET /$kind/read/$id"; echo "status: $code"
      grep -iE '^content-type:' "$HDRS" || true; head -c 600 "$BODY"; echo; echo; } >>"$OUT/$kind.notfound.txt.new"
  done
  mv "$OUT/$kind.notfound.txt.new" "$OUT/$kind.notfound.txt"
done

# 3. Embed: does a foreign key become an object, and which keys are accepted?
for probe in "osuser|server" "app|osuser,server" "site|domains,ip4" "dnsrecord|domain" "ip|server" "osuser|nonsense"; do
  IFS='|' read -r kind embed <<<"$probe"
  code="$(get "/$kind/list/?embed=$embed")"
  { echo "GET /$kind/list/?embed=$embed"; echo "status: $code"
    shape "$SHAPE | (.fields // .)"; echo; } >>"$OUT/embed.txt.new"
done
mv "$OUT/embed.txt.new" "$OUT/embed.txt"

# 4. Name uniqueness, as counts only: are app names unique per osuser or globally?
get "/app/list/" >/dev/null
jq '{apps: length,
     names_shared_across_osusers: (group_by(.name) | map(select((map(.osuser) | unique | length) > 1)) | length),
     names_repeated_within_one_osuser: (group_by([.osuser, .name]) | map(select(length > 1)) | length)}' \
  "$BODY" 2>/dev/null >"$OUT/app.uniqueness.txt"

# 5. Auth failure shape, with a token that cannot be valid.
sleep 0.5
curl -sS --max-time 60 -o "$BODY" -w '%{http_code}' -H "Authorization: Token 0000000000000000000000000000000000000000" \
  "$API/osuser/list/" >"$OUT/unauthorized.txt"
{ echo; head -c 400 "$BODY"; echo; } >>"$OUT/unauthorized.txt"

echo "explore: done" >&2
ls -la "$OUT" >&2
