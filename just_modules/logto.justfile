# Recipes for production Logto on the PostgreSQL cluster. Guide: docs/logto-deploy.md.
# Everything assumes the cluster-env sub-shell so Pulumi and Kubernetes target
# one cluster. No recipe creates namespaces or falls back to a dev stack.

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

# Check PostgreSQL, namespace ownership, Logto config, Secret and Service.
# Read-only. Production config must exist before a stack can be initialized.
# Usage: just logto check [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Logto and PostgreSQL namespace")]
[group('logto')]
[no-cd]
check stack="" namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="log-to"
    NS={{ quote(namespace) }}
    STACK=$(just logto _require-stack --stack {{ quote(stack) }})
    CFG_FILE="${FOLDER}/Pulumi.${STACK}.yaml"

    for var in PULUMI_BACKEND_URL PULUMI_GCP_CREDENTIALS PULUMI_SECRET_PROVIDER; do
        if [[ -z "${!var:-}" ]]; then
            echo "Error: $var is empty — enter the cluster-env sub-shell first." >&2
            exit 1
        fi
    done
    if [[ ! -f "${PULUMI_GCP_CREDENTIALS}" ]]; then
        echo "Error: Pulumi credentials missing: ${PULUMI_GCP_CREDENTIALS}" >&2
        exit 1
    fi
    if [[ ! -f "$CFG_FILE" ]]; then
        echo "Error: stack config missing: $CFG_FILE" >&2
        echo "Create it from docs/reference/logto/deployment.md before installing Logto." >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq not found on PATH — run prepare-tools." >&2
        exit 1
    fi

    config_name=$(yq -r '.config."log-to:properties".name // ""' "$CFG_FILE")
    config_namespace=$(yq -r '.config."log-to:properties".namespace // ""' "$CFG_FILE")
    database_secret=$(yq -r '.config."log-to:properties".databaseSecret // ""' "$CFG_FILE")
    image_name=$(yq -r '.config."log-to:properties".image.name // ""' "$CFG_FILE")
    image_tag=$(yq -r '.config."log-to:properties".image.tag // ""' "$CFG_FILE")
    endpoint=$(yq -r '.config."log-to:properties".endpoint // ""' "$CFG_FILE")
    ingress_host=$(yq -r '.config."log-to:properties".ingress.backendHosts[0] // ""' "$CFG_FILE")
    tls_secret=$(yq -r '.config."log-to:properties".ingress.tlsSecret // ""' "$CFG_FILE")

    if [[ "$config_name" != "logto" ]]; then
        echo "Error: $CFG_FILE must set name logto, got '$config_name'." >&2
        exit 1
    fi
    if [[ "$config_namespace" != "$NS" ]]; then
        echo "Error: $CFG_FILE sets namespace '$config_namespace', expected '$NS'." >&2
        exit 1
    fi
    if [[ "$database_secret" != "logto-app" ]]; then
        echo "Error: $CFG_FILE must use databaseSecret logto-app, got '$database_secret'." >&2
        exit 1
    fi
    if [[ -z "$image_name" || -z "$image_tag" || "$image_tag" == "latest" ]]; then
        echo "Error: $CFG_FILE must pin a non-latest Logto image tag." >&2
        exit 1
    fi
    for value in "$endpoint" "$ingress_host" "$tls_secret"; do
        if [[ -z "$value" || "$value" == *"<"* || "$value" == *">"* ]]; then
            echo "Error: $CFG_FILE contains an empty or placeholder production value." >&2
            exit 1
        fi
    done

    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    app_namespace=$(pulumi -C namespace-bootstrap stack output appNamespace --stack "$STACK")
    if [[ "$app_namespace" != "$NS" ]]; then
        echo "Error: namespace-bootstrap exports app namespace '$app_namespace', expected '$NS'." >&2
        exit 1
    fi

    just postgres verify --namespace "$NS"

    secret_json=$(kubectl get secret logto-app -n "$NS" -o json)
    if ! jq -e '(.data.username // "" | length > 0) and (.data.password // "" | length > 0)' <<<"$secret_json" >/dev/null; then
        echo "Error: Secret logto-app lacks username or password data in namespace '$NS'." >&2
        exit 1
    fi

    postgres_port=$(kubectl get service logto-rw -n "$NS" -o jsonpath='{.spec.ports[0].port}')
    if [[ "$postgres_port" != "5432" ]]; then
        echo "Error: Service logto-rw exposes '$postgres_port', expected 5432." >&2
        exit 1
    fi

    echo "Logto prerequisites ready for stack '$STACK'."

