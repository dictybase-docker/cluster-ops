# Recipes for the frontend_application Pulumi project. Guide: docs/frontend-deploy.md.
# Everything assumes the cluster-env sub-shell so Pulumi and Kubernetes target
# one cluster. Every value comes from frontend_application/Pulumi.<stack>.yaml.

# Post-deploy check: for each app in the stack file, the Deployment rolled out,
# the running image equals <image>:<tag> from the stack file, and the Service
# exists. Read-only. Exits non-zero when any app fails.
# Usage: just frontend verify [--stack <name>] [--namespace <ns>] [--timeout <duration>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace to check (defaults to properties.namespace in the stack file)")]
[arg("timeout", long="timeout", short="t", help="Rollout wait per Deployment (default 120s)")]
[group('frontend')]
[no-cd]
verify stack="" namespace="" timeout="120s":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    STACK={{ quote(stack) }}
    [[ -z "$STACK" ]] && STACK="${PULUMI_STACK:-}"
    if [[ -z "$STACK" ]]; then
        echo "Error: no stack name — set PULUMI_STACK (via cluster env) or pass --stack." >&2
        exit 1
    fi
    if [[ ! "$STACK" =~ ^[A-Za-z0-9._-]+$ ]]; then
        echo "Error: invalid stack name '$STACK'." >&2
        exit 1
    fi
    TIMEOUT={{ quote(timeout) }}
    if [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*[smh]$ ]]; then
        echo "Error: timeout must look like 120s, 5m, or 1h." >&2
        exit 1
    fi

    CFG_FILE="frontend_application/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CFG_FILE" ]]; then
        echo "Error: stack config missing: $CFG_FILE" >&2
        echo "Run: just gcp-pulumi scaffold-frontend-stack" >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq not found on PATH — run prepare-tools." >&2
        exit 1
    fi

    PROPS='.config."frontend_application:properties"'
    NS={{ quote(namespace) }}
    [[ -z "$NS" ]] && NS=$(yq -r "${PROPS}.namespace // \"\"" "$CFG_FILE")
    if [[ -z "$NS" || "$NS" == *"<"* || "$NS" == *">"* ]]; then
        echo "Error: $CFG_FILE has an empty or placeholder namespace." >&2
        exit 1
    fi
    count=$(yq -r "${PROPS}.apps | length" "$CFG_FILE")
    if [[ ! "$count" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: $CFG_FILE lists no apps." >&2
        exit 1
    fi

    failed=0
    for ((i = 0; i < count; i++)); do
        name=$(yq -r "${PROPS}.apps[$i].name" "$CFG_FILE")
        image=$(yq -r "${PROPS}.apps[$i].image" "$CFG_FILE")
        tag=$(yq -r "${PROPS}.apps[$i].tag" "$CFG_FILE")
        deploy="${name}-api-server"
        svc="${name}-api"
        want="${image}:${tag}"

        if [[ "$tag" == "bootstrap" ]]; then
            echo "FAIL ${name}: tag is the placeholder 'bootstrap' — run: just gcp-pulumi set-config --folder frontend_application --key properties.apps.${i}.tag --value <immutable-tag>" >&2
            failed=$((failed + 1))
            continue
        fi
        if ! kubectl -n "$NS" rollout status "deployment/${deploy}" --timeout="$TIMEOUT" >/dev/null 2>&1; then
            echo "FAIL ${name}: deployment/${deploy} did not roll out in namespace ${NS}" >&2
            failed=$((failed + 1))
            continue
        fi
        got=$(kubectl -n "$NS" get deployment "$deploy" -o jsonpath='{.spec.template.spec.containers[0].image}')
        if [[ "$got" != "$want" ]]; then
            echo "FAIL ${name}: running image ${got}, stack file says ${want}" >&2
            failed=$((failed + 1))
            continue
        fi
        if ! kubectl -n "$NS" get service "$svc" >/dev/null 2>&1; then
            echo "FAIL ${name}: service/${svc} not found in namespace ${NS}" >&2
            failed=$((failed + 1))
            continue
        fi
        echo "PASS ${name}: ${want}"
    done

    if [[ "$failed" -ne 0 ]]; then
        echo "Error: ${failed} of ${count} app(s) failed. See reference/frontend/deployment.md#troubleshooting." >&2
        exit 1
    fi
    echo "Frontend applications verified for stack '${STACK}' in namespace '${NS}'."
    echo "Next: just gateway verify"
