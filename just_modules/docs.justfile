# Mechanical doc checks per docs/STYLE.md: TOC/anchor targets, file links,
# comment-as-step env switches, forbidden patterns, section numbering gaps.
# docs/plans/ (historical) is exempt.
# Usage: just docs-lint
[group('docs')]
[no-cd]
docs-lint:
    #!/usr/bin/env bash
    set -euo pipefail
    chmod +x "{{ justfile_directory() }}/scripts/docs-lint.sh"
    "{{ justfile_directory() }}/scripts/docs-lint.sh"