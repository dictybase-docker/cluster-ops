# Agent Instructions for cluster-ops

## Documentation

Before creating or editing any file under `docs/`, read
[`docs/STYLE.md`](docs/STYLE.md) and follow it. Core rules:

- Guides stay lean: summary + `→ [details]` link + one command per section.
  Explanation lives in `docs/reference/<area>/`.
- Never duplicate content between docs — link instead.
- Commands show minimal invocation; defaults come from `just cluster-env`.
- Keep TOC, Quick Reference, and cross-doc anchors in sync on every edit.
