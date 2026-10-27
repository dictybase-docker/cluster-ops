# Recipes for Cilium Gateway API and cert-manager routing. Guide: docs/gateway-deploy.md.
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

# Install Kubernetes Gateway API standard CRDs and ensure cert-manager enables
# Gateway API support (--enable-gateway-api).
# Usage: just gateway install-crds [--version <v>]
[arg("version", long="version", short="v", help="Gateway API release version (default v1.2.1)")]
[group('gateway')]
[no-cd]
install-crds version="v1.2.1":
    #!/usr/bin/env bash
    set -euo pipefail
    ver={{ quote(version) }}
    if [[ ! "$ver" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "Error: version must match v<major>.<minor>.<patch>, got '$ver'." >&2
        exit 1
    fi
    url="https://github.com/kubernetes-sigs/gateway-api/releases/download/${ver}/standard-install.yaml"
    echo "Applying Gateway API standard CRDs (${ver})..."
    kubectl apply -f "$url"
    kubectl get crd gateways.gateway.networking.k8s.io >/dev/null
    kubectl get crd httproutes.gateway.networking.k8s.io >/dev/null

    cm_ns=""
    for candidate in kube-system cert-manager; do
        if kubectl -n "$candidate" get deployment cert-manager >/dev/null 2>&1; then
            cm_ns="$candidate"
            break
        fi
    done
    if [[ -n "$cm_ns" ]]; then
        cm_json=$(kubectl -n "$cm_ns" get deployment cert-manager -o json)
        if ! jq -e 'any(.spec.template.spec.containers[0].args[]?; startswith("--enable-gateway-api"))' <<<"$cm_json" >/dev/null; then
            echo "Enabling --enable-gateway-api on cert-manager in namespace '$cm_ns'..."
            kubectl -n "$cm_ns" patch deployment cert-manager --type='json' \
                -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--enable-gateway-api"}]'
            kubectl -n "$cm_ns" rollout status deployment/cert-manager --timeout=180s
        fi
    fi
    echo "Gateway API CRDs (${ver}) ready. Next: just gateway check"

# Restart the Cilium agent and Cilium operator in kube-system and verify GatewayClass.
# Usage: just gateway restart-cilium [--class <name>] [--retries <n>] [--interval <s>]
[arg("class", long="class", short="c", help="GatewayClass name (default cilium)")]
[arg("retries", long="retries", short="r", help="GatewayClass poll attempts (default 18)")]
[arg("interval", long="interval", short="i", help="Seconds between GatewayClass polls (default 5)")]
[group('gateway')]
[no-cd]
restart-cilium class="cilium" retries="18" interval="5":
    #!/usr/bin/env bash
    set -euo pipefail

    gw_class={{ quote(class) }}
    RETRIES={{ quote(retries) }}
    INTERVAL={{ quote(interval) }}
    if [[ ! "$RETRIES" =~ ^[1-9][0-9]*$ || ! "$INTERVAL" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: retries and interval must be positive integers." >&2
        exit 1
    fi

    echo "Restarting Cilium operator in kube-system..."
    kubectl -n kube-system rollout restart deployment/cilium-operator

    ds_strategy=$(kubectl -n kube-system get daemonset cilium -o jsonpath='{.spec.updateStrategy.type}' 2>/dev/null || true)
    if [[ "$ds_strategy" == "RollingUpdate" ]]; then
        echo "Restarting Cilium agent daemonset in kube-system..."
        kubectl -n kube-system rollout restart daemonset/cilium
        echo "Waiting for Cilium agent rollout..."
        kubectl -n kube-system rollout status daemonset/cilium --timeout=180s
    fi

    echo "Waiting for Cilium operator rollout..."
    kubectl -n kube-system rollout status deployment/cilium-operator --timeout=180s

    echo "Verifying GatewayClass '$gw_class'..."
    accepted=0
    for i in $(seq 1 "$RETRIES"); do
        gwclass_json=$(kubectl get gatewayclass "$gw_class" -o json 2>/dev/null || true)
        if [[ -n "$gwclass_json" ]] && jq -e 'any(.status.conditions[]?; .type == "Accepted" and .status == "True")' <<<"$gwclass_json" >/dev/null 2>&1; then
            accepted=1
            break
        fi
        echo "Waiting for GatewayClass '$gw_class' to report condition Accepted=True (try $i/$RETRIES)..."
        sleep "$INTERVAL"
    done

    if [[ "$accepted" -ne 1 ]]; then
        echo "Error: GatewayClass '$gw_class' not found or not Accepted by Cilium." >&2
        echo "Verify that spec.networking.cilium.gatewayAPI.enabled is true in cluster.yaml and run 'just gcp-cluster apply-cluster'." >&2
        exit 1
    fi

    echo "GatewayClass '$gw_class' is Accepted by Cilium."
    echo "Next: just gateway deploy"

# Read-only preflight: Pulumi env, stack config, namespace-bootstrap match,
# GatewayClass cilium Accepted, cert-manager Gateway API flag, and backend Services
# for enabled routes.
# Usage: just gateway check [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Gateway and service namespace")]
[group('gateway')]
[no-cd]
check stack="" namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="gateway"
    NS={{ quote(namespace) }}
    STACK=$(just gateway _require-stack --stack {{ quote(stack) }})
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
        echo "Run: just gcp-pulumi scaffold-gateway-stack" >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq not found on PATH — run prepare-tools." >&2
        exit 1
    fi

    gw_name=$(yq -r '.config."gateway:properties".name // ""' "$CFG_FILE")
    gw_ns=$(yq -r '.config."gateway:properties".namespace // ""' "$CFG_FILE")
    gw_class=$(yq -r '.config."gateway:properties".gatewayClassName // ""' "$CFG_FILE")
    issuer_email=$(yq -r '.config."gateway:properties".issuer.email // ""' "$CFG_FILE")
    issuer_secret=$(yq -r '.config."gateway:properties".issuer.secretName // ""' "$CFG_FILE")

    if [[ -z "$gw_name" || -z "$gw_class" ]]; then
        echo "Error: $CFG_FILE must set name and gatewayClassName." >&2
        exit 1
    fi
    if [[ "$gw_ns" != "$NS" ]]; then
        echo "Error: $CFG_FILE sets namespace '$gw_ns', expected '$NS'." >&2
        exit 1
    fi
    for val in "$issuer_email" "$issuer_secret"; do
        if [[ -z "$val" || "$val" == *"<"* || "$val" == *">"* ]]; then
            echo "Error: $CFG_FILE contains an empty or placeholder issuer value." >&2
            exit 1
        fi
    done

    enabled_count=0
    for route in frontend graphql minio logto; do
        en=$(yq -r ".config.\"gateway:properties\".routes.${route}.enabled // \"false\"" "$CFG_FILE")
        if [[ "$en" == "true" ]]; then
            enabled_count=$((enabled_count + 1))
            host_count=$(yq -r ".config.\"gateway:properties\".routes.${route}.hosts | length" "$CFG_FILE")
            tls=$(yq -r ".config.\"gateway:properties\".routes.${route}.tlsSecret // \"\"" "$CFG_FILE")
            if [[ "$host_count" -eq 0 || -z "$tls" || "$tls" == *"<"* || "$tls" == *">"* ]]; then
                echo "Error: $CFG_FILE route '$route' is enabled but contains an empty or placeholder host or tlsSecret." >&2
                exit 1
            fi
            while IFS= read -r host; do
                if [[ -z "$host" || "$host" == *"<"* || "$host" == *">"* ]]; then
                    echo "Error: $CFG_FILE route '$route' is enabled but contains an empty or placeholder host or tlsSecret." >&2
                    exit 1
                fi
            done < <(yq -r ".config.\"gateway:properties\".routes.${route}.hosts[]" "$CFG_FILE")
        fi
    done
    if [[ "$enabled_count" -eq 0 ]]; then
        echo "Error: $CFG_FILE must enable at least one route." >&2
        exit 1
    fi

    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    app_namespace=$(pulumi -C namespace-bootstrap stack output appNamespace --stack "$STACK")
    if [[ "$app_namespace" != "$NS" ]]; then
        echo "Error: namespace-bootstrap exports app namespace '$app_namespace', expected '$NS'." >&2
        exit 1
    fi

    gwclass_json=$(kubectl get gatewayclass "$gw_class" -o json 2>/dev/null || true)
    if [[ -z "$gwclass_json" ]]; then
        echo "Error: GatewayClass $gw_class not found on cluster — enable Cilium gatewayAPI, run 'just gateway install-crds', and run 'just gateway restart-cilium'." >&2
        exit 1
    fi
    if ! jq -e 'any(.status.conditions[]?; .type == "Accepted" and .status == "True")' <<<"$gwclass_json" >/dev/null; then
        echo "Error: GatewayClass $gw_class is not Accepted by Cilium." >&2
        exit 1
    fi

    fe_en=$(yq -r '.config."gateway:properties".routes.frontend.enabled // "false"' "$CFG_FILE")
    if [[ "$fe_en" == "true" ]]; then
        for svc in $(yq -r '.config."gateway:properties".routes.frontend.services[].name' "$CFG_FILE"); do
            kubectl -n "$NS" get service "$svc" >/dev/null
        done
    fi
    for route in graphql minio logto; do
        en=$(yq -r ".config.\"gateway:properties\".routes.${route}.enabled // \"false\"" "$CFG_FILE")
        if [[ "$en" == "true" ]]; then
            svc=$(yq -r ".config.\"gateway:properties\".routes.${route}.serviceName" "$CFG_FILE")
            kubectl -n "$NS" get service "$svc" >/dev/null
        fi
    done

    echo "Gateway prerequisites ready for stack '$STACK'."

# Deploy Gateway: check -> ensure-stack -> preview -> create-resource -> wait for external IP.
# Does not check TLS Secrets yet because DNS A records must point to the Gateway
# external IP before cert-manager can complete HTTP-01 challenges. Run
# `just gateway verify` after updating DNS.
# Usage: just gateway deploy [--stack <name>] [--namespace <ns>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Gateway namespace")]
[arg("retries", long="retries", short="r", help="Address poll attempts (default 30)")]
[arg("interval", long="interval", short="i", help="Seconds between address polls (default 10)")]
[group('gateway')]
[no-cd]
deploy stack="" namespace="prod" retries="30" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="gateway"
    NS={{ quote(namespace) }}
    RETRIES={{ quote(retries) }}
    INTERVAL={{ quote(interval) }}
    if [[ ! "$RETRIES" =~ ^[1-9][0-9]*$ || ! "$INTERVAL" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: retries and interval must be positive integers." >&2
        exit 1
    fi
    STACK=$(just gateway _require-stack --stack {{ quote(stack) }})
    CFG_FILE="${FOLDER}/Pulumi.${STACK}.yaml"

    just gateway check --stack "$STACK" --namespace "$NS"
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    gw_name=$(yq -r '.config."gateway:properties".name // "dictycr-gateway"' "$CFG_FILE")
    addr=""
    for i in $(seq 1 "$RETRIES"); do
        addr=$(kubectl get gateway "$gw_name" -n "$NS" -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || true)
        if [[ -n "$addr" ]]; then
            echo "Gateway $gw_name external IP: $addr"
            break
        fi
        echo "Waiting for Gateway $gw_name external IP (try $i/$RETRIES)..."
        sleep "$INTERVAL"
    done
    if [[ -z "$addr" ]]; then
        echo "Error: Gateway $gw_name did not receive an external IP in $NS." >&2
        exit 1
    fi
    echo "Point your DNS A records for enabled route hostnames to $addr, then run:"
    echo "Next: just gateway verify"

# Print the external IP address assigned to the Gateway.
# Usage: just gateway show-ip [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Gateway namespace")]
[group('gateway')]
[no-cd]
show-ip stack="" namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    STACK=$(just gateway _require-stack --stack {{ quote(stack) }})
    CFG_FILE="gateway/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CFG_FILE" ]]; then
        echo "Error: stack config missing: $CFG_FILE" >&2
        exit 1
    fi
    gw_name=$(yq -r '.config."gateway:properties".name // "dictycr-gateway"' "$CFG_FILE")
    addr=$(kubectl get gateway "$gw_name" -n "$NS" -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || true)
    if [[ -z "$addr" ]]; then
        echo "Error: Gateway $gw_name has no external address yet in $NS." >&2
        exit 1
    fi
    printf '%s\n' "$addr"

# Verify Issuer, Gateway, TLS Secrets, and HTTPRoutes for enabled routes.
# Usage: just gateway verify [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Gateway namespace")]
[group('gateway')]
[no-cd]
verify stack="" namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    STACK=$(just gateway _require-stack --stack {{ quote(stack) }})
    CFG_FILE="gateway/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CFG_FILE" ]]; then
        echo "Error: stack config missing: $CFG_FILE" >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq not found on PATH — run prepare-tools." >&2
        exit 1
    fi

    gw_name=$(yq -r '.config."gateway:properties".name // "dictycr-gateway"' "$CFG_FILE")
    issuer_name=$(yq -r '.config."gateway:properties".issuer.name // "letsencrypt-issuer"' "$CFG_FILE")
    failures=0
    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
    CHECK_COLOR=0

    issuer_json=$(kubectl get issuer "$issuer_name" -n "$NS" -o json 2>/dev/null || true)
    if [[ -z "$issuer_json" ]]; then
        bad "Issuer $issuer_name missing in $NS"
    elif jq -e 'any(.status.conditions[]?; .type == "Ready" and .status == "True")' <<<"$issuer_json" >/dev/null; then
        ok "Issuer $issuer_name is Ready"
    else
        bad "Issuer $issuer_name is not Ready"
    fi

    gw_json=$(kubectl get gateway "$gw_name" -n "$NS" -o json 2>/dev/null || true)
    if [[ -z "$gw_json" ]]; then
        bad "Gateway $gw_name missing in $NS"
    else
        if jq -e 'any(.status.conditions[]?; .type == "Programmed" and .status == "True")' <<<"$gw_json" >/dev/null; then
            ok "Gateway $gw_name is Programmed"
        else
            bad "Gateway $gw_name is not Programmed"
        fi
        addr=$(jq -r '.status.addresses[0].value // ""' <<<"$gw_json")
        if [[ -n "$addr" ]]; then
            ok "Gateway $gw_name has address $addr"
        else
            bad "Gateway $gw_name has no external address yet"
        fi
    fi

    for route in frontend graphql minio logto; do
        en=$(yq -r ".config.\"gateway:properties\".routes.${route}.enabled // \"false\"" "$CFG_FILE")
        if [[ "$en" != "true" ]]; then
            continue
        fi
        expected_tls=$(yq -r ".config.\"gateway:properties\".routes.${route}.tlsSecret // \"\"" "$CFG_FILE")
        route_name="${route}-route"

        hr_json=$(kubectl get httproute "$route_name" -n "$NS" -o json 2>/dev/null || true)
        if [[ -z "$hr_json" ]]; then
            bad "HTTPRoute $route_name missing in $NS"
        else
            while IFS= read -r expected_host; do
                [[ -z "$expected_host" ]] && continue
                if jq -e --arg h "$expected_host" 'any(.spec.hostnames[]?; . == $h)' <<<"$hr_json" >/dev/null; then
                    ok "HTTPRoute $route_name matches host $expected_host"
                else
                    bad "HTTPRoute $route_name missing host $expected_host"
                fi
            done < <(yq -r ".config.\"gateway:properties\".routes.${route}.hosts[]" "$CFG_FILE")
            if jq -e 'any(.status.parents[]?.conditions[]?; .type == "Accepted" and .status == "True")' <<<"$hr_json" >/dev/null; then
                ok "HTTPRoute $route_name is Accepted"
            else
                bad "HTTPRoute $route_name is not Accepted"
            fi
        fi

        tls_type=$(kubectl get secret "$expected_tls" -n "$NS" -o jsonpath='{.type}' 2>/dev/null || true)
        if [[ "$tls_type" == "kubernetes.io/tls" ]]; then
            ok "TLS Secret $expected_tls exists"
        else
            bad "TLS Secret $expected_tls missing or not kubernetes.io/tls"
        fi
    done

    echo
    if [[ "$failures" -ne 0 ]]; then
        printf '%d Gateway check(s) failed. See docs/reference/gateway/deployment.md.\n' "$failures"
        exit 1
    fi
    printf 'Gateway checks passed.\n'
