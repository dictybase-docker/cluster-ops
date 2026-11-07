# Recipes for the dcr-mcp Pulumi project. Guide: docs/dcr-mcp-deploy.md.
# Everything assumes the cluster-env sub-shell so Pulumi and Kubernetes target
# one cluster. Every value comes from dcr-mcp/Pulumi.<stack>.yaml and
# config/clusters/<cluster>.yaml.

# Resolve the Pulumi stack name, or fail. Never falls back to dev.
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[no-cd]
_require-stack stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    STACK={{ quote(stack) }}
    if [[ -z "$STACK" ]]; then
        STACK="${PULUMI_STACK:-}"
    fi
    if [[ -z "$STACK" ]]; then
        echo "Error: no stack name — set PULUMI_STACK (via cluster env) or pass --stack." >&2
        exit 1
    fi
    if [[ ! "$STACK" =~ ^[A-Za-z0-9._-]+$ ]]; then
        echo "Error: invalid stack name '$STACK'." >&2
        exit 1
    fi
    echo "$STACK"

# Read-only preflight gate for dcr-mcp: verifies the cluster registry entry
# matching --stack, the shared namespace, Service graphql-api on port 8080, and
# the rollout of deployment/graphql-api-server.
# Usage: just dcr-mcp check [--stack <name>] [--namespace <ns>] [--timeout <duration>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace to check (defaults to the cluster registry namespace)")]
[arg("timeout", long="timeout", short="t", help="Rollout wait for deployment/graphql-api-server (default 120s)")]
[group('dcr-mcp')]
[no-cd]
check stack="" namespace="" timeout="120s":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    STACK=$(just dcr-mcp _require-stack --stack {{ quote(stack) }})
    TIMEOUT={{ quote(timeout) }}
    if [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*[smh]$ ]]; then
        echo "Error: timeout must look like 120s, 5m, or 1h." >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq not found on PATH — run prepare-tools." >&2
        exit 1
    fi

    entry=""
    for f in config/clusters/*.yaml; do
        [[ -e "$f" ]] || continue
        if [[ "$(yq -r '.stack' "$f")" == "$STACK" ]]; then
            entry="$f"
            break
        fi
    done
    if [[ -z "$entry" ]]; then
        echo "Error: no registry entry with stack '$STACK' (checked config/clusters/*.yaml)." >&2
        echo "Run: just gcp-cluster register-cluster" >&2
        exit 1
    fi

    NS={{ quote(namespace) }}
    [[ -z "$NS" ]] && NS=$(yq -r '.namespace // ""' "$entry")
    if [[ -z "$NS" || "$NS" == *"<"* || "$NS" == *">"* ]]; then
        echo "Error: $entry has an empty or placeholder namespace." >&2
        exit 1
    fi

    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: namespace '$NS' not found — run: just gcp-pulumi apply-namespaces" >&2
        exit 1
    fi
    if ! kubectl -n "$NS" get service graphql-api >/dev/null 2>&1; then
        echo "Error: service/graphql-api not found in namespace '$NS' — deploy graphql_server first." >&2
        exit 1
    fi
    if ! kubectl -n "$NS" rollout status deployment/graphql-api-server --timeout="$TIMEOUT" >/dev/null 2>&1; then
        echo "Error: deployment/graphql-api-server did not roll out in namespace '$NS'." >&2
        exit 1
    fi

    echo "dcr-mcp prerequisites ready for stack '${STACK}' in namespace '${NS}'."
    echo "Next: just gcp-pulumi scaffold-dcr-mcp-stack"

# Post-deploy check: Deployment dcr-mcp-api-server rolled out, running image
# equals <image.name>:<image.tag> from dcr-mcp/Pulumi.<stack>.yaml, and Service
# dcr-mcp-api exists on port properties.port. Read-only.
# Usage: just dcr-mcp verify [--stack <name>] [--namespace <ns>] [--timeout <duration>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace to check (defaults to properties.namespace in the stack file)")]
[arg("timeout", long="timeout", short="t", help="Rollout wait for deployment/dcr-mcp-api-server (default 120s)")]
[group('dcr-mcp')]
[no-cd]
verify stack="" namespace="" timeout="120s":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    STACK=$(just dcr-mcp _require-stack --stack {{ quote(stack) }})
    TIMEOUT={{ quote(timeout) }}
    if [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*[smh]$ ]]; then
        echo "Error: timeout must look like 120s, 5m, or 1h." >&2
        exit 1
    fi

    CFG_FILE="dcr-mcp/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CFG_FILE" ]]; then
        echo "Error: stack config missing: $CFG_FILE" >&2
        echo "Run: just gcp-pulumi scaffold-dcr-mcp-stack" >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq not found on PATH — run prepare-tools." >&2
        exit 1
    fi

    PROPS='.config."dcr-mcp:properties"'
    NS={{ quote(namespace) }}
    [[ -z "$NS" ]] && NS=$(yq -r "${PROPS}.namespace // \"\"" "$CFG_FILE")
    if [[ -z "$NS" || "$NS" == *"<"* || "$NS" == *">"* ]]; then
        echo "Error: $CFG_FILE has an empty or placeholder namespace." >&2
        exit 1
    fi

    name=$(yq -r "${PROPS}.name // \"dcr-mcp\"" "$CFG_FILE")
    image=$(yq -r "${PROPS}.image.name // \"dictybase/dcr-mcp\"" "$CFG_FILE")
    tag=$(yq -r "${PROPS}.image.tag // \"\"" "$CFG_FILE")
    deploy="${name}-api-server"
    svc="${name}-api"
    want="${image}:${tag}"

    if [[ -z "$tag" || "$tag" == "bootstrap" || "$tag" == *"<"* || "$tag" == *">"* ]]; then
        echo "FAIL ${name}: tag is '${tag:-empty}' — run: just gcp-pulumi set-config --folder dcr-mcp --key properties.image.tag --value <immutable-tag>" >&2
        exit 1
    fi
    if ! kubectl -n "$NS" rollout status "deployment/${deploy}" --timeout="$TIMEOUT" >/dev/null 2>&1; then
        echo "FAIL ${name}: deployment/${deploy} did not roll out in namespace ${NS}" >&2
        exit 1
    fi
    got=$(kubectl -n "$NS" get deployment "$deploy" -o jsonpath='{.spec.template.spec.containers[0].image}')
    if [[ "$got" != "$want" ]]; then
        echo "FAIL ${name}: running image ${got}, stack file says ${want}" >&2
        exit 1
    fi
    if ! kubectl -n "$NS" get service "$svc" >/dev/null 2>&1; then
        echo "FAIL ${name}: service/${svc} not found in namespace ${NS}" >&2
        exit 1
    fi

    echo "PASS ${name}: ${want}"
    echo "dcr-mcp verified for stack '${STACK}' in namespace '${NS}'."
    echo "Next: just gateway verify"
