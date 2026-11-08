#!/usr/bin/env bash
# Fails if a secret or an owner-specific term is in the repository (R-RES-1, R-DEL-6).
# Checks: gitleaks over git history and over committed ai-sessions/ files, the terms in .sanitize-terms and the
# home directory path over tracked files, and that .env is not tracked.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"
fail=0

echo "== gitleaks: git history"
gitleaks git --config .gitleaks.toml --redact --no-banner . || fail=1

echo "== gitleaks: committed ai-sessions files"
while IFS= read -r f; do
  gitleaks dir --config .gitleaks.toml --redact --no-banner --log-level error "$f" || { echo "secret in $f"; fail=1; }
done < <(git ls-files ai-sessions)

echo "== session exports listed in ai-sessions/README.md exist, and every export is listed"
listed=$(grep -oE '`(sessions|subagents)/[^`]+\.md`' ai-sessions/README.md | tr -d '`' | sort -u)
on_disk=$(cd ai-sessions && find sessions subagents -name '*.md' 2>/dev/null | sort -u)
missing=$(comm -23 <(echo "$listed") <(echo "$on_disk") | sed '/^$/d')
unlisted=$(comm -13 <(echo "$listed") <(echo "$on_disk") | sed '/^$/d')
if [ -n "$missing" ]; then echo "listed but missing:"; echo "$missing"; fail=1; fi
if [ -n "$unlisted" ]; then echo "exported but not listed:"; echo "$unlisted"; fail=1; fi
[ -n "$listed" ] || { echo "no session exports listed"; fail=1; }

echo "== .env must not be tracked"
if git ls-files --error-unmatch .env >/dev/null 2>&1; then echo ".env is tracked!"; fail=1; fi

echo "== personal terms in tracked files"
terms=("$HOME")
if [ -f .sanitize-terms ]; then
  while IFS= read -r line; do
    line="${line%%=>*}"; line="$(echo "$line" | xargs)"
    [ -n "$line" ] && [ "${line:0:1}" != "#" ] && terms+=("$line")
  done < .sanitize-terms
else
  echo "warning: .sanitize-terms missing; only the home path is checked"
fi
for t in "${terms[@]}"; do
  if git grep -n -i -F -e "$t" -- . ':!*.lock' >/dev/null 2>&1; then
    echo "found a personal term in tracked files:"; git grep -n -i -F -l -e "$t" -- . ; fail=1
  fi
done

if [ "$fail" -ne 0 ]; then echo "sanitize-check: FAILED"; exit 1; fi
echo "sanitize-check: clean"
