#!/bin/bash
# Runs the demo-replay store's tests against a throwaway local Postgres (the same one stats_test.sh uses).
set -euo pipefail
R="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${TMPDIR:-/tmp}/s4nd-pgrun"
(cd "$R/tools/local/pgrun" && go build -o "$OUT" .)
cd "$R/infra/web/status-api"
"$OUT" go test -count=1 ./demostore . -run "TestMigrate|TestUploads|TestRemoving|TestADemo|TestListMatches|TestLoad|TestLegacy|TestFS|TestKeys|TestDemo|TestDisplay" "$@"
