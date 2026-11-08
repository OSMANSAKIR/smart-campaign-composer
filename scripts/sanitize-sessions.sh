#!/usr/bin/env bash
# Replaces owner-specific terms in session exports and writes the result for committing (KICKOFF §13).
#
#   scripts/sanitize-sessions.sh [input-dir] [output-dir]     default: ai-sessions/_raw  ai-sessions/sessions
#
# Terms come from the gitignored .sanitize-terms file, one per line: `term` (replaced by <REDACTED>) or
# `term => replacement`. The home directory is always replaced by `~`. Afterwards gitleaks scans the output.
# A human still skims the result before it is committed.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
in="${1:-$root/ai-sessions/_raw}"
out="${2:-$root/ai-sessions/sessions}"
terms="$root/.sanitize-terms"

if [ ! -f "$terms" ]; then
  echo "sanitize: $terms is missing. Create it (gitignored) with your name, email and other personal terms." >&2
  exit 1
fi
mkdir -p "$out"

python3 -I - "$in" "$out" "$terms" "$HOME" <<'PY'
import sys
from pathlib import Path

src, dst, terms_file, home = Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]), sys.argv[4]
pairs = [(home, "~")]
for line in terms_file.read_text(encoding="utf-8").splitlines():
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    term, _, repl = line.partition("=>")
    pairs.append((term.strip(), repl.strip() or "<REDACTED>"))
pairs.sort(key=lambda p: len(p[0]), reverse=True)  # longest first, so "Full Name" wins over "Name"

count = 0
for f in sorted(p for p in src.rglob("*") if p.is_file() and p.suffix in (".md", ".txt")):
    text = f.read_text(encoding="utf-8", errors="replace")
    for term, repl in pairs:
        text = text.replace(term, repl)
        text = text.replace(term.lower(), repl) if term.lower() != term else text
    rel = str(f.relative_to(src)).replace(home.replace("/", "-"), "")  # encoded project dirs embed the home path
    for term, repl in pairs:
        rel = rel.replace(term, repl)
    target = dst / rel
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(text, encoding="utf-8")
    count += 1
print(f"sanitize: wrote {count} file(s) to {dst}")
PY

gitleaks dir --redact --no-banner "$out"
echo "sanitize: gitleaks clean. Skim $out before committing."
