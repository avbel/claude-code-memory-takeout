#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

fail() {
    echo "lint: $1" >&2
    exit 1
}

out="$(gofmt -l .)" || fail "gofmt failed to run"
[ -z "$out" ] || { echo "$out"; fail "gofmt -l reported unformatted files (see above)"; }

go vet ./... || fail "go vet reported problems"

out="$(go fix -diff ./... 2>/dev/null)" || fail "go fix failed to run"
[ -z "$out" ] || { echo "$out"; fail "go fix -diff suggests changes (see above)"; }

out="$(go mod tidy -diff 2>/dev/null)" || fail "go mod tidy -diff failed"
[ -z "$out" ] || { echo "$out"; fail "go.mod/go.sum need 'go mod tidy' (see above)"; }

want="$(cat .golangci-version)"
if ! command -v golangci-lint >/dev/null 2>&1; then
    fail "install golangci-lint ${want}: brew install golangci-lint"
fi
have="$(golangci-lint version 2>/dev/null | grep -o 'version v\?[0-9][0-9.]*' | grep -o '[0-9][0-9.]*' | head -1 || true)"
have="v${have}"
if [ "$have" != "$want" ]; then
    echo "lint: warning: golangci-lint ${have} installed, expected ${want}; continuing" >&2
fi
golangci-lint run ./... || fail "golangci-lint reported problems"
