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

# Render tag-build.yaml, sync apps/*/.env.production endpoints from
# gateway/Pulumi.<stack>.yaml, set ci.yml runner to dcr-frontendx-beefy and
# tag-build.yaml runner to dcr-frontendx-prod, and open (or reuse) a pull
# request in dictyBase/frontendx.
# Usage: just frontend pr-deploy-workflow [--stack <name>] [--render-dir <dir>] [--base <branch>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("render_dir", long="render-dir", help="Directory holding rendered workflow files (default bin/ci-render)")]
[arg("base", long="base", short="b", help="Base branch for the PR (default develop)")]
[group('frontend')]
[no-cd]
pr-deploy-workflow stack="" render_dir="bin/ci-render" base="develop":
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
        exit 1
    fi

    GW_CFG="gateway/Pulumi.${STACK}.yaml"
    if [[ ! -f "$GW_CFG" ]]; then
        echo "Error: gateway stack config missing: $GW_CFG" >&2
        exit 1
    fi
    fe_en=$(yq -r '.config."gateway:properties".routes.frontend.enabled // "false"' "$GW_CFG")
    fe_host=$(yq -r '.config."gateway:properties".routes.frontend.hosts[0] // ""' "$GW_CFG")
    gql_en=$(yq -r '.config."gateway:properties".routes.graphql.enabled // "false"' "$GW_CFG")
    gql_host=$(yq -r '.config."gateway:properties".routes.graphql.hosts[0] // ""' "$GW_CFG")

    if [[ "$fe_en" != "true" || -z "$fe_host" || "$fe_host" == *"<"* || "$fe_host" == *">"* ]]; then
        echo "Error: $GW_CFG must enable routes.frontend and set a non-placeholder host." >&2
        exit 1
    fi
    if [[ "$gql_en" != "true" || -z "$gql_host" || "$gql_host" == *"<"* || "$gql_host" == *">"* ]]; then
        echo "Error: $GW_CFG must enable routes.graphql and set a non-placeholder host." >&2
        exit 1
    fi

    rdir={{ quote(render_dir) }}
    base_branch={{ quote(base) }}
    tag_file="${rdir}/frontendx-tag-build.yaml"
    if [[ ! -f "$tag_file" ]] || ! grep -q 'runner: dcr-frontendx-prod' "$tag_file"; then
        just ci render-tag-deploy \
            --stack "$STACK" \
            --app frontpage:stockcenter:publication:genomepage \
            --project frontend_application \
            --application-type frontend \
            --runner dcr-frontendx-prod \
            --docker-image dicty-frontpage:stock-center:publication:genomepage \
            --dockerfile docker/Dockerfile.frontpage:docker/Dockerfile.stock-center:docker/Dockerfile.publication:docker/Dockerfile.genomepage \
            --out "$tag_file" \
            --workflow-ref "$base_branch"
    fi

    work_dir=$(mktemp -d /tmp/frontend-pr-XXXXXX)
    trap 'rm -rf "$work_dir"' EXIT

    git_https() {
        git -c credential.helper= -c 'credential.helper=!gh auth git-credential' "$@"
    }

    repo="dictyBase/frontendx"
    repo_dir="${work_dir}/frontendx"
    echo "=== ${repo} ==="
    git_https clone --depth 1 --branch "$base_branch" "https://github.com/${repo}.git" "$repo_dir" >/dev/null 2>&1

    mkdir -p "${repo_dir}/.github/workflows"
    cp "$tag_file" "${repo_dir}/.github/workflows/tag-build.yaml"
    rm -f "${repo_dir}/.github/workflows/staging.yml"
    ci_wf="${repo_dir}/.github/workflows/ci.yml"
    if [[ -f "$ci_wf" ]]; then
        sed -i.bak -E 's|^([[:space:]]*runner:).*|\1 dcr-frontendx-beefy|' "$ci_wf"
        rm -f "${ci_wf}.bak"
    fi

    fe_url="https://${fe_host}"
    gql_url="https://${gql_host}"

    for app_dir in dicty-frontpage stock-center; do
        env_file="${repo_dir}/apps/${app_dir}/.env.production"
        sed -i.bak \
            -e "s|^VITE_APP_FRONTPAGE_URL=.*|VITE_APP_FRONTPAGE_URL=\"${fe_url}\"|" \
            -e "s|^VITE_APP_STOCKCENTER_URL=.*|VITE_APP_STOCKCENTER_URL=\"${fe_url}/stockcenter\"|" \
            -e "s|^VITE_APP_PUBLICATION_URL=.*|VITE_APP_PUBLICATION_URL=\"${fe_url}/publication\"|" \
            -e "s|^VITE_APP_GRAPHQL_SERVER=.*|VITE_APP_GRAPHQL_SERVER=\"${gql_url}\"|" \
            -e "s|^VITE_AUTH_ENABLED=.*|VITE_AUTH_ENABLED=false|" \
            "$env_file"
        rm -f "${env_file}.bak"
    done

    pub_env="${repo_dir}/apps/publication/.env.production"
    sed -i.bak \
        -e "s|^VITE_FRONTPAGE_URL=.*|VITE_FRONTPAGE_URL=\"${fe_url}\"|" \
        -e "s|^VITE_STOCKCENTER_URL=.*|VITE_STOCKCENTER_URL=\"${fe_url}/stockcenter\"|" \
        -e "s|^VITE_PUBLICATION_URL=.*|VITE_PUBLICATION_URL=\"${fe_url}/publication\"|" \
        -e "s|^VITE_GRAPHQL_SERVER=.*|VITE_GRAPHQL_SERVER='${gql_url}'|" \
        -e "s|^VITE_DEPLOY_ENV=.*|VITE_DEPLOY_ENV='production'|" \
        -e "s|^VITE_AUTH_ENABLED=.*|VITE_AUTH_ENABLED=false|" \
        "$pub_env"
    rm -f "${pub_env}.bak"

    gp_env="${repo_dir}/apps/genome-page/.env.production"
    gp_dev="${repo_dir}/apps/genome-page/.env.development"
    if ! grep -q '^VITE_GRAPHQL_SERVER=' "$gp_env" && [[ -f "$gp_dev" ]]; then
        cp "$gp_dev" "$gp_env"
    fi
    sed -i.bak \
        -e "s|^VITE_FRONTPAGE_URL=.*|VITE_FRONTPAGE_URL=\"${fe_url}\"|" \
        -e "s|^VITE_STOCKCENTER_URL=.*|VITE_STOCKCENTER_URL=\"${fe_url}/stockcenter\"|" \
        -e "s|^VITE_PUBLICATION_URL=.*|VITE_PUBLICATION_URL=\"${fe_url}/publication\"|" \
        -e "s|^VITE_GRAPHQL_SERVER=.*|VITE_GRAPHQL_SERVER='${gql_url}'|" \
        -e "s|^VITE_DEPLOY_ENV=.*|VITE_DEPLOY_ENV='production'|" \
        -e "s|^VITE_AUTH_ENABLED=.*|VITE_AUTH_ENABLED=false|" \
        "$gp_env"
    rm -f "${gp_env}.bak"

    if [[ -z "$(git -C "$repo_dir" status --porcelain)" ]]; then
        echo "  frontendx already up to date on ${base_branch} — skipping PR."
        exit 0
    fi

    head_branch="ci/tag-deploy-${STACK}"
    git_https -C "$repo_dir" fetch origin "$head_branch" >/dev/null 2>&1 || true
    git -C "$repo_dir" checkout -B "$head_branch" >/dev/null 2>&1
    git -C "$repo_dir" add -A .github/workflows apps/dicty-frontpage/.env.production apps/stock-center/.env.production apps/publication/.env.production apps/genome-page/.env.production
    git -C "$repo_dir" commit -m "ci(deploy): add production tag deploy workflow and endpoints for ${STACK}" >/dev/null
    git_https -C "$repo_dir" push -u origin "$head_branch" --force >/dev/null 2>&1

    existing_url=$(gh pr list --repo "$repo" --head "$head_branch" --state open --json url -q '.[0].url' 2>/dev/null || true)
    if [[ -n "$existing_url" ]]; then
        echo "  PR (existing): ${existing_url}"
    else
        new_url=$(gh pr create --repo "$repo" --base "$base_branch" --head "$head_branch" \
            --title "ci(deploy): add production tag deploy workflow and endpoints (${STACK})" \
            --body "Adds \`.github/workflows/tag-build.yaml\` targeting \`${STACK}\`, removes obsolete \`.github/workflows/staging.yml\`, and aligns \`apps/*/.env.production\` with \`${fe_url}\` and \`${gql_url}\`.")
        echo "  PR (created):  ${new_url}"
    fi
