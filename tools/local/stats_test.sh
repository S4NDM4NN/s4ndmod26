#!/bin/bash
# Runs the status API's stats tests against a throwaway local Postgres (downloaded once).
set -euo pipefail
R="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${TMPDIR:-/tmp}/s4nd-pgrun"
(cd "$R/tools/local/pgrun" && go build -o "$OUT" .)
cd "$R/infra/web/status-api"
"$OUT" go test -count=1 ./stats "$@"
