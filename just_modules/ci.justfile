# CI deploy credential and variable recipes.
# Key lifecycle: source the key (reuse --sa-key, or mint via `just gcp-sa
# create-sa` / `create-sa-key`) -> preflight it -> publish it -> set the vars.
# Registry-driven: config/clusters/<cluster>.yaml is the single source of truth.

# Read-only preflight of the deployer SA key against the registry.
# Checks, in order: key parses as GCP SA JSON; key project matches the
# registry gcp_project; kubeconfig export from the kops state bucket works
# (same code path as verify-deployer-access); KMS encrypt/decrypt round-trip
# on the registry secrets key; IAM permissions of deployer-roles.txt.
# Zero mutations; fails closed naming every failure; never prints key material.
# Usage: just ci check-deploy-credentials [--cluster <name>] [--sa-key <path>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("sa_key", long="sa-key", short="k", help="Deployer service account key JSON path (defaults to credentials/<cluster>/deployer.json)")]
[group('ci-management')]
[no-cd]
check-deploy-credentials cluster="" sa_key="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name={{ quote(cluster) }}
    [ -z "${name}" ] && name="${CLUSTER_NAME:-}"
    key={{ quote(sa_key) }}
    [ -z "${key}" ] && [ -n "${name}" ] && key="credentials/${name}/deployer.json"

    go run ./cmd/cluster-ops preflight deploy-credentials \
        --cluster "${name}" \
        --sa-key "${key}"

# Publish the deployer SA key to the org secret PROD_DEPLOY_SA_KEY.
# One idempotent upsert (gh secret set) that stores the key and sets repo
# visibility (selected repositories) in a single call. Refuses a missing or
# unreadable key; never echoes key material; prints the rm reminder for the
# on-disk file. Needs an org-admin gh token (Actions secrets write).
# Usage: just ci set-deploy-secret [--cluster <name>] [--sa-key <path>] --repos <owner/name>[,<owner/name>...]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("sa_key", long="sa-key", short="k", help="Deployer service account key JSON path (defaults to credentials/<cluster>/deployer.json)")]
[arg("repos", long="repos", short="r", help="Target repositories, owner/name form, comma-separated")]
[group('ci-management')]
[no-cd]
set-deploy-secret cluster="" sa_key="" repos="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    [ -z "${name}" ] && name="${CLUSTER_NAME:-}"
    if [ -z "${name}" ]; then
        echo "ERROR: no cluster name — pass --cluster or set CLUSTER_NAME (via cluster env)." >&2
        exit 1
    fi
    key="{{ sa_key }}"
    [ -z "${key}" ] && key="credentials/${name}/deployer.json"
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

    # Verify every repo exists, then merge with any repos already selected on
    # PROD_DEPLOY_SA_KEY so single-service runs never revoke prior repos.
    # gh secret set --org <org> --repos expects bare repo names (not GraphQL IDs).
    names=()
    unknown=()
    for r in "${repos[@]}"; do
        id=$(gh repo view "${r}" --json id -q .id 2>/dev/null) || true
        if [ -z "${id}" ]; then
            unknown+=("${r}")
        else
            names+=("${r#*/}")
        fi
    done
    if [ "${#unknown[@]}" -gt 0 ]; then
        echo "ERROR: cannot resolve repo id for:" >&2
        printf '       %s\n' "${unknown[@]}" >&2
        exit 1
    fi
    existing=""
    if raw_repos=$(gh api "orgs/dictyBase/actions/secrets/PROD_DEPLOY_SA_KEY/repositories" 2>/dev/null); then
        existing=$(printf '%s\n' "${raw_repos}" | jq -r '.repositories[]?.name // empty' 2>/dev/null) || true
    fi
    for ex in ${existing}; do
        case " ${names[*]} " in
            *" ${ex} "*) ;;
            *) names+=("${ex}") ;;
        esac
    done

    gh secret set PROD_DEPLOY_SA_KEY --org dictyBase \
        --visibility selected --repos "$(IFS=,; echo "${names[*]}")" < "${key}"

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
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("repos", long="repos", short="r", help="Target repositories, owner/name form, comma-separated")]
[group('ci-management')]
[no-cd]
sync-deploy-vars cluster="" repos:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    [ -z "${name}" ] && name="${CLUSTER_NAME:-}"
    if [ -z "${name}" ]; then
        echo "ERROR: no cluster name — pass --cluster or set CLUSTER_NAME (via cluster env)." >&2
        exit 1
    fi
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
    case "${cluster_val}" in
        *.local) kops_cluster="${cluster_val}" ;;
        *)       kops_cluster="${cluster_val}-k8s.local" ;;
    esac
    kops_state=$(yq -r '.kops_state' "${entry}")
    pulumi_state=$(yq -r '.pulumi_state' "${entry}")
    kops_ver=$(yq -r '.kops_version' "${entry}")
    kubectl_ver=$(yq -r '.kubectl_version' "${entry}")
    pulumi_ver=$(yq -r '.pulumi_version' "${entry}")

    echo "Publishing variables for ${name} (prefix ${ci_env}):"
    org_var="${ci_env}_PULUMI_STATE_STORAGE"
    org_before=$(gh variable list --org dictyBase 2>/dev/null | grep "^${org_var}\s" | awk '{print $2}') || true
    gh variable set "${org_var}" --org dictyBase --visibility all --body "${pulumi_state}"
    echo "  org dictyBase: ${org_var}=${pulumi_state} (was: ${org_before:-unset})"
    for r in "${repos[@]}"; do
        for kv in \
            "${ci_env}_CLUSTER=${kops_cluster}" \
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
        sec_repos=""
        if sec_raw=$(gh api "orgs/dictyBase/actions/secrets/PROD_DEPLOY_SA_KEY/repositories" 2>/dev/null); then
            sec_repos=$(printf '%s\n' "${sec_raw}" | jq -r '.repositories[]?.name // empty' 2>/dev/null) || true
        fi
        if ! printf '%s\n' "${sec_repos}" | grep -qx "$(basename "${r}")"; then
            echo "WARN: PROD_DEPLOY_SA_KEY not visible to ${r} — fix:" >&2
            echo "      just ci set-deploy-secret --cluster ${name} --sa-key <path> --repos ${r}" >&2
        fi

        # Org var visibility: warn only — it already exists org-wide.
        repo_vars=$(gh variable list --repo "${r}" 2>/dev/null || true)
        org_vars=$(gh variable list --org dictyBase 2>/dev/null || true)
        if ! printf '%s\n%s\n' "${repo_vars}" "${org_vars}" | grep -q "^PULUMI_STATE_STORAGE\s"; then
            echo "WARN: PULUMI_STATE_STORAGE not visible to ${r} — check the org variable's visibility." >&2
        fi
    done
    echo "Variables published for ${#repos[@]} repo(s)."
    echo "Next: just ci render-tag-deploy --stack {{ cluster }} --app <app> --project <project> --out tag-build.yaml"

