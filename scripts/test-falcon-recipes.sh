#!/usr/bin/env bash
# Contract tests for the `just falcon` recipes.
#
# curl, gcloud, kubectl, pulumi, skopeo, and `just gcp-pulumi` are mocked on
# PATH: each mock records its call in $STUB_LOG and answers from STUB_*
# variables. jq and yq run for real against throwaway fixture files.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

just_bin="$(command -v just)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/falcon-test.XXXXXX")"
log_file="${tmp_dir}/calls.log"
stack="fc-contract-$$"
cfg_file="falcon-sensor/Pulumi.${stack}.yaml"
cleanup() { rm -rf "$tmp_dir" "$cfg_file"; }
trap cleanup EXIT
mkdir -p "${tmp_dir}/bin" "${tmp_dir}/fx"

project="dcr-test1"
cid="0123456789ABCDEF0123456789ABCDEF-A1"
src_digest="sha256:$(printf 'a%.0s' {1..64})"
other_digest="sha256:$(printf 'b%.0s' {1..64})"
client_secret="CS-CLIENT-SECRET-VALUE"
reg_token="CS-REGISTRY-TOKEN-VALUE"
gcp_token="ya29.GCP-ACCESS-TOKEN"

# ── fixtures ─────────────────────────────────────────────────────────────────
printf '{"client_email":"sa-manager@%s.iam.gserviceaccount.com"}' "$project" >"${tmp_dir}/fx/manager.json"
nodes_json() {
    # nodes_json <project> <count>
    jq -n --arg p "$1" --argjson n "$2" \
        '{items: [range($n) | {metadata: {name: "node-\(.)"}, spec: {providerID: "gce://\($p)/us-central1-a/node-\(.)"}}]}'
}
nodes_json "$project" 3 >"${tmp_dir}/fx/nodes.json"
nodes_json "other-proj" 1 >"${tmp_dir}/fx/nodes-foreign.json"
echo '{"items":[]}' >"${tmp_dir}/fx/nodes-none.json"
ds_json() {
    # ds_json <desired> <ready> <image>
    jq -n --argjson d "$1" --argjson r "$2" --arg i "$3" \
        '{spec: {selector: {matchLabels: {"app.kubernetes.io/instance": "falcon-sensor"}},
                 template: {spec: {containers: [{name: "falcon-node-sensor", image: $i}]}}},
          status: {desiredNumberScheduled: $d, numberReady: $r}}'
}
mirror="us-central1-docker.pkg.dev/${project}/falcon/falcon-sensor"
ds_json 3 3 "${mirror}@${src_digest}" >"${tmp_dir}/fx/ds-ok.json"
ds_json 2 2 "${mirror}@${src_digest}" >"${tmp_dir}/fx/ds-short.json"
jq -n '{items: [range(3) | {metadata: {name: "falcon-sensor-\(.)"}, spec: {nodeName: "node-\(.)"}, status: {}}]}' \
    >"${tmp_dir}/fx/pods.json"

write_cfg() {
    # write_cfg <cloud> <with-image yes|no> <with-cid yes|no>
    {
        echo "config:"
        echo "  falcon-sensor:properties:"
        echo "    namespace: falcon-system"
        echo "    clusterName: dcr-test1"
        [[ -n "$1" ]] && echo "    cloud: $1"
        echo "    chart: {name: falcon-sensor, repository: https://crowdstrike.github.io/falcon-helm, version: 1.37.0}"
        echo "    registry: {location: us-central1, repository: falcon}"
        if [[ "$2" == yes ]]; then
            echo "    image: {repository: ${mirror}, tag: 7.40.0-1, digest: ${src_digest}}"
        fi
        if [[ "$3" == yes ]]; then
            echo "    cid:"
            echo "      secure: v1:encrypted"
        fi
    } >"$cfg_file"
}

# ── mocks ────────────────────────────────────────────────────────────────────
cat >"${tmp_dir}/bin/kubectl" <<'MOCK'
#!/usr/bin/env bash
printf 'kubectl %s\n' "$*" >>"${STUB_LOG}"
case "$*" in
    "get nodes -o json") cat "${STUB_NODES}" ;;
    *" get daemonset "*) [[ -n "${STUB_DS:-}" ]] && cat "${STUB_DS}" || exit 1 ;;
    *" get pods -o wide"*) echo "NAME READY" ;;
    *" get pods "*"-o json"*) cat "${STUB_PODS}" ;;
    *" exec "*)
        pod=$(awk '{for (i=1;i<=NF;i++) if ($i=="exec") print $(i+1)}' <<<"$*")
        [[ " ${STUB_NOAID:-} " == *" $pod "* ]] && { echo 'aid is not set.'; exit 0; }
        echo 'aid="0123456789abcdef0123456789abcdef".' ;;
    *) exit 0 ;;
