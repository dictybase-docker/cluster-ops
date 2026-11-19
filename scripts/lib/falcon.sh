# shellcheck shell=bash
# Shared helpers for just_modules/falcon.justfile. Source it from a recipe
# that runs at the repository root. No helper prints a credential.

FC_FOLDER="falcon-sensor"
FC_REGISTRY="registry.crowdstrike.com"
# Unified (non-regional) node sensor repository, sensor 7.31 and later.
FC_SOURCE_REPO="${FC_REGISTRY}/falcon-sensor/release/falcon-sensor"

fc_die() {
    echo "Error: $*" >&2
    exit 1
}

# fc_resolve <stack-flag> <namespace-flag>
# Sets STACK, CFG, NS. The namespace comes from properties.namespace in
# falcon-sensor/Pulumi.<stack>.yaml. A --namespace flag that does not match
# fails here, before any change.
fc_resolve() {
    STACK="${1:-${PULUMI_STACK:-}}"
    [[ -n "$STACK" ]] || fc_die "no stack name — pass --stack or set PULUMI_STACK (via just cluster-env)."
    [[ "$STACK" =~ ^[A-Za-z0-9._-]+$ ]] || fc_die "invalid stack name '$STACK'."
    CFG="${FC_FOLDER}/Pulumi.${STACK}.yaml"
    [[ -f "$CFG" ]] || fc_die "stack config $CFG missing. Copy falcon-sensor/Pulumi.dcr-kube1.yaml and edit it."
    local cfg_ns
    cfg_ns=$(fc_prop namespace)
    [[ -n "$cfg_ns" && "$cfg_ns" != *"<"* ]] || fc_die "properties.namespace is empty or a placeholder in $CFG."
    if [[ -n "${2:-}" && "$2" != "$cfg_ns" ]]; then
        fc_die "--namespace '$2' does not match properties.namespace '$cfg_ns' in $CFG."
    fi
    NS="$cfg_ns"
}

# fc_prop <dotted.path> prints one plain value of falcon-sensor:properties, or "".
fc_prop() {
    yq -r ".config.\"falcon-sensor:properties\".$1 // \"\"" "$CFG"
}

# fc_has_secret <key> succeeds when properties.<key> is a KMS-encrypted value.
fc_has_secret() {
    [[ "$(yq -r ".config.\"falcon-sensor:properties\".$1.secure // \"\"" "$CFG")" != "" ]]
}

# fc_require_cloud sets CLOUD and API_HOST from properties.cloud. The Falcon
# cloud region names the CrowdStrike tenant location, not a GCP region.
fc_require_cloud() {
    CLOUD=$(fc_prop cloud)
    case "$CLOUD" in
        us-1) API_HOST="api.crowdstrike.com" ;;
        us-2) API_HOST="api.us-2.crowdstrike.com" ;;
        us-3) API_HOST="api.us-3.crowdstrike.com" ;;
        eu-1) API_HOST="api.eu-1.crowdstrike.com" ;;
        us-gov-1) API_HOST="api.laggar.gcw.crowdstrike.com" ;;
        us-gov-2) API_HOST="api.us-gov-2.crowdstrike.mil" ;;
        "") fc_die "properties.cloud is not set in $CFG. Run: just gcp-pulumi set-config --folder falcon-sensor --key properties.cloud --value <us-1|us-2|us-3|eu-1|us-gov-1|us-gov-2>" ;;
        *) fc_die "properties.cloud '$CLOUD' in $CFG is not a Falcon cloud region (us-1, us-2, us-3, eu-1, us-gov-1, us-gov-2)." ;;
    esac
}

# fc_require_tools <tool>... fails on the first missing tool.
fc_require_tools() {
    local t
    for t in "$@"; do
        command -v "$t" >/dev/null 2>&1 || fc_die "required tool '$t' is not installed."
    done
}

# fc_require_project sets PROJECT from PROJECT_ID. Never reads ambient gcloud
# config or GOOGLE_CLOUD_PROJECT.
fc_require_project() {
    PROJECT="${PROJECT_ID:-}"
    [[ -n "$PROJECT" ]] || fc_die "PROJECT_ID is not set — enter the cluster shell: just cluster-env --env <env> --cluster <name>."
}