# Render the complete tag workflow for a service repo: deploy-only via the
# composite deploy, pointed at a production cluster through the <CI_ENV>_*
# variables. Static values come from the registry entry whose 'stack' key
# matches --stack (or $PULUMI_STACK). Also runs the input-drift guard: fetches
# composite-deploy.yaml at --workflow-ref and fails when any rendered 'with:'
# key is missing from its workflow_call inputs — a caller/reusable-workflow
# mismatch dies here, not in a production deploy run. Writes to --out; never
# edits the service repo in place. The PR and merge in the service repo stay
# manual.
# Usage: just ci render-tag-deploy [--stack <name>] --app <app> --project <project> [--docker-image <name>] --out <file> [--workflow-ref <ref>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK); must match a registry entry's stack key")]
[arg("app", long="app", short="a", help="Application name passed to the composite (e.g. order, graphql-server)")]
[arg("project", long="project", short="p", help="cluster-ops Pulumi project folder (e.g. modware-order, graphql_server)")]
[arg("docker_image", long="docker-image", short="d", help="Docker Hub image name (defaults to --project with '_' replaced by '-')")]
[arg("out", long="out", short="o", help="Output file path for the rendered tag-build.yaml")]
[arg("workflow_ref", long="workflow-ref", short="w", help="Git ref of dictyBase/workflows to pin in the output (default develop)")]
[group('ci-management')]
[no-cd]
render-tag-deploy stack="" app="" project="" out="" workflow_ref="develop" docker_image="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    [ -z "${stack_name}" ] && stack_name="${PULUMI_STACK:-}"
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — pass --stack or set PULUMI_STACK (via cluster env)." >&2
        exit 1
    fi
    app_name="{{ app }}"
    project="{{ project }}"
    out_file="{{ out }}"
    image_name="{{ docker_image }}"
    [ -z "${image_name}" ] && image_name="${project//_/-}"

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

    tmp=$(mktemp /tmp/tag-build-XXXXXX)
    wtmp=$(mktemp /tmp/composite-XXXXXX)
    trap 'rm -f "${tmp}" "${wtmp}"' EXIT
    sed \
        -e "s|__APP__|${app_name}|g" \
        -e "s|__PROJECT__|${project}|g" \
        -e "s|__STACK__|${stack_name}|g" \
        -e "s|__DOCKER_IMAGE__|${image_name}|g" \
        -e "s|__CI_ENV__|${ci_env}|g" \
        -e "s|__WORKFLOW_REF__|{{ workflow_ref }}|g" \
        config/templates/tag-build-deploy.yaml.tmpl > "${tmp}"

    # When another service in config/services.yaml shares this repository as a
    # secondary deployment (e.g. modware-annotation-feature inside
    # modware-annotation), append a call-buildless-deploy job that deploys that
    # stack with the image built by call-build-deploy.
    if [ -f "config/services.yaml" ]; then
        secondary=$(yq -r ".services[] | select(.repo == \"${project}\" and .folder != \"${project}\") | .app + \" \" + .folder" config/services.yaml 2>/dev/null || true)
        while read -r sec_app sec_folder; do
            [ -z "${sec_app}" ] && continue
            {
                echo "  call-buildless-deploy:"
                echo "    needs: call-build-deploy"
                echo "    uses: dictyBase/workflows/.github/workflows/deploy-buildless-backend.yaml@{{ workflow_ref }}"
                echo "    secrets: inherit"
                echo "    with:"
                echo "      app: ${sec_app}"
                echo "      project: ${sec_folder}"
                echo "      stack: ${stack_name}"
                echo "      repository: \${{{{ github.repository }}"
                echo "      ref: \${{{{ github.ref_name }}"
                echo "      dockerfile: build/package/Dockerfile"
                echo "      docker_image: ${image_name}"
                echo "      cluster: \${{{{ vars.${ci_env}_CLUSTER }}"
                echo "      cluster_state_storage: \${{{{ vars.${ci_env}_KOPS_STATE_STORAGE }}"
                echo "      kops_version: \${{{{ vars.${ci_env}_KOPS_VERSION }}"
                echo "      kubectl_version: \${{{{ vars.${ci_env}_KUBECTL_VERSION }}"
                echo "      pulumi_version: \${{{{ vars.${ci_env}_PULUMI_VERSION }}"
                echo "      environment: production"
                echo "      runner: backend"
            } >> "${tmp}"
        done < <(printf '%s\n' "${secondary}")
    fi

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
    if ! curl -fsSL "https://raw.githubusercontent.com/dictyBase/workflows/{{ workflow_ref }}/.github/workflows/composite-deploy.yaml" -o "${wtmp}"; then
        echo "ERROR: could not fetch composite-deploy.yaml at ref '{{ workflow_ref }}' (network or bad ref)." >&2
        exit 1
    fi
    if [ ! -s "${wtmp}" ]; then
        echo "ERROR: could not fetch composite-deploy.yaml at ref '{{ workflow_ref }}'." >&2
        exit 1
    fi
    with_keys=$(sed -n '/^    with:/,/^$/p' "${tmp}" | grep -E '^      [a-z_]+:' | sed 's/^\s*//;s/:.*//')
    # Do not pipe yq into grep -q. pipefail plus grep -q closes the pipe.
    # yq 4 then gets SIGPIPE and the guard reports a false input-drift error.
    input_keys=$(yq -r '.on.workflow_call.inputs | keys | .[]' "${wtmp}")
    nl=$(printf '\n')
    for key in ${with_keys}; do
        case "${nl}${input_keys}${nl}" in
            *"${nl}${key}${nl}"*) ;;
            *)
                echo "ERROR: input-drift — '${key}' is not an input of composite-deploy.yaml@{{ workflow_ref }}." >&2
                echo "       Update the template or pin --workflow-ref to the last compatible ref." >&2
                exit 1
                ;;
        esac
    done

    mkdir -p "$(dirname "${out_file}")"
    cp "${tmp}" "${out_file}"
    echo "Rendered ${out_file} (stack ${stack_name}, prefix ${ci_env}, workflow ref {{ workflow_ref }})."
    echo "Next: PR the file into the service repo, delete its staging-build.yaml, and merge."