esac
MOCK

cat >"${tmp_dir}/bin/gcloud" <<'MOCK'
#!/usr/bin/env bash
printf 'gcloud %s\n' "$*" >>"${STUB_LOG}"
case "$*" in
    "compute instances describe "*)
        [[ -n "${STUB_FAIL_NODE:-}" && "$*" == *"${STUB_FAIL_NODE} "* ]] && { echo 'ERROR: denied' >&2; exit 1; }
        scope="https://www.googleapis.com/auth/devstorage.read_only"
        [[ "${STUB_NOSCOPE:-0}" == 1 ]] && scope="https://www.googleapis.com/auth/logging.write"
        sa="nodes@dcr-test1.iam.gserviceaccount.com"
        [[ "$*" == *node-0* ]] && sa="control-plane@dcr-test1.iam.gserviceaccount.com"
        printf '{"serviceAccounts":[{"email":"%s","scopes":["%s"]}]}' "$sa" "$scope" ;;
    "artifacts repositories describe "*)
        state="${STUB_AR_STATE:-missing}"
        [[ "${STUB_REPO_EXISTS:-0}" == 1 ]] && state=exists
        if [[ -n "${STUB_AR_DENY_COUNT:-}" ]]; then
            n=$(cat "${STUB_DIR}/deny-count" 2>/dev/null || echo 0)
            if [[ "$n" -lt "$STUB_AR_DENY_COUNT" ]]; then
                echo $((n + 1)) >"${STUB_DIR}/deny-count"; state=denied
            fi
        fi
        case "$state" in
            exists) exit 0 ;;
            denied) echo "ERROR: PERMISSION_DENIED: Permission 'artifactregistry.repositories.get' denied on resource" >&2 ;;
            disabled) echo 'ERROR: SERVICE_DISABLED: Artifact Registry API has not been used' >&2 ;;
            *) echo 'ERROR: (gcloud.artifacts.repositories.describe) NOT_FOUND: Requested entity was not found.' >&2 ;;
        esac
        exit 1 ;;
    "projects get-iam-policy "*)
        if [[ "${STUB_HAS_ADMIN:-0}" == 1 ]]; then
            printf '{"bindings":[{"role":"roles/artifactregistry.admin","members":["serviceAccount:sa-manager@dcr-test1.iam.gserviceaccount.com"]}]}'
        else
            printf '{"bindings":[{"role":"roles/storage.admin","members":["serviceAccount:sa-manager@dcr-test1.iam.gserviceaccount.com"]}]}'
        fi ;;
    "artifacts repositories get-iam-policy "*)
        printf '{"bindings":[{"role":"roles/artifactregistry.reader","members":["serviceAccount:control-plane@dcr-test1.iam.gserviceaccount.com"]}]}' ;;
    "auth print-access-token"*) echo "ya29.GCP-ACCESS-TOKEN" ;;
    *) exit 0 ;;
esac
MOCK

cat >"${tmp_dir}/bin/curl" <<'MOCK'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >>"${STUB_LOG}"
url="" hdr=""
prev=""
for a in "$@"; do
    [[ "$prev" == "-H" && "$a" == @* ]] && hdr="${a#@}"
    [[ "$a" == https://* ]] && url="$a"
    prev="$a"
done
if [[ "$url" != */oauth2/token ]]; then
    grep -q 'Authorization: Bearer cs-bearer' "$hdr" || { echo 'missing bearer' >&2; exit 22; }
fi
case "$url" in
    */oauth2/token) [[ "${STUB_OAUTH_FAIL:-0}" == 1 ]] && exit 22; echo '{"access_token":"cs-bearer"}' ;;
    */sensors/queries/installers/ccid/v1) printf '{"resources":["%s"]}' "${STUB_CID}" ;;
    */image-registry-credentials/v1) echo '{"resources":[{"token":"CS-REGISTRY-TOKEN-VALUE"}]}' ;;
    *) exit 22 ;;
