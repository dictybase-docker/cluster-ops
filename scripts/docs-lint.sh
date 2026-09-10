#!/usr/bin/env bash
# docs-lint: mechanical checks for docs/STYLE.md rules that a script can
# decide without judgment. Reports file:line and exits non-zero on findings.
# docs/plans/ is exempt (historical documents).
#
# Checks:
#   1. TOC links resolve to real headings (per file)
#   2. In-document anchors resolve to real headings (per file)
#   3. Relative file-link targets exist on disk
#   4. Comment-as-step: an env/context switch hidden as a comment inside a
#      multi-command fenced bash block (STYLE.md rule 6)
#   5. Forbidden patterns: emoji, !!!, TODO/FIXME, hardcoded --stack prod
#   6. Guide section numbering has no gaps after the first numbered section
#
# Usage: just docs-lint

set -u
root="$(cd "$(dirname "$0")/.." && pwd)"
failures=0

lint() {
    local f="$1"
    python3 - "$f" <<'PYEOF'
import re, sys, os

f = sys.argv[1]
text = open(f).read()
lines = text.split("\n")
rel = os.path.relpath(f)

def p(msg, ln=None):
    loc = f"{rel}:{ln}" if ln else rel
    print(f"  {loc}  {msg}")

# headings -> generated anchors (github style)
def anchor(h):
    a = h.strip().lower()
    a = re.sub(r"[^\w\s-]", "", a)
    a = re.sub(r"\s", "-", a)
    return a

heads = set()
for i, l in enumerate(lines, 1):
    m = re.match(r"^(#{1,4})\s+(.*)$", l)
    if m:
        heads.add(anchor(m.group(2)))

# fenced regions (code examples) — checks below skip them
fence_lines = set()
in_f = False
for i, l in enumerate(lines, 1):
    if l.strip().startswith("```"):
        if in_f:
            fence_lines.update(range(in_f, i + 1))
            in_f = False
        else:
            in_f = i
if in_f:
    fence_lines.update(range(in_f, len(lines) + 1))

bad = 0

# 1+2. every in-doc anchor (TOC and inline) resolves to a heading
for i, l in enumerate(lines, 1):
    if i in fence_lines:
        continue
    for m in re.finditer(r"\]\(#([^)]+)\)", l):
        if m.group(1) not in heads:
            p(f"anchor '#{m.group(1)}' has no matching heading", i)
            bad += 1

# 3. relative file links exist
for i, l in enumerate(lines, 1):
    if i in fence_lines:
        continue
    for m in re.finditer(r"\]\(([^)#]+?)(#[^)]*)?\)", l):
        t = m.group(1)
        if t.startswith(("http://", "https://", "mailto:", "...")) or t.startswith("/"):
            continue
        if not os.path.exists(os.path.join(os.path.dirname(f), t)):
            p(f"link target missing: {t}", i)
            bad += 1

# 4. comment-as-step inside fenced bash blocks
#    A fenced block with >1 command whose comment matches switch vocabulary.
switch_words = re.compile(
    r"#.*(only if|if still|rotate|switch|re-enter|enter the|as .+ identity)",
    re.I,
)
in_fence = False
block_start = 0
block_body = []
for i, l in enumerate(lines, 1):
    if l.strip().startswith("```"):
        if in_fence:
            if block_body and any(switch_words.search(c) for c in block_body):
                cmds = sum(1 for c in block_body if c.startswith("just ") or c.startswith("kubectl "))
                if cmds > 1:
                    p(
                        "comment-as-step: env/context switch hidden as a comment — "
                        "split into separate labeled blocks (STYLE.md rule 6)",
                        block_start,
                    )
                    bad += 1
            in_fence = False
            block_body = []
        else:
            in_fence = True
            block_start = i
        continue
    if in_fence:
        # inside a fence: only lines starting with '#' that are not commands are comments
        if l.startswith("#"):
            block_body.append(l)

# 5. forbidden patterns
for i, l in enumerate(lines, 1):
    if i in fence_lines:
        continue
    if re.search(r"[\U0001F300-\U0001FAFF\u2705\u274C\u26A0\uFE0F]", l):
        p("emoji in doc", i)
        bad += 1
    if "TODO" in l or "FIXME" in l:
        p(f"TODO/FIXME left in doc", i)
        bad += 1
    if "--stack prod" in l and not rel.endswith("STYLE.md"):
        p("hardcoded --stack prod; default from $PULUMI_STACK", i)
        bad += 1

# 6. guide numbering gaps (only files that look like guides: have TOC + numbered sections)
secs = []
for i, l in enumerate(lines, 1):
    m = re.match(r"^## (\d+)\. ", l)
    if m:
        secs.append((int(m.group(1)), i))
if secs and "## Table of Contents" in text:
    want = secs[0][0]
    for n, i in secs:
        if n != want:
            p(f"section numbering gap: expected {want}, found {n} (check ## {n})", i)
            bad += 1
        want += 1

sys.exit(1 if bad else 0)
PYEOF
}

echo "docs-lint: mechanical checks per docs/STYLE.md"
count=0
while IFS= read -r f; do
    count=$((count + 1))
    out=$(lint "$f") || { echo "$f:"; echo "$out"; failures=$((failures + 1)); }
done < <(cd "$root" && find docs README.md -name '*.md' 2>/dev/null | grep -v '^docs/plans/' | sort)

echo
if [ "$failures" -eq 0 ]; then
    printf '\033[32mAll %d docs pass mechanical checks.\033[0m\n' "$count"
    exit 0
fi
printf '\033[31m%d doc(s) failed.\033[0m\n' "$failures"
exit 1