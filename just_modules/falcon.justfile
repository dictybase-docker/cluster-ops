# Recipes for the CrowdStrike Falcon node sensor DaemonSet (falcon-helm chart
# falcon-sensor). Guide: docs/falcon-deploy.md. Everything assumes the
# cluster-env sub-shell (just cluster-env) so PULUMI_STACK, PROJECT_ID,
# GOOGLE_APPLICATION_CREDENTIALS (sa-manager), PULUMI_GCP_CREDENTIALS, and
# KUBECONFIG are set. The Falcon API client comes from FALCON_CLIENT_ID and
# FALCON_CLIENT_SECRET in the environment, never from flags.

# Print the node sensor image tags that the CrowdStrike registry offers to
# this tenant. Read-only.
# Usage: just falcon list-tags [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('falcon')]
[no-cd]
list-tags stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/falcon.sh
    fc_require_tools curl jq yq skopeo
    fc_resolve {{ quote(stack) }} ""
    fc_require_cloud
    fc_require_api_creds
    fc_tmpdir
    fc_registry_login
    skopeo list-tags --authfile "$WORK/auth.json" "docker://${FC_SOURCE_REPO}" | jq -r '.Tags[]' | sort -V

# Copy one node sensor image from registry.crowdstrike.com into the project
# Artifact Registry, grant the node service accounts read access, and pin the
# digest in the stack config. Every check runs before the first change.
# Usage: just falcon mirror-image --tag <sensor-tag> [--stack <name>]
[arg("tag", long="tag", short="t", help="Sensor image tag from 'just falcon list-tags' (7.40 or later for chart 1.37)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('falcon')]
[no-cd]
mirror-image tag="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/falcon.sh

    # ── preflight: no change before this block ends ──────────────────────
    TAG={{ quote(tag) }}
    fc_require_tag "$TAG"
    fc_require_tools curl jq yq skopeo gcloud kubectl
    fc_resolve {{ quote(stack) }} ""
    fc_require_project
    fc_require_cluster
    fc_require_manager_key
    fc_require_cloud
    fc_require_api_creds
    LOCATION=$(fc_prop registry.location)
    REPO=$(fc_prop registry.repository)
    [[ "$LOCATION" =~ ^[a-z]+-[a-z]+[0-9]+$ ]] || fc_die "properties.registry.location '$LOCATION' in $CFG is not a GCP region."
    [[ "$REPO" =~ ^[a-z][a-z0-9-]{0,62}$ ]] || fc_die "properties.registry.repository '$REPO' in $CFG is not a valid repository id."
    HOST="${LOCATION}-docker.pkg.dev"
    DEST_REPO="${HOST}/${PROJECT}/${REPO}/falcon-sensor"
    fc_tmpdir

    NODE_SAS=$(fc_node_service_accounts) || exit 1
    [[ -n "$NODE_SAS" ]] || fc_die "found no node service accounts."
    echo "Node service accounts: $(tr '\n' ' ' <<<"$NODE_SAS")"

    fc_registry_login
    if fc_has_secret cid; then
        STACK_CID=$(GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}" \
            pulumi -C "$FC_FOLDER" -s "$STACK" config get --path properties.cid)
        [[ "$STACK_CID" == "$FC_CID" ]] ||
            fc_die "properties.cid on stack '$STACK' is not the CID of the tenant that owns this API client."
    fi
    SRC_DIGEST=$(skopeo inspect --authfile "$WORK/auth.json" "docker://${FC_SOURCE_REPO}:${TAG}" | jq -r '.Digest // empty') ||
        fc_die "tag '$TAG' is not in ${FC_SOURCE_REPO}. List the tags with: just falcon list-tags"
    [[ "$SRC_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]] || fc_die "could not read the digest of ${FC_SOURCE_REPO}:${TAG}."
    echo "Source: ${FC_SOURCE_REPO}:${TAG}@${SRC_DIGEST}"

    # ── changes ──────────────────────────────────────────────────────────
    just gcp-pulumi ensure-stack --folder "$FC_FOLDER" --stack "$STACK"

    if gcloud artifacts repositories describe "$REPO" --location "$LOCATION" --project "$PROJECT" \
        --format 'value(name)' >/dev/null 2>"$WORK/err"; then
        echo "Artifact Registry repository $REPO exists — continuing."
    elif grep -Eq 'NOT_FOUND|not found' "$WORK/err"; then
        gcloud artifacts repositories create "$REPO" --repository-format docker \
            --location "$LOCATION" --project "$PROJECT" \
            --description "Mirror of the CrowdStrike Falcon node sensor image"
    else
        echo "Error: could not describe Artifact Registry repository $REPO:" >&2
        cat "$WORK/err" >&2
        grep -q 'SERVICE_DISABLED' "$WORK/err" &&
            echo "Enable artifactregistry.googleapis.com with: just gcp-api enable-apis --api-file <file>" >&2
        exit 1
    fi

    gcloud artifacts repositories get-iam-policy "$REPO" --location "$LOCATION" --project "$PROJECT" \
        --format json >"$WORK/policy.json"
    while IFS= read -r sa; do
        member="serviceAccount:${sa}"
        if MEMBER="$member" jq -e '[.bindings[]? | select(.role == "roles/artifactregistry.reader") | .members[]] | index(env.MEMBER)' \
            "$WORK/policy.json" >/dev/null; then
            echo "$sa can read $REPO — continuing."
        else
            gcloud artifacts repositories add-iam-policy-binding "$REPO" --location "$LOCATION" \
                --project "$PROJECT" --member "$member" --role roles/artifactregistry.reader >/dev/null
            echo "Granted roles/artifactregistry.reader on $REPO to $sa."
        fi
    done <<<"$NODE_SAS"

    fc_add_gcp_auth "$HOST"
    skopeo copy --all --preserve-digests --authfile "$WORK/auth.json" \
        "docker://${FC_SOURCE_REPO}:${TAG}" "docker://${DEST_REPO}:${TAG}"
    DEST_DIGEST=$(skopeo inspect --authfile "$WORK/auth.json" "docker://${DEST_REPO}:${TAG}" | jq -r '.Digest // empty')
    [[ "$DEST_DIGEST" == "$SRC_DIGEST" ]] ||
        fc_die "mirror digest '${DEST_DIGEST}' does not match source digest '${SRC_DIGEST}'. The stack config is unchanged."

    just gcp-pulumi set-config --folder "$FC_FOLDER" --stack "$STACK" --key properties.image.repository --value "$DEST_REPO"
    just gcp-pulumi set-config --folder "$FC_FOLDER" --stack "$STACK" --key properties.image.tag --value "$TAG"
    just gcp-pulumi set-config --folder "$FC_FOLDER" --stack "$STACK" --key properties.image.digest --value "$DEST_DIGEST"
    echo "Mirrored ${DEST_REPO}:${TAG}@${DEST_DIGEST}. Commit $CFG."
    echo "Next: just falcon deploy"

# Deploy the falcon-sensor stack, then wait until one sensor pod is ready on
# every node. Fails before any change when the CID, the cloud region, or the
# mirrored image is missing from the stack config.
# Usage: just falcon deploy [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace; a mismatch fails)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('falcon')]
[no-cd]
deploy namespace="" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/falcon.sh

    fc_require_tools jq yq kubectl
    fc_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fc_require_project
    fc_require_cloud
    fc_has_secret cid || fc_die "properties.cid is not set. Run: just gcp-pulumi set-secret --folder falcon-sensor --key properties.cid"
    [[ "$(fc_prop image.digest)" =~ ^sha256:[0-9a-f]{64}$ ]] ||
        fc_die "properties.image.digest is not set. Run: just falcon mirror-image --tag <sensor-tag>"
    [[ "$(fc_prop image.repository)" == *"-docker.pkg.dev/${PROJECT}/"* ]] ||
        fc_die "properties.image.repository is not an Artifact Registry path in project '$PROJECT'. Run: just falcon mirror-image --tag <sensor-tag>"
    RETRIES={{ quote(retries) }}
    INTERVAL={{ quote(interval) }}
    [[ "$RETRIES" =~ ^[0-9]+$ && "$INTERVAL" =~ ^[0-9]+$ ]] || fc_die "--retries and --interval must be whole numbers."
    fc_require_cluster

    echo "Deploying $FC_FOLDER (stack '$STACK') into namespace '$NS' on $NODES nodes..."
    just gcp-pulumi ensure-stack --folder "$FC_FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FC_FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FC_FOLDER" --stack "$STACK"

    DS=$(GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}" \
        pulumi -C "$FC_FOLDER" -s "$STACK" stack output daemonSet)
    [[ -n "$DS" ]] || fc_die "stack '$STACK' has no daemonSet output after apply."
    converged=0
    for ((i = 1; i <= RETRIES; i++)); do
        status=$(kubectl -n "$NS" get daemonset "$DS" -o json 2>/dev/null || echo '{}')
        desired=$(jq '.status.desiredNumberScheduled // 0' <<<"$status")
        ready=$(jq '.status.numberReady // 0' <<<"$status")
        if [[ "$desired" -eq "$NODES" && "$ready" -eq "$desired" ]]; then
            converged=1
            echo "Falcon sensor ready on $ready/$NODES nodes."
            break
        fi
        if kubectl -n "$NS" get pods -o json 2>/dev/null |
            jq -e '[.items[].status.containerStatuses[]?.state.waiting.reason // empty] | any(. == "ErrImagePull" or . == "ImagePullBackOff")' >/dev/null; then
            echo "Warning: a sensor pod cannot pull its image. See docs/reference/falcon/troubleshooting.md#image-pull-fails." >&2
        fi
        echo "Waiting for sensor pods: $ready ready, $desired scheduled, $NODES nodes (try $i/$RETRIES)..."
        sleep "$INTERVAL"
    done
    if [[ "$converged" -ne 1 ]]; then
        kubectl -n "$NS" get pods -o wide >&2 || true
        fc_die "the Falcon sensor is not ready on every node. Run: just falcon verify"
    fi

# Read-only post-install check: one ready sensor pod on every node, the
# mirrored digest in use, and a Falcon agent ID (AID) in every pod. An AID
# shows that the sensor registered with the CrowdStrike cloud.
# Usage: just falcon verify [--namespace <ns>] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace; a mismatch fails)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('falcon')]
[no-cd]
verify namespace="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/falcon.sh

    fc_require_tools jq yq kubectl
    fc_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fc_require_project
    fc_require_cluster
    DS=$(GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}" \
        pulumi -C "$FC_FOLDER" -s "$STACK" stack output daemonSet 2>/dev/null || true)
    [[ -n "$DS" ]] || fc_die "stack '$STACK' has no daemonSet output. Run: just falcon deploy"
    WANT_IMAGE="$(fc_prop image.repository)@$(fc_prop image.digest)"
    fails=0
    ok() { printf '  ok    %s\n' "$*"; }
    bad() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

    status=$(kubectl -n "$NS" get daemonset "$DS" -o json 2>/dev/null || echo '{}')
    desired=$(jq '.status.desiredNumberScheduled // 0' <<<"$status")
    ready=$(jq '.status.numberReady // 0' <<<"$status")
    if [[ "$NODES" -gt 0 && "$desired" -eq "$NODES" ]]; then ok "DaemonSet $DS scheduled on $desired/$NODES nodes"
    else bad "DaemonSet $DS scheduled on $desired/$NODES nodes"; fi
    if [[ "$desired" -gt 0 && "$ready" -eq "$desired" ]]; then ok "$ready/$desired sensor pods ready"
    else bad "$ready/$desired sensor pods ready"; fi
    image=$(jq -r '.spec.template.spec.containers[] | select(.name == "falcon-node-sensor") | .image' <<<"$status")
    if [[ "$image" == "$WANT_IMAGE" ]]; then ok "image $image"
    else bad "image '${image}' is not the mirrored '${WANT_IMAGE}'"; fi

    selector=$(jq -r '.spec.selector.matchLabels // {} | to_entries | map("\(.key)=\(.value)") | join(",")' <<<"$status")
    [[ -n "$selector" ]] || selector="app.kubernetes.io/instance=$DS"
    pods=$(kubectl -n "$NS" get pods -l "$selector" -o json |
        jq -r '.items[] | "\(.metadata.name) \(.spec.nodeName // "-")"') || pods=""
    count=$(grep -c . <<<"$pods" || true)
    if [[ "$count" -eq "$NODES" ]]; then ok "$count sensor pods for $NODES nodes"
    else bad "$count sensor pods for $NODES nodes"; fi
    while read -r pod node; do
        [[ -n "$pod" ]] || continue
        aid=$(kubectl -n "$NS" exec "$pod" -c falcon-node-sensor -- \
            sh -c 'if command -v falconctl >/dev/null 2>&1; then falconctl -g --aid; else /opt/CrowdStrike/falconctl -g --aid; fi' 2>/dev/null |
            grep -Eo '[0-9a-fA-F]{32}' | head -n 1 || true)
        if [[ -n "$aid" && "$aid" != 00000000000000000000000000000000 ]]; then ok "$node: agent ID $aid"
        else bad "$node: pod $pod has no agent ID (sensor not registered with the CrowdStrike cloud)"; fi
    done <<<"$pods"

    if [[ "$fails" -gt 0 ]]; then
        echo "ERROR: $fails check(s) failed. See docs/reference/falcon/troubleshooting.md." >&2
        exit 1
    fi
    echo "All checks passed. An agent ID shows registration, not current protection."
    echo "Confirm the hosts in the Falcon console: Host setup and management > Host management."

# Print the sensor container log of the pod on one node. Read-only.
# Usage: just falcon logs --node <node-name> [--namespace <ns>] [--stack <name>] [--tail <n>]
[arg("node", long="node", help="Node name from 'just falcon verify'")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace; a mismatch fails)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("tail", long="tail", help="Lines to print (default 200)")]
[group('falcon')]
[no-cd]
logs node="" namespace="" stack="" tail="200":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/falcon.sh
    NODE={{ quote(node) }}
    TAIL={{ quote(tail) }}
    [[ -n "$NODE" ]] || fc_die "--node is required. List the nodes with: just falcon verify"
    [[ "$TAIL" =~ ^[0-9]+$ ]] || fc_die "--tail must be a whole number."
    fc_require_tools jq yq kubectl
    fc_resolve {{ quote(stack) }} {{ quote(namespace) }}
    POD=$(kubectl -n "$NS" get pods -o json |
        NODE="$NODE" jq -r '[.items[] | select(.spec.nodeName == env.NODE and any(.spec.containers[]; .name == "falcon-node-sensor"))][0].metadata.name // empty')
    [[ -n "$POD" ]] || fc_die "no sensor pod on node '$NODE' in namespace '$NS'."
    kubectl -n "$NS" logs "$POD" -c falcon-node-sensor --tail "$TAIL"

# Destroy the falcon-sensor stack: the Helm release, Secret
# falcon-sensor-cid, and the namespace. The chart cleanup hook removes
# /opt/CrowdStrike from each node. The Artifact Registry mirror stays.
# Usage: just falcon teardown [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('falcon')]
[no-cd]
teardown stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/falcon.sh
    fc_resolve {{ quote(stack) }} ""
    fc_require_project
    fc_require_cluster
    echo "Destroying falcon-sensor stack '$STACK'. Nodes lose Falcon protection."
    just gcp-pulumi remove-resource --folder "$FC_FOLDER" --stack "$STACK"
    echo "Teardown complete. The Artifact Registry mirror and its IAM bindings are untouched."