esac
MOCK

cat >"${tmp_dir}/bin/skopeo" <<'MOCK'
#!/usr/bin/env bash
printf 'skopeo %s\n' "$*" >>"${STUB_LOG}"
auth=""
prev=""
for a in "$@"; do [[ "$prev" == "--authfile" ]] && auth="$a"; prev="$a"; done
[[ -n "$auth" ]] && cp "$auth" "${STUB_DIR}/last-auth.json"
case "$1" in
    list-tags) echo '{"Tags":["7.41.0-2","7.40.0-1"]}' ;;
    inspect)
        ref="${*: -1}"
        [[ "$ref" == *-missing ]] && exit 1
        if [[ "$ref" == *pkg.dev* ]]; then printf '{"Digest":"%s"}' "${STUB_DEST_DIGEST}"
        else printf '{"Digest":"%s"}' "${STUB_SRC_DIGEST}"; fi ;;
    copy) exit 0 ;;
esac
MOCK

cat >"${tmp_dir}/bin/pulumi" <<'MOCK'
#!/usr/bin/env bash
printf 'pulumi %s\n' "$*" >>"${STUB_LOG}"
case "$*" in
    *"config get --path properties.cid"*) printf '%s' "${STUB_STACK_CID}" ;;
    *"stack output daemonSet"*) echo falcon-sensor ;;
esac
MOCK