# fc_require_cluster fails unless KUBECONFIG is set and every node is a GCE
# instance in PROJECT. Sets NODES (the node count, at least 1).
fc_require_cluster() {
    [[ -n "${KUBECONFIG:-}" ]] || fc_die "KUBECONFIG is not set — enter the cluster shell: just cluster-env --env <env> --cluster <name>."
    local nodes_json wrong
    nodes_json=$(kubectl get nodes -o json) || fc_die "kubectl cannot list the nodes (KUBECONFIG='${KUBECONFIG}')."
    NODES=$(jq '.items | length' <<<"$nodes_json")
    [[ "$NODES" -gt 0 ]] || fc_die "the cluster has no nodes (KUBECONFIG='${KUBECONFIG}')."
    wrong=$(PROJECT="$PROJECT" jq -r '.items[] | (.spec.providerID // "") | select(startswith("gce://\(env.PROJECT)/") | not)' <<<"$nodes_json")
    [[ -z "$wrong" ]] || fc_die "nodes outside project '$PROJECT': $wrong. Wrong KUBECONFIG?"
}

# fc_require_tag fails on a mutable or unsupported sensor tag. Chart 1.37
# supports sensor 7.40 and later.
fc_require_tag() {
    [[ -n "$1" ]] || fc_die "--tag is required. List the tags with: just falcon list-tags"
    [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || fc_die "--tag '$1' is not a valid image tag."
    [[ "$1" =~ ^([0-9]+)\.([0-9]+)\. ]] || fc_die "--tag '$1' is not a versioned sensor tag (for example 7.40.0-...). Mutable tags such as latest are rejected."
    local major="${BASH_REMATCH[1]}" minor="${BASH_REMATCH[2]}"
    if [[ "$major" -lt 7 || ( "$major" -eq 7 && "$minor" -lt 40 ) ]]; then
        fc_die "--tag '$1' is older than sensor 7.40, which chart 1.37 requires."
    fi
}

# fc_require_manager_key binds every gcloud call to the sa-manager key in
# GOOGLE_APPLICATION_CREDENTIALS (cluster env). Sets MANAGER_KEY.
fc_require_manager_key() {
    MANAGER_KEY="${GOOGLE_APPLICATION_CREDENTIALS:-}"
    [[ -n "$MANAGER_KEY" && -f "$MANAGER_KEY" ]] || fc_die "GOOGLE_APPLICATION_CREDENTIALS does not point at the sa-manager key file."
    local who
    who=$(jq -r '.client_email // empty' "$MANAGER_KEY")
    [[ "$who" == "sa-manager@${PROJECT}.iam.gserviceaccount.com" ]] ||
        fc_die "'$MANAGER_KEY' is '${who:-not a service account key}', not sa-manager@${PROJECT}."
    export CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="$MANAGER_KEY"
}

# fc_require_api_creds fails unless FALCON_CLIENT_ID and FALCON_CLIENT_SECRET
# are in the environment. They are never flags: flags show in ps and history.
fc_require_api_creds() {
    [[ -n "${FALCON_CLIENT_ID:-}" ]] || fc_die "FALCON_CLIENT_ID is not set. Export the Falcon API client ID (scopes: Sensor Download Read, Falcon Images Download Read)."
    [[ -n "${FALCON_CLIENT_SECRET:-}" ]] || fc_die "FALCON_CLIENT_SECRET is not set. Export the Falcon API client secret."
}

# fc_tmpdir makes a private work directory and removes it on exit. Sets WORK.
fc_tmpdir() {
    WORK="$(mktemp -d "${TMPDIR:-/tmp}/falcon.XXXXXX")"
    chmod 700 "$WORK"
    # shellcheck disable=SC2064
    trap "rm -rf '$WORK'" EXIT
}

# fc_api <path> prints the JSON body of an authenticated GET to the Falcon API.
# The bearer header goes through a 0600 file, never through argv.
fc_api() {
    curl -sS --fail --proto '=https' -H @"$WORK/bearer" "https://${API_HOST}$1"
}

# fc_registry_login needs CLOUD, API_HOST, WORK. It gets an OAuth token, the
# tenant CID, and a registry token, then writes $WORK/auth.json for skopeo.
# Sets FC_CID (with checksum).
fc_registry_login() {
    FALCON_CLIENT_ID="$FALCON_CLIENT_ID" FALCON_CLIENT_SECRET="$FALCON_CLIENT_SECRET" \
        jq -rn '"client_id=\(env.FALCON_CLIENT_ID|@uri)&client_secret=\(env.FALCON_CLIENT_SECRET|@uri)"' \
        >"$WORK/form"
    local token
    token=$(curl -sS --fail --proto '=https' -X POST "https://${API_HOST}/oauth2/token" \
        -H "Content-Type: application/x-www-form-urlencoded" --data @"$WORK/form" |
        jq -r '.access_token // empty') ||
        fc_die "Falcon OAuth request to ${API_HOST} failed. Check the API client and properties.cloud."
    [[ -n "$token" ]] || fc_die "Falcon OAuth returned no token. Check the API client and properties.cloud ('$CLOUD')."
    printf 'Authorization: Bearer %s\n' "$token" >"$WORK/bearer"

    FC_CID=$(fc_api /sensors/queries/installers/ccid/v1 | jq -r '.resources[0] // empty') ||
        fc_die "could not read the tenant CID. The API client needs the Sensor Download Read scope."
    [[ "$FC_CID" =~ ^[0-9A-Fa-f]{32}-[0-9A-Fa-f]{2}$ ]] || fc_die "the Falcon API returned no valid CID."

    local reg_pass
    reg_pass=$(fc_api /container-security/entities/image-registry-credentials/v1 | jq -r '.resources[0].token // empty') ||
        fc_die "could not read the registry token. The API client needs the Falcon Images Download Read scope."
    [[ -n "$reg_pass" ]] || fc_die "the Falcon API returned no registry token. Check the Falcon Images Download scope and the tenant subscription."

    local reg_user
    reg_user="fc-$(printf '%s' "${FC_CID%%-*}" | tr '[:upper:]' '[:lower:]')"
    FC_U="$reg_user" FC_P="$reg_pass" FC_R="$FC_REGISTRY" \
        jq -n '{auths: {(env.FC_R): {auth: ("\(env.FC_U):\(env.FC_P)" | @base64)}}}' >"$WORK/auth.json"
    chmod 600 "$WORK/auth.json"
}

# fc_add_gcp_auth <registry-host> adds an Artifact Registry entry, from the
# sa-manager access token, to $WORK/auth.json.
fc_add_gcp_auth() {
    local tok
    tok=$(gcloud auth print-access-token) || fc_die "gcloud could not mint an access token from $MANAGER_KEY."
    FC_T="$tok" FC_H="$1" jq '.auths[env.FC_H] = {auth: ("oauth2accesstoken:\(env.FC_T)" | @base64)}' \
        "$WORK/auth.json" >"$WORK/auth.next"
    mv "$WORK/auth.next" "$WORK/auth.json"
    chmod 600 "$WORK/auth.json"
}

# fc_node_service_accounts prints "<email>" once per distinct GCE service
# account of the cluster nodes, control plane included. It fails when a node
# is outside PROJECT, when there are no nodes, or when a node has no access
# scope that can pull from Artifact Registry.
fc_node_service_accounts() {
    local ids id rest zone name sa_json
    ids=$(kubectl get nodes -o json | jq -r '.items[].spec.providerID // ""') ||
        fc_die "kubectl cannot list the nodes (KUBECONFIG='${KUBECONFIG:-}')."
    [[ -n "$ids" ]] || fc_die "the cluster has no nodes, or kubectl cannot reach it (KUBECONFIG='${KUBECONFIG:-}')."
    : >"$WORK/sas"
    while IFS= read -r id; do
        [[ "$id" == gce://* ]] || fc_die "node providerID '$id' is not a GCE instance."
        rest="${id#gce://}"
        [[ "${rest%%/*}" == "$PROJECT" ]] || fc_die "node '$id' is in project '${rest%%/*}', not PROJECT_ID '$PROJECT'. Wrong KUBECONFIG?"
        rest="${rest#*/}"
        zone="${rest%%/*}"
        name="${rest#*/}"
        sa_json=$(gcloud compute instances describe "$name" --zone "$zone" --project "$PROJECT" \
            --format json | jq -c '.serviceAccounts // []') ||
            fc_die "could not describe instance $name in zone $zone."
        [[ "$(jq 'length' <<<"$sa_json")" -gt 0 ]] || fc_die "instance $name has no service account: it cannot pull from Artifact Registry."
        jq -e '[.[].scopes[]] | any(. == "https://www.googleapis.com/auth/cloud-platform" or . == "https://www.googleapis.com/auth/devstorage.read_only" or . == "https://www.googleapis.com/auth/devstorage.read_write" or . == "https://www.googleapis.com/auth/devstorage.full_control")' \
            <<<"$sa_json" >/dev/null ||
            fc_die "instance $name has no cloud-platform or devstorage access scope: it cannot pull from Artifact Registry."
        jq -r '.[].email' <<<"$sa_json" >>"$WORK/sas"
    done <<<"$ids"
    sort -u "$WORK/sas"
}
