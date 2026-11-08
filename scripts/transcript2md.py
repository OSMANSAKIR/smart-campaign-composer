#!/usr/bin/env python3
"""Convert Claude Code JSONL transcripts into readable Markdown (KICKOFF §13).

Usage:
  python3 -I scripts/transcript2md.py <file.jsonl|dir> <out.md|outdir>

For a directory, every *.jsonl below it is converted, keeping the relative layout. Images, base64 payloads and
bookkeeping entries are dropped; long tool inputs and results are truncated. The output still contains local
paths and names: run scripts/sanitize-sessions.sh before committing anything.
"""
import json
import re
import sys
from pathlib import Path

MAX_TOOL_CHARS = 2000
BASE64_RE = re.compile(r"[A-Za-z0-9+/=]{400,}")


def clip(text: str, limit: int = MAX_TOOL_CHARS) -> str:
    text = BASE64_RE.sub("[base64 removed]", text)
    if len(text) > limit:
        return text[:limit] + f"\n… [{len(text) - limit} more characters truncated]"
    return text


def block_text(block) -> str:
    if isinstance(block, str):
        return block
    if not isinstance(block, dict):
        return ""
    kind = block.get("type")
    if kind == "text":
        return block.get("text", "")
    if kind == "image":
        return "[image removed]"
    if kind == "tool_result":
        content = block.get("content")
        if isinstance(content, list):
            return "\n".join(block_text(c) for c in content)
        return str(content or "")
    return ""


def fence(text: str, lang: str = "") -> str:
    ticks = "````" if "```" in text else "```"
    return f"{ticks}{lang}\n{text}\n{ticks}"


def convert(src: Path, dst: Path) -> None:
    out, models = [], set()
    usage = {"input_tokens": 0, "output_tokens": 0, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
    first = last = session = None
    for line in src.read_text(encoding="utf-8").splitlines():
        try:
            entry = json.loads(line)
        except json.JSONDecodeError:
            continue
        kind = entry.get("type")
        if kind not in ("user", "assistant") or entry.get("isMeta"):
            continue
        ts = entry.get("timestamp")
        first, last = first or ts, ts or last
        session = session or entry.get("sessionId")
        msg = entry.get("message") or {}
        if kind == "assistant":
            if msg.get("model"):
                models.add(msg["model"])
            for k in usage:
                usage[k] += (msg.get("usage") or {}).get(k) or 0
        content = msg.get("content")
        blocks = content if isinstance(content, list) else [content]
        for b in blocks:
            if isinstance(b, dict) and b.get("type") == "tool_use":
                args = json.dumps(b.get("input", {}), ensure_ascii=False, indent=2)
                out.append(f"**Tool call · {b.get('name')}**\n\n" + fence(clip(args), "json"))
            elif isinstance(b, dict) and b.get("type") == "tool_result":
                out.append("**Tool result**\n\n" + fence(clip(block_text(b))))
            elif isinstance(b, dict) and b.get("type") == "thinking":
                thought = b.get("thinking", "").strip()
                if thought:
                    out.append(f"<details><summary>Thinking</summary>\n\n{thought}\n\n</details>")
            else:
                text = block_text(b).strip()
                if text:
                    who = "User" if kind == "user" else "Assistant"
                    out.append(f"### {who}\n\n{text}")
    header = [
        f"# Session {session or src.stem}",
        "",
        f"- Source: `{src.name}`",
        f"- Time: {first} → {last}",
        f"- Models: {', '.join(sorted(models)) or 'n/a'}",
        "- Tokens: " + ", ".join(f"{k} {v:,}" for k, v in usage.items()),
        "",
        "---",
        "",
    ]
    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_text("\n".join(header) + "\n\n".join(out) + "\n", encoding="utf-8")


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    src, dst = Path(sys.argv[1]), Path(sys.argv[2])
    if src.is_dir():
        files = sorted(src.rglob("*.jsonl"))
        for f in files:
            convert(f, dst / f.relative_to(src).with_suffix(".md"))
        print(f"transcript2md: converted {len(files)} file(s) into {dst}")
    else:
        convert(src, dst)
        print(f"transcript2md: wrote {dst}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