# Publish the org variable <CI_ENV>_PULUMI_STATE_STORAGE from the cluster
# registry and render dictyBase/workflows composite-deploy.yaml (and
# deploy-buildless-backend.yaml) with production secret (<CI_ENV>_DEPLOY_SA_KEY)
# and Pulumi state (<CI_ENV>_PULUMI_STATE_STORAGE) selection for
# inputs.environment == 'production'.
# Idempotent: if the workflow files at --workflow-ref already carry both
# selections, the rendered files match upstream and the recipe reports no PR
# is needed.
# Usage: just ci render-composite-deploy [--cluster <name>] [--out <file>] [--workflow-ref <ref>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("out", long="out", short="o", help="Output file path for the rendered composite-deploy.yaml (default bin/ci-render/composite-deploy.yaml)")]
[arg("workflow_ref", long="workflow-ref", short="w", help="Git ref of dictyBase/workflows to fetch (default develop)")]
[group('ci-management')]
[no-cd]
render-composite-deploy cluster="" out="bin/ci-render/composite-deploy.yaml" workflow_ref="develop":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    [ -z "${name}" ] && name="${CLUSTER_NAME:-}"
    if [ -z "${name}" ]; then
        echo "ERROR: no cluster name — pass --cluster or set CLUSTER_NAME (via cluster env)." >&2
        exit 1
    fi
    entry="config/clusters/${name}.yaml"
    if [ ! -f "${entry}" ]; then
        echo "ERROR: no registry entry for cluster '${name}' — expected ${entry}." >&2
        exit 1
    fi
    ci_env=$(yq -r '.ci_env' "${entry}")
    pulumi_state=$(yq -r '.pulumi_state' "${entry}")
    out_file="{{ out }}"

    org_var="${ci_env}_PULUMI_STATE_STORAGE"
    before=$(gh variable list --org dictyBase 2>/dev/null | grep "^${org_var}\s" | awk '{print $2}') || true
    gh variable set "${org_var}" --org dictyBase --visibility all --body "${pulumi_state}"
    echo "Published org variable ${org_var}=${pulumi_state} (was: ${before:-unset})."

    wtmp=$(mktemp /tmp/comp-orig-XXXXXX)
    tmp=$(mktemp /tmp/comp-new-XXXXXX)
    btmp=$(mktemp /tmp/buildless-orig-XXXXXX)
    bnew=$(mktemp /tmp/buildless-new-XXXXXX)
    trap 'rm -f "${wtmp}" "${tmp}" "${btmp}" "${bnew}"' EXIT
    if ! curl -fsSL "https://raw.githubusercontent.com/dictyBase/workflows/{{ workflow_ref }}/.github/workflows/composite-deploy.yaml" -o "${wtmp}"; then
        echo "ERROR: could not fetch composite-deploy.yaml at ref '{{ workflow_ref }}' (network or bad ref)." >&2
        exit 1
    fi
    if [ ! -s "${wtmp}" ]; then
        echo "ERROR: could not fetch composite-deploy.yaml at ref '{{ workflow_ref }}'." >&2
        exit 1
    fi

    sed \
        -e "s|credentials_json: \${{{{ secrets.DEPLOY_SA_KEY }}|credentials_json: \${{{{ inputs.environment == 'production' \&\& secrets.${ci_env}_DEPLOY_SA_KEY \|\| secrets.DEPLOY_SA_KEY }}|g" \
        -e "s|--pulumi-state \${{{{ vars.PULUMI_STATE_STORAGE }}|--pulumi-state \${{{{ inputs.environment == 'production' \&\& vars.${org_var} \|\| vars.PULUMI_STATE_STORAGE }}|g" \
        "${wtmp}" > "${tmp}"

    yq -r '.' "${tmp}" >/dev/null
    if ! grep -q "secrets.${ci_env}_DEPLOY_SA_KEY" "${tmp}" || ! grep -q "vars.${org_var}" "${tmp}"; then
        echo "ERROR: rendered composite-deploy.yaml is missing ${ci_env}_DEPLOY_SA_KEY or ${org_var} routing." >&2
        exit 1
    fi

    mkdir -p "$(dirname "${out_file}")"
    cp "${tmp}" "${out_file}"

    # Also render deploy-buildless-backend.yaml alongside composite-deploy.yaml when available.
    buildless_out="$(dirname "${out_file}")/deploy-buildless-backend.yaml"
    if curl -fsSL "https://raw.githubusercontent.com/dictyBase/workflows/{{ workflow_ref }}/.github/workflows/deploy-buildless-backend.yaml" -o "${btmp}" 2>/dev/null && [ -s "${btmp}" ]; then
        sed \
            -e "s|credentials_json: \${{{{ secrets.DEPLOY_SA_KEY }}|credentials_json: \${{{{ inputs.environment == 'production' \&\& secrets.${ci_env}_DEPLOY_SA_KEY \|\| secrets.DEPLOY_SA_KEY }}|g" \
            -e "s|--pulumi-state \${{{{ vars.PULUMI_STATE_STORAGE }}|--pulumi-state \${{{{ inputs.environment == 'production' \&\& vars.${org_var} \|\| vars.PULUMI_STATE_STORAGE }}|g" \
            -e "s|runs-on: ubuntu-latest|runs-on: \${{{{ inputs.runner }}|g" \
            "${btmp}" > "${bnew}"
        if ! grep -q '^      runner:' "${bnew}"; then
            yq -i '.on.workflow_call.inputs.runner = {"type": "string", "default": "ubuntu-latest", "description": "Runner for the job"}' "${bnew}"
        fi
        yq -r '.' "${bnew}" >/dev/null
        cp "${bnew}" "${buildless_out}"
    fi

    if cmp -s "${wtmp}" "${tmp}"; then
        echo "composite-deploy.yaml@{{ workflow_ref }} already routes ${ci_env}_DEPLOY_SA_KEY and ${org_var} (no PR needed)."
    else
        echo "Rendered ${out_file} (routes ${ci_env}_DEPLOY_SA_KEY and ${org_var} when environment == 'production')."
        echo "Next: just ci pr-deploy-workflows --stack ${name}"
    fi