cat >"${tmp_dir}/bin/just" <<MOCK
#!/usr/bin/env bash
if [[ "\$1" == gcp-pulumi ]]; then printf 'just %s\n' "\$*" >>"\${STUB_LOG}"; exit 0; fi
exec "${just_bin}" "\$@"
MOCK
chmod +x "${tmp_dir}"/bin/*

# ── harness ──────────────────────────────────────────────────────────────────
base_env=(
    "PATH=${tmp_dir}/bin:${PATH}" "STUB_LOG=${log_file}" "STUB_DIR=${tmp_dir}"
    "PULUMI_STACK=${stack}" "PROJECT_ID=${project}" "KUBECONFIG=${tmp_dir}/fx/kubeconfig"
    "GOOGLE_APPLICATION_CREDENTIALS=${tmp_dir}/fx/manager.json"
    "PULUMI_GCP_CREDENTIALS=${tmp_dir}/fx/manager.json"
    "FALCON_CLIENT_ID=client-id" "FALCON_CLIENT_SECRET=${client_secret}"
    "STUB_NODES=${tmp_dir}/fx/nodes.json" "STUB_PODS=${tmp_dir}/fx/pods.json"
    "STUB_DS=${tmp_dir}/fx/ds-ok.json" "STUB_CID=${cid}" "STUB_STACK_CID=${cid}"
    "STUB_SRC_DIGEST=${src_digest}" "STUB_DEST_DIGEST=${src_digest}"
)
run_recipe() { : >"$log_file"; rm -f "${tmp_dir}/deny-count"; env "${base_env[@]}" "$@"; }
expect_failure() {
    local expected="$1" output status
    shift
    set +e
    output=$(run_recipe "$@" 2>&1)
    status=$?
    set -e
    if [[ "$status" -eq 0 || "$output" != *"$expected"* ]]; then
        printf 'expected failure containing %q, got status %s:\n%s\n' "$expected" "$status" "$output" >&2
        exit 1
    fi
}
assert_no_call() { if rg -q -- "$1" "$log_file"; then printf 'unexpected call matching %s:\n' "$1" >&2; cat "$log_file" >&2; exit 1; fi; }
assert_call() { rg -q -- "$1" "$log_file" || { printf 'missing call matching %s:\n' "$1" >&2; cat "$log_file" >&2; exit 1; }; }
line_of() { rg -n -m1 -- "$1" "$log_file" | cut -d: -f1; }
assert_before() {
    local a b
    a=$(line_of "$1")
    b=$(line_of "$2")
    [[ -n "$a" && -n "$b" && "$a" -lt "$b" ]] || { printf '%s must precede %s:\n' "$1" "$2" >&2; cat "$log_file" >&2; exit 1; }
}
no_mutation() {
    assert_no_call 'gcp-pulumi (ensure-stack|set-config|create-resource)'
    assert_no_call 'artifacts repositories (create|add-iam-policy-binding)'
    assert_no_call '^skopeo copy'
}
mutation_re='gcp-pulumi ensure-stack|artifacts repositories create|add-iam-policy-binding|skopeo copy|gcp-pulumi set-config'

echo "falcon recipe contract tests"

# ── mirror-image preflight ───────────────────────────────────────────────────
write_cfg us-1 no no
expect_failure '--tag is required' "$just_bin" falcon mirror-image
no_mutation
expect_failure "is not a valid image tag" "$just_bin" falcon mirror-image --tag 'x;rm -rf'
no_mutation
expect_failure 'FALCON_CLIENT_SECRET is not set' env -u FALCON_CLIENT_SECRET "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
expect_failure 'PROJECT_ID is not set' env -u PROJECT_ID "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
printf '{"client_email":"someone@%s.iam.gserviceaccount.com"}' "$project" >"${tmp_dir}/fx/other.json"
expect_failure 'not sa-manager@' env "GOOGLE_APPLICATION_CREDENTIALS=${tmp_dir}/fx/other.json" "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
expect_failure 'Mutable tags such as latest are rejected' "$just_bin" falcon mirror-image --tag latest
no_mutation
expect_failure 'older than sensor 7.40' "$just_bin" falcon mirror-image --tag 7.39.1-100-1
no_mutation
expect_failure 'KUBECONFIG is not set' env -u KUBECONFIG "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
echo '  test_mirror_requires_tag_creds_and_identity: PASS'

write_cfg "" no no
expect_failure 'properties.cloud is not set' "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
write_cfg us-central1 no no
expect_failure "is not a Falcon cloud region" "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
echo '  test_mirror_requires_falcon_cloud: PASS'

write_cfg us-1 no no
expect_failure "nodes outside project '${project}'" env "STUB_NODES=${tmp_dir}/fx/nodes-foreign.json" "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
expect_failure 'the cluster has no nodes' env "STUB_NODES=${tmp_dir}/fx/nodes-none.json" "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
expect_failure 'no cloud-platform or devstorage access scope' env STUB_NOSCOPE=1 "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
expect_failure 'could not describe instance node-0' env STUB_FAIL_NODE=node-0 "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
expect_failure 'could not describe instance node-2' env STUB_FAIL_NODE=node-2 "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
echo '  test_mirror_checks_node_identity_before_change: PASS'

expect_failure 'Falcon OAuth request' env STUB_OAUTH_FAIL=1 "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
expect_failure "tag '7.40.0-missing' is not in" "$just_bin" falcon mirror-image --tag 7.40.0-missing
no_mutation
write_cfg us-1 no yes
expect_failure 'is not the CID of the tenant' env "STUB_STACK_CID=FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF-00" "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
echo '  test_mirror_checks_source_and_tenant_before_change: PASS'

expect_failure 'Run: just falcon grant-registry-access' env STUB_AR_STATE=denied "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
assert_no_call 'oauth2/token'
expect_failure 'artifactregistry.googleapis.com is not enabled' env STUB_AR_STATE=disabled "$just_bin" falcon mirror-image --tag 7.40.0-1
no_mutation
echo '  test_mirror_checks_registry_access_before_change: PASS'

# ── grant-registry-access ────────────────────────────────────────────────────
write_cfg us-1 no no
expect_failure 'not sa-manager@' env "GOOGLE_APPLICATION_CREDENTIALS=${tmp_dir}/fx/other.json" "$just_bin" falcon grant-registry-access
assert_no_call 'add-iam-policy-binding'
expect_failure 'artifactregistry.googleapis.com is not enabled' env STUB_AR_STATE=disabled "$just_bin" falcon grant-registry-access
assert_no_call 'add-iam-policy-binding'
out=$(run_recipe env STUB_AR_DENY_COUNT=3 "$just_bin" falcon grant-registry-access --interval 0 2>&1) || { echo "$out"; exit 1; }
assert_call 'gcloud projects add-iam-policy-binding dcr-test1 --member serviceAccount:sa-manager@dcr-test1.iam.gserviceaccount.com --role roles/artifactregistry.admin --condition None'
assert_before 'projects get-iam-policy' 'projects add-iam-policy-binding'
[[ "$out" == *"try 2/8"* && "$out" == *"Next: just falcon mirror-image"* ]] || { echo "grant output: $out" >&2; exit 1; }
out=$(run_recipe env STUB_HAS_ADMIN=1 STUB_AR_STATE=exists "$just_bin" falcon grant-registry-access 2>&1)
assert_no_call 'add-iam-policy-binding'
[[ "$out" == *"has roles/artifactregistry.admin"* ]] || { echo "grant output: $out" >&2; exit 1; }
expect_failure 'still denies sa-manager@dcr-test1 after 2 retries' env STUB_AR_STATE=denied "$just_bin" falcon grant-registry-access --retries 2 --interval 0
echo '  test_grant_registry_access_is_idempotent_and_waits: PASS'

# ── mirror-image changes ─────────────────────────────────────────────────────
write_cfg us-1 no yes
out=$(run_recipe "$just_bin" falcon mirror-image --tag 7.40.0-1 2>&1)
assert_before 'compute instances describe' 'gcp-pulumi ensure-stack'
assert_before "skopeo inspect .*registry.crowdstrike.com/falcon-sensor/release/falcon-sensor:7.40.0-1" 'gcp-pulumi ensure-stack'
assert_before 'gcp-pulumi ensure-stack' 'artifacts repositories create falcon --repository-format docker --location us-central1 --project dcr-test1'
assert_call 'add-iam-policy-binding falcon .*--member serviceAccount:nodes@dcr-test1.iam.gserviceaccount.com --role roles/artifactregistry.reader'
assert_no_call 'add-iam-policy-binding .*control-plane@'
assert_before 'add-iam-policy-binding' '^skopeo copy'
assert_call "skopeo copy --all --preserve-digests .* docker://registry.crowdstrike.com/falcon-sensor/release/falcon-sensor:7.40.0-1 docker://${mirror}:7.40.0-1"
assert_before '^skopeo copy' 'gcp-pulumi set-config'
assert_call "set-config --folder falcon-sensor --stack ${stack} --key properties.image.repository --value ${mirror}"
assert_call "set-config .* --key properties.image.tag --value 7.40.0-1"
assert_call "set-config .* --key properties.image.digest --value ${src_digest}"
assert_call 'curl .*--data @'
auth=$(cat "${tmp_dir}/last-auth.json")
want_cs=$(printf 'fc-0123456789abcdef0123456789abcdef:%s' "$reg_token" | base64 | tr -d '\n')
[[ "$(jq -r '.auths["registry.crowdstrike.com"].auth' <<<"$auth")" == "$want_cs" ]] || { echo "bad CrowdStrike registry auth" >&2; exit 1; }
[[ "$(jq -r '.auths["us-central1-docker.pkg.dev"].auth' <<<"$auth" | base64 -d)" == "oauth2accesstoken:${gcp_token}" ]] || { echo "bad Artifact Registry auth" >&2; exit 1; }
for s in "$client_secret" "$reg_token" "$gcp_token" cs-bearer; do
    if rg -qF -- "$s" "$log_file" || [[ "$out" == *"$s"* ]]; then echo "credential '$s' leaked into argv or output" >&2; exit 1; fi
done
echo '  test_mirror_orders_changes_and_pins_digest: PASS'
echo '  test_mirror_keeps_credentials_out_of_argv: PASS'

out=$(run_recipe env STUB_REPO_EXISTS=1 "$just_bin" falcon mirror-image --tag 7.40.0-1 2>&1)
assert_no_call 'artifacts repositories create'
[[ "$out" == *"repository falcon exists"* ]] || { echo "expected repository reuse" >&2; exit 1; }
echo '  test_mirror_reuses_existing_repository: PASS'

expect_failure 'does not match source digest' env "STUB_DEST_DIGEST=${other_digest}" "$just_bin" falcon mirror-image --tag 7.40.0-1
assert_no_call 'gcp-pulumi set-config'
echo '  test_mirror_digest_mismatch_leaves_config: PASS'

out=$(run_recipe "$just_bin" falcon list-tags 2>&1)
[[ "$out" == *"7.40.0-1"*"7.41.0-2"* ]] || { echo "list-tags output: $out" >&2; exit 1; }
if rg -q -- "$mutation_re" "$log_file"; then echo "list-tags must be read-only" >&2; exit 1; fi
echo '  test_list_tags_is_read_only: PASS'

# ── deploy ───────────────────────────────────────────────────────────────────
write_cfg us-1 yes yes
expect_failure "--namespace 'prod' does not match properties.namespace 'falcon-system'" "$just_bin" falcon deploy --namespace prod
no_mutation
write_cfg us-1 yes no
expect_failure 'properties.cid is not set' "$just_bin" falcon deploy
no_mutation
write_cfg us-1 no yes
expect_failure 'properties.image.digest is not set' "$just_bin" falcon deploy
no_mutation
write_cfg "" yes yes
expect_failure 'properties.cloud is not set' "$just_bin" falcon deploy
no_mutation
write_cfg us-1 yes yes
expect_failure 'nodes outside project' env "STUB_NODES=${tmp_dir}/fx/nodes-foreign.json" "$just_bin" falcon deploy
no_mutation
expect_failure 'the cluster has no nodes' env "STUB_NODES=${tmp_dir}/fx/nodes-none.json" "$just_bin" falcon deploy
no_mutation
echo '  test_deploy_preflight_before_change: PASS'

out=$(run_recipe "$just_bin" falcon deploy --retries 2 --interval 0 2>&1) || { echo "$out"; cat "$log_file"; exit 1; }
assert_before 'gcp-pulumi preview' 'gcp-pulumi create-resource'
[[ "$out" == *"ready on 3/3 nodes"* ]] || { echo "deploy output: $out" >&2; exit 1; }
expect_failure 'not ready on every node' env "STUB_DS=${tmp_dir}/fx/ds-short.json" "$just_bin" falcon deploy --retries 2 --interval 0
expect_failure 'not ready on every node' "$just_bin" falcon deploy --retries 0 --interval 0
echo '  test_deploy_waits_for_every_node: PASS'

# ── verify ───────────────────────────────────────────────────────────────────
out=$(run_recipe "$just_bin" falcon verify 2>&1)
[[ "$out" == *"All checks passed"* && "$out" == *"node-2: agent ID"* ]] || { echo "verify output: $out" >&2; exit 1; }
if rg -q -- "$mutation_re" "$log_file"; then echo "verify must be read-only" >&2; exit 1; fi
expect_failure '1 check(s) failed' env "STUB_NOAID=falcon-sensor-1" "$just_bin" falcon verify
expect_failure 'FAIL  DaemonSet falcon-sensor scheduled on 2/3 nodes' env "STUB_DS=${tmp_dir}/fx/ds-short.json" "$just_bin" falcon verify
ds_json 3 3 "registry.crowdstrike.com/falcon-sensor/release/falcon-sensor:latest" >"${tmp_dir}/fx/ds-foreign.json"
expect_failure 'is not the mirrored' env "STUB_DS=${tmp_dir}/fx/ds-foreign.json" "$just_bin" falcon verify
echo '{"items":[]}' >"${tmp_dir}/fx/pods-none.json"
expect_failure '0 sensor pods for 3 nodes' env "STUB_PODS=${tmp_dir}/fx/pods-none.json" "$just_bin" falcon verify
expect_failure 'nodes outside project' env "STUB_NODES=${tmp_dir}/fx/nodes-foreign.json" "$just_bin" falcon verify
echo '  test_verify_checks_coverage_image_and_aid: PASS'

# ── logs ─────────────────────────────────────────────────────────────────────
jq '.items[].spec.containers = [{name: "falcon-node-sensor"}]' "${tmp_dir}/fx/pods.json" >"${tmp_dir}/fx/pods-c.json"
run_recipe env "STUB_PODS=${tmp_dir}/fx/pods-c.json" "$just_bin" falcon logs --node node-1 >/dev/null
assert_call 'kubectl -n falcon-system logs falcon-sensor-1 -c falcon-node-sensor --tail 200'
expect_failure "no sensor pod on node 'node-9'" env "STUB_PODS=${tmp_dir}/fx/pods-c.json" "$just_bin" falcon logs --node node-9
echo '  test_logs_selects_pod_by_node: PASS'

# ── teardown ─────────────────────────────────────────────────────────────────
run_recipe "$just_bin" falcon teardown >/dev/null
assert_call "gcp-pulumi remove-resource --folder falcon-sensor --stack ${stack}"
assert_no_call 'artifacts repositories delete'
expect_failure 'nodes outside project' env "STUB_NODES=${tmp_dir}/fx/nodes-foreign.json" "$just_bin" falcon teardown
assert_no_call 'remove-resource'
echo '  test_teardown_keeps_mirror: PASS'

echo "All falcon recipe contract tests passed."
