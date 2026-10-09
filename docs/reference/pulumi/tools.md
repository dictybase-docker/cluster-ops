# Pulumi Toolchain Check Details

Back to: [Pulumi Setup Guide](../../pulumi-setup.md)

## What It Does

Verifies the local toolchain the Pulumi workflow needs: `pulumi`, `gcloud`,
`kubectl`, `jq`, `yq`. Prints one line per tool with its version; exits
non-zero if any binary is missing from `PATH`. Read-only, zero prompts.

## Behavior

1. Resolves each binary on `PATH` (`exec.LookPath`); a missing binary is a `FAIL not found on PATH`.
2. Reads the version per tool — `pulumi version`, `gcloud version`, `kubectl version --client -o json`, `jq --version`, `yq --version` — and reports `PASS <tool> <version>`.
3. A version that cannot be read passes as `<tool> version unknown` (display parsing degrades, never crashes).
4. Prints `All required tools present.` on success; on failure the wrapper prints the install hint `just install-tool --name <tool> --version <version>`.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| — | — | — | Takes no flags |

## Notes

- Version strings are display-only: never parsed to detect failure conditions.
- The pre-migration shell recipe remains as `check-tools-shell` for one release (rollback).
