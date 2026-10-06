# Go Migration Plans

Replace brittle shell in `just_modules/*.justfile` and `scripts/` with Go in
`cmd/cluster-ops` — client-go, the official GCP SDKs, and terratest only for
the port-forward tunnel and k3d integration tests.

| Plan | Scope | Status |
|------|-------|--------|
| [Phase 1 implementation](checks-phase1-implementation.md) | Shared foundation + the four backend deploy-gate checks, task by task | Ready to execute — verified on a clean branch copy |
| [Checks and probes](checks-probes.md) | All check/verify/validate recipes; phases 1–3 | Proposed |
| [Port-forward](port-forward.md) | The 8 `kubectl port-forward` invocations | Proposed — needs phase 1 |
| [Wait and poll](wait-poll.md) | `sleep`/`until` loops and `kubectl wait` → `internal/waitfor` | Proposed — needs phase 1 |
| [GCP converge](gcp-converge.md) | `gcloud` mutations → describe-then-create in `internal/gcp` | Proposed |
| [Pulumi Automation API](pulumi-automation.md) | Pulumi CLI orchestration → `internal/pulumiauto` | Optional — go/no-go criteria inside |

Order: phase 1 first — it creates `internal/preflight`, `internal/kube` and
`internal/registry`, which every other plan builds on.