# Open (or update) pull requests in dictyBase/workflows and each selected
# service repo from the rendered files in bin/ci-render/. Renders any missing
# file first; clones each target repo into a throwaway directory, writes
# .github/workflows/composite-deploy.yaml (in dictyBase/workflows) and
# .github/workflows/tag-build.yaml (plus removes staging-build.yaml in each
# service repo), pushes the branch over HTTPS via gh git-credential, and opens
# the PR against --base. Idempotent: skips repos with no diff and reuses any
# open PR for the branch.
# Usage: just ci pr-deploy-workflows [--stack <name>] [--services <a,b>] [--render-dir <dir>] [--base <branch>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("services", long="services", help="Comma-separated subset of apps from config/services.yaml (default: all)")]
[arg("render_dir", long="render-dir", help="Directory holding rendered workflow files (default bin/ci-render)")]
[arg("base", long="base", short="b", help="Base branch for PRs (default develop)")]
[group('ci-management')]
[no-cd]
pr-deploy-workflows stack="" services="" render_dir="bin/ci-render" base="develop":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    [ -z "${stack_name}" ] && stack_name="${PULUMI_STACK:-}"
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — pass --stack or set PULUMI_STACK (via cluster env)." >&2
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
    cluster_name=$(yq -r '.cluster' "${entry}")

    rdir={{ quote(render_dir) }}
    base_branch={{ quote(base) }}
    wanted="{{ services }}"

    all_services=$(yq -r '.services[] | .app + " " + .folder + " " + (.repo // .folder)' config/services.yaml)
    if [ -z "${all_services}" ]; then
        echo "ERROR: no services in config/services.yaml." >&2
        exit 1
    fi

    work_dir=$(mktemp -d /tmp/ci-pr-XXXXXX)
    trap 'rm -rf "${work_dir}"' EXIT

    git_https() {
        git -c credential.helper= -c 'credential.helper=!gh auth git-credential' "$@"
    }

    open_or_reuse_pr() {
        local repo="$1" head_branch="$2" target_base="$3" title="$4" body="$5"
        local existing_url=""
        existing_url=$(gh pr list --repo "${repo}" --head "${head_branch}" --state open --json url -q '.[0].url' 2>/dev/null || true)
        if [ -n "${existing_url}" ]; then
            echo "  PR (existing): ${existing_url}"
        else
            local new_url=""
            new_url=$(gh pr create --repo "${repo}" --base "${target_base}" --head "${head_branch}" --title "${title}" --body "${body}")
            echo "  PR (created):  ${new_url}"
        fi
    }

    # 1. dictyBase/workflows composite-deploy.yaml
    comp_file="${rdir}/composite-deploy.yaml"
    if [ ! -f "${comp_file}" ]; then
        just ci render-composite-deploy --cluster "${cluster_name}" --out "${comp_file}" --workflow-ref "${base_branch}"
    fi
    echo "=== dictyBase/workflows ==="
    wf_dir="${work_dir}/workflows"
    git_https clone --depth 1 --branch "${base_branch}" "https://github.com/dictyBase/workflows.git" "${wf_dir}" >/dev/null 2>&1
    mkdir -p "${wf_dir}/.github/workflows"
    cp "${comp_file}" "${wf_dir}/.github/workflows/composite-deploy.yaml"
    buildless_file="${rdir}/deploy-buildless-backend.yaml"
    [ -f "${buildless_file}" ] && cp "${buildless_file}" "${wf_dir}/.github/workflows/deploy-buildless-backend.yaml"
    if [ -z "$(git -C "${wf_dir}" status --porcelain)" ]; then
        echo "  workflows already up to date on ${base_branch} — skipping PR."
    else
        wf_branch="ci/prod-deploy-${stack_name}"
        git -C "${wf_dir}" checkout -B "${wf_branch}" >/dev/null 2>&1
        git -C "${wf_dir}" add .github/workflows/composite-deploy.yaml
        [ -f "${wf_dir}/.github/workflows/deploy-buildless-backend.yaml" ] && git -C "${wf_dir}" add .github/workflows/deploy-buildless-backend.yaml
        git -C "${wf_dir}" commit -m "ci(deploy): route production secret and pulumi state for ${stack_name}" >/dev/null
        git_https -C "${wf_dir}" push -u origin "${wf_branch}" --force-with-lease >/dev/null 2>&1
        open_or_reuse_pr "dictyBase/workflows" "${wf_branch}" "${base_branch}" \
            "ci(deploy): route production secret and pulumi state (${stack_name})" \
            "Routes \`PROD_DEPLOY_SA_KEY\` and \`PROD_PULUMI_STATE_STORAGE\` when \`inputs.environment == 'production'\`."
    fi

    # 2. Each selected service repo tag-build.yaml (+ remove staging-build.yaml)
    matched=0
    while read -r app folder repo_name; do
        [ -z "${app}" ] && continue
        if [ -n "${wanted}" ]; then
            case ",${wanted}," in
                *",${app},"*) : ;;
                *) continue ;;
            esac
        fi
        matched=$((matched + 1))
        # Secondary services that share a repo (e.g. modware-annotation-feature)
        # are rendered inside the primary repo's tag-build.yaml.
        [ "${repo_name}" != "${folder}" ] && continue
        tag_file="${rdir}/${folder}-tag-build.yaml"
        if [ ! -f "${tag_file}" ]; then
            just ci render-tag-deploy --stack "${stack_name}" --app "${app}" \
                --project "${folder}" --out "${tag_file}" --workflow-ref "${base_branch}"
        fi
        echo "=== dictyBase/${folder} ==="
        svc_dir="${work_dir}/${folder}"
        git_https clone --depth 1 --branch "${base_branch}" "https://github.com/dictyBase/${folder}.git" "${svc_dir}" >/dev/null 2>&1
        mkdir -p "${svc_dir}/.github/workflows"
        cp "${tag_file}" "${svc_dir}/.github/workflows/tag-build.yaml"
        rm -f "${svc_dir}/.github/workflows/staging-build.yaml"
        if [ -z "$(git -C "${svc_dir}" status --porcelain)" ]; then
            echo "  tag-build.yaml already up to date on ${base_branch} — skipping PR."
        else
            svc_branch="ci/tag-deploy-${stack_name}"
            git -C "${svc_dir}" checkout -B "${svc_branch}" >/dev/null 2>&1
            git -C "${svc_dir}" add -A .github/workflows
            git -C "${svc_dir}" commit -m "ci(deploy): add production tag deploy workflow for ${stack_name}" >/dev/null
            git_https -C "${svc_dir}" push -u origin "${svc_branch}" --force-with-lease >/dev/null 2>&1
            open_or_reuse_pr "dictyBase/${folder}" "${svc_branch}" "${base_branch}" \
                "ci(deploy): add production tag deploy workflow (${stack_name})" \
                "Replaces \`tag-build.yaml\` with \`composite-deploy\` targeting \`${stack_name}\` and removes obsolete \`staging-build.yaml\`."
        fi
    done < <(printf '%s\n' "${all_services}")

    if [ "${matched}" -eq 0 ]; then
        echo "ERROR: no services matched --services '${wanted}' in config/services.yaml." >&2
        exit 1
    fi
    echo "All workflow PRs ready for ${stack_name}. Merge them, push cluster-ops develop first, then cut tags."

