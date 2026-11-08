#!/usr/bin/env bash
# Copies every Claude Code transcript of this project into ai-sessions/_raw/harvest/ (gitignored):
# main sessions, subagent transcripts (<session>/subagents/*.jsonl) and fallback worktree sessions, which live
# under their own encoded project paths. Memory files are skipped.
# Next steps: scripts/transcript2md.py, then scripts/sanitize-sessions.sh (KICKOFF §13).
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
dest="$root/ai-sessions/_raw/harvest"
mkdir -p "$dest"

shopt -s nullglob
found=0
for dir in "$HOME"/.claude/projects/*smart-campaign-composer*; do
  name="$(basename "$dir")"
  mkdir -p "$dest/$name"
  rsync -a --prune-empty-dirs --exclude='memory/' --include='*/' --include='*.jsonl' --include='*.json' \
    --exclude='*' "$dir/" "$dest/$name/"
  found=$((found + 1))
done

if [ "$found" -eq 0 ]; then
  echo "harvest: no project directories found under ~/.claude/projects" >&2
  exit 1
fi
echo "harvest: copied $(find "$dest" -name '*.jsonl' | wc -l | tr -d ' ') transcript files from $found project dir(s) into ai-sessions/_raw/harvest/"