# Install Logto: check -> ensure stack -> preview -> apply -> rollout -> verify.
# create-resource runs `pulumi up -f -y`; preview is the review gate.
# Usage: just logto install [--stack <name>] [--namespace <ns>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Rollout retry count")]
[arg("interval", long="interval", short="i", help="Seconds between rollout retries")]
[arg("namespace", long="namespace", short="n", help="Logto namespace")]
[group('logto')]
[no-cd]
install stack="" namespace="prod" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="log-to"
    NS={{ quote(namespace) }}
    RETRIES={{ quote(retries) }}
    INTERVAL={{ quote(interval) }}
    if [[ ! "$RETRIES" =~ ^[1-9][0-9]*$ || ! "$INTERVAL" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: retries and interval must be positive integers." >&2
        exit 1
    fi
    STACK=$(just logto _require-stack --stack {{ quote(stack) }})

    just logto check --stack "$STACK" --namespace "$NS"
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    timeout_seconds=$((RETRIES * INTERVAL))
    kubectl rollout status deployment/logto -n "$NS" --timeout="${timeout_seconds}s"
    just logto verify --stack "$STACK" --namespace "$NS"

# Verify Logto Deployment, Pod, PVC, Services and Ingress.
# Usage: just logto verify [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Logto namespace")]
[group('logto')]
[no-cd]
verify stack="" namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    STACK=$(just logto _require-stack --stack {{ quote(stack) }})
    CFG_FILE="log-to/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CFG_FILE" ]]; then
        echo "Error: stack config missing: $CFG_FILE" >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq not found on PATH — run prepare-tools." >&2
        exit 1
    fi

    expected_host=$(yq -r '.config."log-to:properties".ingress.backendHosts[0] // ""' "$CFG_FILE")
    expected_tls=$(yq -r '.config."log-to:properties".ingress.tlsSecret // ""' "$CFG_FILE")
    failures=0
    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
    CHECK_COLOR=0

    deployment_json=$(kubectl get deployment logto -n "$NS" -o json 2>/dev/null || true)
    if [[ -z "$deployment_json" ]]; then
        bad "Deployment logto missing in $NS"
    else
        available=$(jq -r '.status.availableReplicas // 0' <<<"$deployment_json")
        if [[ "$available" -ge 1 ]]; then
            ok "Deployment logto has an available replica"
        else
            bad "Deployment logto has no available replica"
        fi
    fi

    ready_pods=$(kubectl get pods -n "$NS" -l app=logto -o json 2>/dev/null \
        | jq '[.items[] | select(.status.phase == "Running")
            | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)] | length' || echo 0)
    if [[ "$ready_pods" -ge 1 ]]; then
        ok "$ready_pods Logto pod(s) Running and ready"
    else
        bad "no ready Logto pod in $NS"
    fi

    pvc_phase=$(kubectl get pvc logto-claim -n "$NS" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    if [[ "$pvc_phase" == "Bound" ]]; then
        ok "PVC logto-claim Bound"
    else
        bad "PVC logto-claim not Bound (status: ${pvc_phase:-missing})"
    fi

    for service_port in "logto-api:3001" "logto-admin:3002"; do
        service_name="${service_port%%:*}"
        expected_port="${service_port##*:}"
        actual_port=$(kubectl get service "$service_name" -n "$NS" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || true)
        if [[ "$actual_port" == "$expected_port" ]]; then
            ok "Service $service_name exposes $expected_port"
        else
            bad "Service $service_name exposes '${actual_port:-missing}', expected $expected_port"
        fi
    done

    ingress_json=$(kubectl get ingress logto-ingress -n "$NS" -o json 2>/dev/null || true)
    if [[ -z "$ingress_json" ]]; then
        bad "Ingress logto-ingress missing"
    else
        if jq -e --arg host "$expected_host" 'any(.spec.rules[]?; .host == $host)' <<<"$ingress_json" >/dev/null; then
            ok "Ingress host $expected_host configured"
        else
            bad "Ingress host $expected_host missing"
        fi
        if jq -e --arg secret "$expected_tls" 'any(.spec.tls[]?; .secretName == $secret)' <<<"$ingress_json" >/dev/null; then
            ok "Ingress TLS Secret $expected_tls configured"
        else
            bad "Ingress TLS Secret $expected_tls missing"
        fi
    fi

    echo
    kubectl logs deployment/logto -n "$NS" --tail=100 2>/dev/null || true
    echo
    if [[ "$failures" -ne 0 ]]; then
        printf '%d Logto check(s) failed. See docs/reference/logto/deployment.md.\n' "$failures"
        exit 1
    fi
    printf 'Logto checks passed.\n'