# Print the highest semver tag of a repository, verbatim (v-prefix or bare).
# Fetches all tags, normalizes the prefix only for ordering, sorts
# numerically, and prints the winner. Fails naming the repo when the tag
# list is empty or fetch fails. Deploy recipes use this as the default
# --image-tag source.
# Usage: just ci latest-tag --repo <owner/name>
[arg("repo", long="repo", short="r", help="Repository in owner/name form")]
[group('ci-management')]
[no-cd]
latest-tag repo:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    r="{{ repo }}"
    if ! [[ "${r}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
        echo "ERROR: '${r}' is not in owner/name form." >&2
        exit 1
    fi
    tags=$(gh api "repos/${r}/tags" --paginate --jq '.[].name' 2>/dev/null) || {
        echo "ERROR: could not list tags for ${r} (gh auth or network)." >&2
        exit 1
    }
    best=$(printf '%s\n' "${tags}" | awk '
        /^[vV]?[0-9]+(\.[0-9]+)*$/ {
            v = $0; sub(/^[vV]/, "", v)
            n = split(v, a, ".")
            printf "%06d%06d%06d %s\n", a[1]+0, a[2]+0, a[3]+0, $0
        }' | sort | tail -n 1 | awk '{print $2}')
    if [ -z "${best}" ]; then
        echo "ERROR: no semver tags found for ${r}." >&2
        exit 1
    fi
    echo "${best}"

# Create (or reuse) the deployer service account and key for a cluster,
# then verify the key with check-deploy-credentials.
# Key lands at the standard credentials/<cluster>/deployer.json path (or --sa-key).
# Idempotent: an existing key file is reused (SA keys cap at 10 — never
# re-mint when the file is present); a missing SA is created with the
# deployer-roles.txt role set. Ends by running check-deploy-credentials so
# the key is verified before returning. Run inside the cluster-env shell
# (gcloud identity comes from it).
# Usage: just ci create-deploy-key [--cluster <name>] [--sa-name <name>] [--sa-key <path>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("sa_name", long="sa-name", short="s", help="Deployer service account name (default deployer)")]
[arg("sa_key", long="sa-key", short="k", help="Deployer service account key JSON path (defaults to credentials/<cluster>/deployer.json)")]
[group('ci-management')]
[no-cd]
create-deploy-key cluster="" sa_name="deployer" sa_key="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    name="{{ cluster }}"
    [ -z "${name}" ] && name="${CLUSTER_NAME:-}"
    if [ -z "${name}" ]; then
        echo "ERROR: no cluster name — pass --cluster or set CLUSTER_NAME (via cluster env)." >&2
        exit 1
    fi
    sa="{{ sa_name }}"
    entry="config/clusters/${name}.yaml"
    if [ ! -f "${entry}" ]; then
        echo "ERROR: no registry entry for cluster '${name}' — expected ${entry}." >&2
        exit 1
    fi
    project=$(yq -r '.gcp_project' "${entry}")

    key_file="{{ sa_key }}"
    [ -z "${key_file}" ] && key_file="credentials/${name}/deployer.json"
    if [ -f "${key_file}" ]; then
        if ! jq -e '.project_id' "${key_file}" >/dev/null 2>&1; then
            echo "ERROR: ${key_file} exists but is not a GCP SA key JSON — remove it if you own it." >&2
            exit 1
        fi
        echo "Reusing existing key: ${key_file} (never re-mint — SA keys cap at 10)."
    else
        sa_email="${sa}@${project}.iam.gserviceaccount.com"
        if gcloud iam service-accounts describe "${sa_email}" --project "${project}" >/dev/null 2>&1; then
            echo "Service account ${sa_email} exists — reusing."
        else
            just gcp-sa create-sa --project "${project}" --sa-name "${sa}" \
                --roles-file gcs-files/roles-permissions/deployer-roles.txt
        fi

        mkdir -p "$(dirname "${key_file}")"
        just gcp-sa create-sa-key --project "${project}" --sa-name "${sa}" --key-file "${key_file}"
        if ! jq -e '.project_id' "${key_file}" >/dev/null 2>&1; then
            echo "ERROR: ${key_file} is not a valid SA key JSON after minting." >&2
            exit 1
        fi
        echo "Deployer key ready: ${key_file} (SA ${sa_email}, project ${project})."
    fi

    just ci check-deploy-credentials --cluster "${name}" --sa-key "${key_file}"

# Deploy every backend service in config/services.yaml to a cluster, in one
# run. Per service: scaffold (skipped when the stack file exists), prereq
# gate, bootstrap-service (converges; verifies-only when the deployment
# already runs the wanted tag), sync-deploy-vars, tag workflow render. Once
# per run: set-deploy-secret over all target repos. Image tags come from
# --image-tag or, per service, the repo's highest semver tag (just ci
# latest-tag). Idempotent: re-running converges — skips prior progress,
# never fails on it. Run inside the cluster-env shell; create-deploy-key
# must have run once for the cluster.
# Usage: just ci deploy-backend-services --stack <name> [--image-tag <tag>] [--services <a,b>] [--arango-service <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("image_tag", long="image-tag", short="t", help="Deploy this tag for every service (default: each repo's highest semver tag)")]
[arg("services", long="services", help="Comma-separated subset of apps from config/services.yaml (default: all)")]
[arg("arango_service", long="arango-service", short="a", help="ArangoDB Service name for the prereq gate (default arangodb)")]
[group('ci-management')]
[no-cd]
deploy-backend-services stack="" image_tag="" services="" arango_service="arangodb":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    [ -z "${stack_name}" ] && stack_name="${PULUMI_STACK:-}"
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — pass --stack or set PULUMI_STACK (via cluster env)." >&2
        exit 1
    fi

    # Registry + deployer key must exist up front.
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
    cluster_name=$(yq -r '.cluster' "${entry}")
    key_file="credentials/${cluster_name}/deployer.json"
    if [ ! -f "${key_file}" ]; then
        echo "ERROR: deployer key missing: ${key_file}" >&2
        echo "       Create it once: just ci create-deploy-key --cluster ${cluster_name}" >&2
        exit 1
    fi

    # Service manifest: app, folder, port, repo_name tuples.
    all_services=$(yq -r '.services[] | .app + " " + .folder + " " + (.port | tostring) + " " + (.repo // .folder)' config/services.yaml)
    if [ -z "${all_services}" ]; then
        echo "ERROR: no services in config/services.yaml." >&2
        exit 1
    fi
    wanted="{{ services }}"
    want_tag="{{ image_tag }}"
    arango_svc="{{ arango_service }}"
    repos=()
    while read -r app folder port repo_name; do
        [ -z "${app}" ] && continue
        if [ -n "${wanted}" ]; then
            case ",${wanted}," in
                *",${app},"*) : ;;
                *) continue ;;
            esac
        fi
        case " ${repos[*]+"${repos[*]}"} " in
            *" dictyBase/${repo_name} "*) ;;
            *) repos+=("dictyBase/${repo_name}") ;;
        esac

        cfg="${folder}/Pulumi.${stack_name}.yaml"
        if [ -f "${cfg}" ]; then
            echo "=== ${app}: stack config present, skipping scaffold ==="
        else
            echo "=== ${app}: scaffolding (port ${port}) ==="
            just gcp-pulumi scaffold-backend-stack --stack "${stack_name}" \
                --folder "${folder}" --port "${port}"
        fi

        echo "=== ${app}: prerequisite gate ==="
        just gcp-pulumi check-backend-prereqs --stack "${stack_name}" \
            --folder "${folder}" --arango-service "${arango_svc}"

        echo "=== ${app}: bootstrap deploy ==="
        if [ -n "${want_tag}" ]; then
            # Batch path: one `deploy backend` call after the loop deploys all
            # services serially and stops at the first failure.
            echo "queued for batch deploy"
        else
            just gcp-pulumi bootstrap-service --stack "${stack_name}" \
                --folder "${folder}"
        fi

        if [ "${repo_name}" = "${folder}" ]; then
            echo "=== ${app}: variables ==="
            just ci sync-deploy-vars --cluster "${cluster_name}" --repos "dictyBase/${repo_name}"

            echo "=== ${app}: tag workflow render ==="
            mkdir -p bin/ci-render
            just ci render-tag-deploy --stack "${stack_name}" --app "${app}" \
                --project "${folder}" --out "bin/ci-render/${folder}-tag-build.yaml"
        fi
    done < <(printf '%s\n' "${all_services}")

    if [ "${#repos[@]}" -eq 0 ]; then
        echo "ERROR: no services matched --services '${wanted}' in config/services.yaml." >&2
        exit 1
    fi

    if [ -n "${want_tag}" ]; then
        echo "=== batch deploy (serial, stop on first failure) ==="
        go run ./cmd/cluster-ops deploy backend --services-file config/services.yaml \
            --services "{{ services }}" --stack "${stack_name}" \
            --image-tag "${want_tag}" --arango-service "${arango_svc}"
    fi

    echo "=== deployer key visibility for ${#repos[@]} repo(s) ==="
    just ci set-deploy-secret --cluster "${cluster_name}" \
        --sa-key "${key_file}" --repos "$(IFS=,; echo "${repos[*]}")"

    echo "All requested services converged on ${stack_name}."
    echo "Rendered workflows: bin/ci-render/<folder>-tag-build.yaml — PR each into its service repo."
    echo "Next: merge the PRs, push cluster-ops develop first, then cut tags (release process)."
