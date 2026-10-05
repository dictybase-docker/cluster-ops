# CI deploy credential and variable recipes.
# Key lifecycle: source the key (reuse --sa-key, or mint via `just gcp-sa
# create-sa` / `create-sa-key`) -> preflight it -> publish it -> set the vars.
# Registry-driven: config/clusters/<cluster>.yaml is the single source of truth.

# Read-only preflight of the deployer SA key against the registry.
# Checks, in order: key parses as GCP SA JSON; key project matches the
# registry gcp_project; kubeconfig export from the kops state bucket works
# (same code path as verify-deployer-access); KMS encrypt/decrypt round-trip
# on the registry secrets key. Zero mutations; fails closed naming every
# failure; never prints key material.
# Usage: just ci check-deploy-credentials --cluster <name> --sa-key <path>
[arg("cluster", long="cluster", short="c", help="Cluster name (registry entry: config/clusters/<name>.yaml)")]
[arg("sa_key", long="sa-key", short="k", help="Deployer service account key JSON path")]
[group('ci-management')]
[no-cd]
check-deploy-credentials cluster sa_key:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    key="{{ sa_key }}"
    if [ ! -f "${key}" ]; then
        echo "ERROR: SA key file not found: ${key}" >&2
        exit 1
    fi
    project_id=$(jq -r '.project_id // empty' "${key}")
    if [ -z "${project_id}" ]; then
        echo "ERROR: ${key} is not a GCP service account key JSON." >&2
        exit 1
    fi

    entry="config/clusters/${name}.yaml"
    if [ ! -f "${entry}" ]; then
        echo "ERROR: no registry entry for cluster '${name}' — expected ${entry}." >&2
        exit 1
    fi
    reg_project=$(yq -r '.gcp_project' "${entry}")
    if [ "${project_id}" != "${reg_project}" ]; then
        echo "ERROR: key project '${project_id}' does not match registry gcp_project '${reg_project}'." >&2
        echo "       Mint or reuse a key from the '${reg_project}' project only." >&2
        exit 1
    fi

    echo "=== 1/2: deployer access (kubeconfig + deployments + KMS) ==="
    just gcp-cluster verify-deployer-access --cluster "${name}" --sa-key "${key}"

    echo "=== 2/2: roles from deployer-roles.txt present on the SA ==="
    roles_file="gcs-files/roles-permissions/deployer-roles.txt"
    if [ -f "${roles_file}" ]; then
        sa_email=$(jq -r '.client_email // empty' "${key}")
        while read -r role; do
            [ -z "${role}" ] && continue
            if ! gcloud projects get-iam-policy "${reg_project}" \
                --flatten="bindings[].members" \
                --filter="bindings.members:${sa_email}" \
                --format="value(bindings.role)" 2>/dev/null | grep -qx "${role}"; then
                echo "MISSING: role '${role}' not granted to ${sa_email} in ${reg_project}." >&2
                exit 1
            fi
        done < "${roles_file}"
        echo "All deployer roles present."
    else
        echo "WARN: ${roles_file} not found — role check skipped (add the file to enforce it)." >&2
    fi

    echo "Deploy credentials verified for ${name}."
    echo "Next: just ci set-deploy-secret --cluster ${name} --sa-key ${key} --repos <owner/name>"

