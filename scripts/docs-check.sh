#!/usr/bin/env bash
# Verifies the claims the README makes about this repository.
#
# The README goes stale on events the code does not cause — a tag, a released version — and nothing
# else in CI has an opinion about it. Every other layer is checked; documentation that asserts
# specific numbers should be too, or it drifts until someone notices by hand.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

failures=0

fail() {
  echo "  FAIL  $1" >&2
  failures=$((failures + 1))
}

pass() {
  echo "  ok    $1"
}

claimed() {
  # Pulls the first number preceding a phrase on the README's counts line.
  grep -oE "[0-9]+ $1" README.md | head -1 | grep -oE "^[0-9]+" || echo "missing"
}

echo "==> counts"

actual_contract_tests=$(grep -rhoE '^\s+function (test|testFuzz|invariant)_[A-Za-z0-9_]*\(' contracts/test/ | wc -l | tr -d ' ')
claimed_contract_tests=$(claimed "contract tests")
if [ "$claimed_contract_tests" = "$actual_contract_tests" ]; then
  pass "contract tests: $actual_contract_tests"
else
  fail "README claims $claimed_contract_tests contract tests, found $actual_contract_tests"
fi

actual_e2e=$(grep -rhoE '^func Test[A-Za-z0-9_]*\(' backend/internal/e2e/ | wc -l | tr -d ' ')
claimed_e2e=$(claimed "end-to-end tests")
if [ "$claimed_e2e" = "$actual_e2e" ]; then
  pass "end-to-end tests: $actual_e2e"
else
  fail "README claims $claimed_e2e end-to-end tests, found $actual_e2e"
fi

actual_packages=$(cd backend && go list ./... | while read -r pkg; do
  dir="${pkg#github.com/aizen299/aegis-protocol/backend/}"
  [ -d "$dir" ] && ls "$dir"/*_test.go >/dev/null 2>&1 && echo "$pkg"
done | wc -l | tr -d ' ')
claimed_packages=$(claimed "backend packages")
if [ "$claimed_packages" = "$actual_packages" ]; then
  pass "backend packages with tests: $actual_packages"
else
  fail "README claims $claimed_packages backend packages, found $actual_packages"
fi

echo "==> released versions have tags"

# A row saying "Released" or "tagged vX" must correspond to a tag that exists. This is the check
# that would have caught "ready to tag v0.2.0" surviving the tag being created.
while read -r version; do
  if git rev-parse "$version" >/dev/null 2>&1; then
    pass "$version is tagged"
  else
    fail "README references $version as released, but no such tag exists"
  fi
done < <(grep -oE 'tagged \`v[0-9]+\.[0-9]+\.[0-9]+\`' README.md | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | sort -u)

if grep -qE 'ready to tag \`(v[0-9]+\.[0-9]+\.[0-9]+)\`' README.md; then
  pending=$(grep -oE 'ready to tag \`v[0-9]+\.[0-9]+\.[0-9]+\`' README.md | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  if git rev-parse "$pending" >/dev/null 2>&1; then
    fail "README says \"ready to tag $pending\" but $pending already exists"
  else
    pass "$pending is genuinely untagged"
  fi
fi

echo "==> referenced paths exist"

while read -r path; do
  [ -e "$path" ] && pass "$path" || fail "README links $path, which does not exist"
done < <(grep -oE '\]\(([A-Za-z0-9_./-]+)\)' README.md \
  | sed -E 's/^\]\(//; s/\)$//' \
  | grep -vE '^https?:' | grep -vE '^#' | sort -u)

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures README claim(s) are out of date." >&2
  exit 1
fi
echo "README claims check out."