# Publish the deployer SA key to the org secret PROD_DEPLOY_SA_KEY.
# One idempotent upsert (gh secret set) that stores the key and sets repo
# visibility (selected repositories) in a single call. Refuses a missing or
# unreadable key; never echoes key material; prints the rm reminder for the
# on-disk file. Needs an org-admin gh token (Actions secrets write).
# Usage: just ci set-deploy-secret --cluster <name> --sa-key <path> --repos <owner/name>[,<owner/name>...]
[arg("cluster", long="cluster", short="c", help="Cluster name (registry entry: config/clusters/<name>.yaml)")]
[arg("sa_key", long="sa-key", short="k", help="Deployer service account key JSON path")]
[arg("repos", long="repos", short="r", help="Target repositories, owner/name form, comma-separated")]
[group('ci-management')]
[no-cd]
set-deploy-secret cluster sa_key repos:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    key="{{ sa_key }}"
    IFS=, read -r -a repos <<< "{{ repos }}"
    if [ "${#repos[@]}" -eq 0 ]; then
        echo "ERROR: pass --repos <owner/name>[,<owner/name>...]." >&2
        exit 1
    fi
    for r in "${repos[@]}"; do
        if ! [[ "${r}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
            echo "ERROR: '${r}' is not in owner/name form." >&2
            exit 1
        fi
    done
    if [ ! -r "${key}" ]; then
        echo "ERROR: SA key file missing or unreadable: ${key}" >&2
        exit 1
    fi
    if ! jq -e '.project_id' "${key}" >/dev/null 2>&1; then
        echo "ERROR: ${key} is not a GCP service account key JSON." >&2
        exit 1
    fi

    # Fail fast when gh cannot see the org (auth scope problem) before
    # touching the secret.
    if ! gh org list 2>/dev/null | grep -q dictyBase; then
        echo "ERROR: gh token cannot access org dictyBase (needs org Actions secrets write)." >&2
        exit 1
    fi

    # Map repo names to ids in one listing call; name every unknown repo.
    ids=()
    unknown=()
    for r in "${repos[@]}"; do
        id=$(gh repo view "${r}" --json id -q .id 2>/dev/null) || true
        if [ -z "${id}" ]; then
            unknown+=("${r}")
        else
            ids+=("${id}")
        fi
    done
    if [ "${#unknown[@]}" -gt 0 ]; then
        echo "ERROR: cannot resolve repo id for:" >&2
        printf '       %s\n' "${unknown[@]}" >&2
        exit 1
    fi

    gh secret set PROD_DEPLOY_SA_KEY --org dictyBase \
        --visibility selected --repos "$(IFS=,; echo "${ids[*]}")" < "${key}"

    echo "Published PROD_DEPLOY_SA_KEY (org dictyBase, selected repositories) for ${name}."
    echo "  The key file on disk is secret material — delete it when done:"
    echo "    rm ${key}"
    echo "Next: just ci sync-deploy-vars --cluster ${name} --repos <owner/name>"

# Publish the registry values as GitHub Actions variables for tag deploys.
# Variable names derive from the registry ci_env prefix (PROD -> PROD_CLUSTER,
# PROD_KOPS_STATE_STORAGE, PROD_KOPS_VERSION, PROD_KUBECTL_VERSION,
# PROD_PULUMI_VERSION); values come only from the registry — nothing is
# hardcoded here. Idempotent upserts; verifies PROD_DEPLOY_SA_KEY visibility
# per repo and prints the exact fix when absent.
# Usage: just ci sync-deploy-vars --cluster <name> --repos <owner/name>[,<owner/name>...]
[arg("cluster", long="cluster", short="c", help="Cluster name (registry entry: config/clusters/<name>.yaml)")]
[arg("repos", long="repos", short="r", help="Target repositories, owner/name form, comma-separated")]
[group('ci-management')]
[no-cd]
sync-deploy-vars cluster repos:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    IFS=, read -r -a repos <<< "{{ repos }}"
    if [ "${#repos[@]}" -eq 0 ]; then
        echo "ERROR: pass --repos <owner/name>[,<owner/name>...]." >&2
        exit 1
    fi
    for r in "${repos[@]}"; do
        if ! [[ "${r}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
            echo "ERROR: '${r}' is not in owner/name form." >&2
            exit 1
        fi
    done

    entry="config/clusters/${name}.yaml"
    if [ ! -f "${entry}" ]; then
        echo "ERROR: no registry entry for cluster '${name}' — expected ${entry}." >&2
        exit 1
    fi
    ci_env=$(yq -r '.ci_env' "${entry}")
    cluster_val=$(yq -r '.cluster' "${entry}")
    kops_state=$(yq -r '.kops_state' "${entry}")
    kops_ver=$(yq -r '.kops_version' "${entry}")
    kubectl_ver=$(yq -r '.kubectl_version' "${entry}")
    pulumi_ver=$(yq -r '.pulumi_version' "${entry}")

    echo "Publishing variables for ${name} (prefix ${ci_env}):"
    for r in "${repos[@]}"; do
        for kv in \
            "${ci_env}_CLUSTER=${cluster_val}" \
            "${ci_env}_KOPS_STATE_STORAGE=${kops_state}" \
            "${ci_env}_KOPS_VERSION=${kops_ver}" \
            "${ci_env}_KUBECTL_VERSION=${kubectl_ver}" \
            "${ci_env}_PULUMI_VERSION=${pulumi_ver}"; do
            var="${kv%%=*}"
            val="${kv#*=}"
            before=$(gh variable list --repo "${r}" 2>/dev/null | grep "^${var}\s" | awk '{print $2}') || true
            gh variable set "${var}" --body "${val}" --repo "${r}"
            echo "  ${r}: ${var}=${val} (was: ${before:-unset})"
        done

        # Secret visibility per repo: print the exact fix when absent.
        if ! gh api "orgs/dictyBase/actions/secrets/PROD_DEPLOY_SA_KEY/repositories" \
            --jq ".repositories[].name" 2>/dev/null | grep -qx "$(basename "${r}")"; then
            echo "WARN: PROD_DEPLOY_SA_KEY not visible to ${r} — fix:" >&2
            echo "      just ci set-deploy-secret --cluster ${name} --sa-key <path> --repos ${r}" >&2
        fi

        # Org var visibility: warn only — it already exists org-wide.
        if ! gh variable list --repo "${r}" 2>/dev/null | grep -q "^PULUMI_STATE_STORAGE\s"; then
            echo "WARN: PULUMI_STATE_STORAGE not visible to ${r} — check the org variable's visibility." >&2
        fi
    done
    echo "Variables published for ${#repos[@]} repo(s)."
    echo "Next: just ci render-tag-deploy --stack {{ cluster }} --app <app> --project <project> --out tag-build.yaml"

# Render the complete tag workflow for a service repo: test + lint, then the
# composite deploy, pointed at a production cluster through the <CI_ENV>_*
# variables. Static values come from the registry entry whose 'stack' key
# matches --stack. Also runs the input-drift guard: fetches composite-deploy.yaml
# at --workflow-ref and fails when any rendered 'with:' key is missing from
# its workflow_call inputs — a caller/reusable-workflow mismatch dies here,
# not in a production deploy run. Writes to --out; never edits the service
# repo in place. The PR and merge in the service repo stay manual.
# Usage: just ci render-tag-deploy --stack <name> --app <app> --project <project> --out <file> [--workflow-ref <ref>]
[arg("stack", long="stack", short="s", help="Pulumi stack name; must match a registry entry's stack key")]
[arg("app", long="app", short="a", help="Application name passed to the composite (e.g. order)")]
[arg("project", long="project", short="p", help="cluster-ops Pulumi project folder (= service repo name)")]
[arg("out", long="out", short="o", help="Output file path for the rendered tag-build.yaml")]
[arg("workflow_ref", long="workflow-ref", short="w", help="Git ref of dictyBase/workflows to pin in the output (default develop)")]
[group('ci-management')]
[no-cd]
render-tag-deploy stack app project out workflow_ref="develop":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    app_name="{{ app }}"
    project="{{ project }}"
    out_file="{{ out }}"

    if [ -z "${app_name}" ] || [ -z "${project}" ] || [ -z "${out_file}" ]; then
        echo "ERROR: --app, --project and --out are required." >&2
        exit 1
    fi

    entry=""
    for f in config/clusters/*.yaml; do
        [ -e "${f}" ] || continue
        if [ "$(yq -r '.stack' "${f}")" = "${stack_name}" ]; then
            entry="${f}"
            break
        fi
    done
    if [ -z "${entry}" ]; then
        echo "ERROR: no registry entry with stack '${stack_name}' (checked config/clusters/*.yaml)." >&2
        exit 1
    fi
    ci_env=$(yq -r '.ci_env' "${entry}")

    tmp=$(mktemp)
    trap 'rm -f "${tmp}"' EXIT
    sed \
        -e "s|__APP__|${app_name}|g" \
        -e "s|__PROJECT__|${project}|g" \
        -e "s|__STACK__|${stack_name}|g" \
        -e "s|__DOCKER_IMAGE__|${project}|g" \
        -e "s|__CI_ENV__|${ci_env}|g" \
        -e "s|__WORKFLOW_REF__|{{ workflow_ref }}|g" \
        config/templates/tag-build-deploy.yaml.tmpl > "${tmp}"

    if grep -q '__[A-Z_]*__' "${tmp}"; then
        echo "ERROR: unsubstituted placeholder in rendered output:" >&2
        grep '__[A-Z_]*__' "${tmp}" >&2
        exit 1
    fi
    if grep -qi 'staging' "${tmp}"; then
        echo "ERROR: rendered file mentions staging — there is no staging environment." >&2
        exit 1
    fi
    yq -r '.' "${tmp}" >/dev/null

    # Input-drift guard: every with: key must exist in composite-deploy.yaml's
    # workflow_call inputs at the pinned ref. Mismatch fails here, not in prod.
    wtmp=$(mktemp)
    if ! curl -fsSL "https://raw.githubusercontent.com/dictyBase/workflows/{{ workflow_ref }}/.github/workflows/composite-deploy.yaml" -o "${wtmp}"; then
        echo "ERROR: could not fetch composite-deploy.yaml at ref '{{ workflow_ref }}' (network or bad ref)." >&2
        exit 1
    fi
    if [ ! -s "${wtmp}" ]; then
        echo "ERROR: could not fetch composite-deploy.yaml at ref '{{ workflow_ref }}'." >&2
        exit 1
    fi
    with_keys=$(sed -n '/^    with:/,/^$/p' "${tmp}" | grep -E '^      [a-z_]+:' | sed 's/^\s*//;s/:.*//')
    for key in ${with_keys}; do
        if ! yq -r '.on.workflow_call.inputs | keys | .[]' "${wtmp}" 2>/dev/null | grep -qx "${key}"; then
            echo "ERROR: input-drift — '${key}' is not an input of composite-deploy.yaml@{{ workflow_ref }}." >&2
            echo "       Update the template or pin --workflow-ref to the last compatible ref." >&2
            exit 1
        fi
    done

    mkdir -p "$(dirname "${out_file}")"
    cp "${tmp}" "${out_file}"
    echo "Rendered ${out_file} (stack ${stack_name}, prefix ${ci_env}, workflow ref {{ workflow_ref }})."
    echo "Next: PR the file into the service repo, delete its staging-build.yaml, and merge."
