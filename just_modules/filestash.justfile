# Recipes for the filestash Pulumi project. Guide: docs/filestash-deploy.md.
# Everything assumes the cluster-env sub-shell so Pulumi and Kubernetes target
# one cluster. Every value comes from filestash/Pulumi.<stack>.yaml and
# config/clusters/<cluster>.yaml.

plugins_lock_default := "filestash/plugins/plugins.lock.yaml"
office_lock_default := "filestash/plugins/office-deps.lock.yaml"

# Build the plugin bundle image from the lock files and push it.
# Downloads every vendor ZIP and LibreOffice file into a temporary build
# context and checks its sha256 first. Refuses to build while any lock entry
# has an empty sha256, license, or reviewer. Prints the digest for
# plugins.image in the stack file.
# Usage: just filestash build-plugins --tag <t> [--image <name>] [--push yes|no]
[arg("tag", long="tag", short="t", help="Bundle image tag (required), for example 2026.11.17")]
[arg("image", long="image", help="Bundle image name (default dictybase/filestash-plugins)")]
[arg("push", long="push", help="Push the image and print its digest: yes|no (default yes)")]
[arg("lock", long="lock", help="Plugin lock file (default filestash/plugins/plugins.lock.yaml)")]
[arg("office_lock", long="office-lock", help="Office file lock (default filestash/plugins/office-deps.lock.yaml)")]
[group('filestash')]
[no-cd]
build-plugins tag="" image="dictybase/filestash-plugins" push="yes" lock="" office_lock="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    TAG={{ quote(tag) }}
    IMAGE={{ quote(image) }}
    PUSH={{ quote(push) }}
    LOCK={{ quote(lock) }}
    OFFICE_LOCK={{ quote(office_lock) }}
    [[ -n "$LOCK" ]] || LOCK={{ quote(plugins_lock_default) }}
    [[ -n "$OFFICE_LOCK" ]] || OFFICE_LOCK={{ quote(office_lock_default) }}
    if [[ -z "$TAG" ]]; then
        echo "Error: no bundle tag — pass --tag." >&2
        exit 1
    fi
    if [[ ! "$TAG" =~ ^[A-Za-z0-9._-]+$ || "$TAG" == "latest" ]]; then
        echo "Error: invalid --tag '$TAG' (letters, digits, . _ -; not latest)." >&2
        exit 1
    fi
    [[ "$PUSH" == "yes" || "$PUSH" == "no" ]] || { echo "Error: --push must be yes or no." >&2; exit 1; }
    for f in "$LOCK" "$OFFICE_LOCK"; do
        [[ -f "$f" ]] || { echo "Error: lock file $f not found." >&2; exit 1; }
    done

    # Gate: every entry must be pinned, licensed, and reviewed.
    problems=$(
        yq -r '.plugins[] | select((.sha256 | test("^[0-9a-f]{64}$") | not)) | "\(.name): sha256"' "$LOCK"
        yq -r '.plugins[] | select(.license == "" or .license == null) | "\(.name): license"' "$LOCK"
        yq -r '.plugins[] | select(.reviewer == "" or .reviewer == null) | "\(.name): reviewer"' "$LOCK"
        yq -r '.office | select(.reviewer == "" or .reviewer == null) | "\(.name): reviewer"' "$LOCK"
        yq -r '.office | select((.ref | test("^[0-9a-f]{40}$") | not)) | "\(.name): ref"' "$LOCK"
        yq -r '.files[] | select((.sha256 | test("^[0-9a-f]{64}$") | not)) | "\(.file): sha256"' "$OFFICE_LOCK"
    )
    if [[ -n "$problems" ]]; then
        echo "Error: lock entries are not ready (fill them, then build again):" >&2
        printf '%s\n' "$problems" | sed 's/^/  /' >&2
        echo "  Office checksums: just filestash lock-office-deps" >&2
        exit 1
    fi

    sha256_of() {
        if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
    }
    fetch_checked() {
        local url="$1" dest="$2" want="$3" got
        curl -fsSL --retry 3 -o "$dest" "$url"
        got=$(sha256_of "$dest")
        if [[ "$got" != "$want" ]]; then
            echo "Error: checksum mismatch for $url" >&2
            echo "  want $want" >&2
            echo "  got  $got" >&2
            exit 1
        fi
    }

    ctx="$(mktemp -d "${TMPDIR:-/tmp}/filestash-plugins.XXXXXX")"
    trap 'rm -rf "$ctx"' EXIT
    mkdir -p "$ctx/vendor" "$ctx/lowa"
    while IFS=$'\t' read -r name url sum; do
        echo "Fetching $name ..."
        fetch_checked "$url" "$ctx/vendor/$name.zip" "$sum"
    done < <(yq -r '.plugins[] | [.name, .url, .sha256] | @tsv' "$LOCK")
    while IFS=$'\t' read -r file url sum; do
        echo "Fetching office file $file ..."
        fetch_checked "$url" "$ctx/lowa/$file" "$sum"
    done < <(yq -r '.files[] | [.file, .url, .sha256] | @tsv' "$OFFICE_LOCK")
    cp filestash/plugins/Dockerfile "$ctx/Dockerfile"

    REF=$(yq -r '.office.ref' "$LOCK")
    PUSH_FLAG=()
    [[ "$PUSH" == "yes" ]] && PUSH_FLAG=(--push)
    docker buildx build --platform linux/amd64 \
        --build-arg "FILESTASH_REF=$REF" \
        -t "$IMAGE:$TAG" "${PUSH_FLAG[@]}" "$ctx"

    if [[ "$PUSH" == "yes" ]]; then
        # The manifest digest is the sha256 of the raw manifest bytes.
        docker buildx imagetools inspect "$IMAGE:$TAG" --raw > "$ctx/manifest.json"
        DIGEST="sha256:$(sha256_of "$ctx/manifest.json")"
        echo "Built and pushed $IMAGE:$TAG@$DIGEST"
        echo "Next: just gcp-pulumi set-config --folder filestash --key properties.plugins.image.tag --value $TAG"
        echo "      just gcp-pulumi set-config --folder filestash --key properties.plugins.image.digest --value $DIGEST"
    else
        echo "Built $IMAGE:$TAG (not pushed)."
    fi

# Download the LibreOffice WASM files for application_office and write their
# sha256 into the office lock file. Run it on a host that can reach the CDN,
# then review the diff before you commit it.
# Usage: just filestash lock-office-deps
[arg("office_lock", long="office-lock", help="Office file lock (default filestash/plugins/office-deps.lock.yaml)")]
[group('filestash')]
[no-cd]
lock-office-deps office_lock="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    OFFICE_LOCK={{ quote(office_lock) }}
    [[ -n "$OFFICE_LOCK" ]] || OFFICE_LOCK={{ quote(office_lock_default) }}
    [[ -f "$OFFICE_LOCK" ]] || { echo "Error: lock file $OFFICE_LOCK not found." >&2; exit 1; }
    sha256_of() {
        if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
    }
    tmp="$(mktemp -d "${TMPDIR:-/tmp}/filestash-office.XXXXXX")"
    trap 'rm -rf "$tmp"' EXIT
    count=$(yq -r '.files | length' "$OFFICE_LOCK")
    for ((i = 0; i < count; i++)); do
        url=$(yq -r ".files[$i].url" "$OFFICE_LOCK")
        file=$(yq -r ".files[$i].file" "$OFFICE_LOCK")
        curl -fsSL --retry 3 -o "$tmp/$file" "$url"
        sum=$(sha256_of "$tmp/$file")
        # Rewrite the sha256 line that follows this url line; keeps comments.
        awk -v url="$url" -v sum="$sum" '
            hit && /^ *sha256:/ { sub(/sha256:.*/, "sha256: " sum); hit = 0 }
            $0 ~ "^ *url: " && $2 == url { hit = 1 }
            { print }
        ' "$OFFICE_LOCK" > "$tmp/lock.yaml"
        cp "$tmp/lock.yaml" "$OFFICE_LOCK"
        echo "  $file $sum"
    done
    echo "Updated $OFFICE_LOCK. Review the diff, then: just filestash build-plugins --tag <t>"

# Read-only preflight: the namespace, Secret filestash with ADMIN_PASSWORD and
# CONFIG_SECRET, Secret minio-root, and Service minio.
# Usage: just filestash check [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
check stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    kubectl get namespace "$NS" >/dev/null 2>&1 || fs_die "namespace '$NS' not found."
    for key in ADMIN_PASSWORD CONFIG_SECRET; do
        [[ -n "$(fs_secret_value filestash "$key")" ]] || fs_die "Secret filestash has no $key in '$NS' — run: just filestash create-secret"
    done
    fs_secret_exists minio-root || fs_die "Secret minio-root not found in '$NS'."
    kubectl -n "$NS" get service minio >/dev/null 2>&1 || fs_die "service/minio not found in '$NS'."
    echo "filestash prerequisites ready for stack '$STACK' in namespace '$NS'."
    echo "Next: just filestash deploy"

# Create Secret filestash: the bcrypt hash of the admin password (hidden
# prompt) and a random CONFIG_SECRET. If the Secret exists, keep its
# CONFIG_SECRET and replace only ADMIN_PASSWORD: a new CONFIG_SECRET breaks
# every encrypted value in config.json.
# Usage: just filestash create-secret [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
create-secret stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    command -v htpasswd >/dev/null 2>&1 || fs_die "htpasswd not found (Apache utils) — it makes the bcrypt hash."
    kubectl get namespace "$NS" >/dev/null 2>&1 || fs_die "namespace '$NS' not found."
    read -rsp "Filestash admin password (input hidden): " pw
    echo
    [[ ${#pw} -ge 12 ]] || fs_die "the admin password needs at least 12 characters."
    hash=$(printf '%s' "$pw" | htpasswd -niBC 10 "" | tr -d ':\n')
    unset pw
    [[ "$hash" == \$2* ]] || fs_die "htpasswd did not return a bcrypt hash."
    config_secret=$(fs_secret_value filestash CONFIG_SECRET)
    action=created
    if [[ -n "$config_secret" ]]; then
        echo "Secret filestash exists: keeping CONFIG_SECRET, replacing ADMIN_PASSWORD."
        action=updated
    else
        config_secret=$(fs_random 32)
    fi
    envfile="$(mktemp "${TMPDIR:-/tmp}/filestash-secret.XXXXXX")"
    trap 'rm -f "$envfile"' EXIT
    chmod 600 "$envfile"
    printf 'ADMIN_PASSWORD=%s\nCONFIG_SECRET=%s\n' "$hash" "$config_secret" > "$envfile"
    kubectl -n "$NS" create secret generic filestash --from-env-file="$envfile" \
        --dry-run=client -o yaml \
        | kubectl label --local -f - app.kubernetes.io/name=filestash -o yaml \
        | kubectl apply -f -
    echo "Secret filestash $action in '$NS'."
    echo "Next: just filestash create-buckets"

# Create the uploads and downloads buckets. uploads stays private. The public
# policy on downloads is a separate step: just filestash publish-bucket.
# Usage: just filestash create-buckets [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
create-buckets stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fs_minio_root '
        mc mb --ignore-existing minio/uploads
        mc mb --ignore-existing minio/downloads
        mc anonymous set none minio/uploads
    '
    echo "Buckets uploads (private) and downloads ready in '$NS'."
    echo "Next: just filestash create-users"

# Create the four MinIO users and their policies (filestash/policies/*.json).
# Keys live in Secret filestash-users. A second run reuses the stored keys:
# a new key stops every share that staff made with the old one.
# Usage: just filestash create-users [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
create-users stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    roles=(intake public operator publisher)
    if fs_secret_exists filestash-users; then
        echo "Secret filestash-users exists: reusing the stored keys."
    else
        envfile="$(mktemp "${TMPDIR:-/tmp}/filestash-users.XXXXXX")"
        trap 'rm -f "$envfile"' EXIT
        chmod 600 "$envfile"
        for role in "${roles[@]}"; do
            printf '%sAccessKey=filestash-%s\n%sSecretKey=%s\n' "$role" "$role" "$role" "$(fs_random 40)" >> "$envfile"
        done
        kubectl -n "$NS" create secret generic filestash-users --from-env-file="$envfile"
        kubectl -n "$NS" label secret filestash-users app.kubernetes.io/name=filestash
        echo "Secret filestash-users created in '$NS'."
    fi
    for role in "${roles[@]}"; do
        ak=$(fs_secret_value filestash-users "${role}AccessKey")
        sk=$(fs_secret_value filestash-users "${role}SecretKey")
        [[ -n "$ak" && -n "$sk" ]] || fs_die "Secret filestash-users has no $role keys."
        export "FS_AK_${role}=$ak" "FS_SK_${role}=$sk"
    done
    fs_minio_root '
        for role in intake public operator publisher; do
            ak_var="FS_AK_${role}"; sk_var="FS_SK_${role}"
            mc admin policy create minio "filestash-${role}" "filestash/policies/${role}.json"
            printf "%s\n%s\n" "${!ak_var}" "${!sk_var}" | mc admin user add minio
            if mc admin user info minio "${!ak_var}" --json | jq -e --arg p "filestash-${role}" \
                "(.policyName // \"\") | split(\",\") | index(\$p)" >/dev/null; then
                echo "  ${!ak_var}: policy filestash-${role} already attached"
            else
                mc admin policy attach minio "filestash-${role}" --user "${!ak_var}"
            fi
        done
    '
    echo "MinIO users filestash-{intake,public,operator,publisher} ready in '$NS'."
    echo "Next: just filestash deploy"

# Set the anonymous read policy on downloads: s3:GetObject on downloads/*
# only. No anonymous list. Direct file URLs then work on the MinIO host.
# Usage: just filestash publish-bucket [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
publish-bucket stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fs_minio_root 'mc anonymous set-json filestash/policies/downloads-anonymous.json minio/downloads'
    echo "downloads is public-read (GetObject only) in '$NS'."
    echo "Next: just filestash publish --source <collaborator>/<path> --dataset <name>"

# Remove every anonymous permission from downloads.
# Usage: just filestash unpublish-bucket [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
unpublish-bucket stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fs_minio_root 'mc anonymous set none minio/downloads'
    echo "downloads is private in '$NS'. Direct file URLs now return 403."

# Deploy the filestash stack: preflight, stack select/init, pulumi up, and a
# rollout wait.
# Usage: just filestash deploy [--stack <name>] [--namespace <ns>] [--timeout <d>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[arg("timeout", long="timeout", short="t", help="Rollout wait (default 300s)")]
[group('filestash')]
[no-cd]
deploy stack="" namespace="" timeout="300s":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    just filestash check --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}"
    go run ./cmd/cluster-ops stack ensure --folder filestash --stack "$STACK" --create
    go run ./cmd/cluster-ops stack up --folder filestash --stack "$STACK"
    kubectl -n "$NS" rollout status deployment/filestash --timeout={{ quote(timeout) }}
    echo "Filestash deployed in '$NS'."
    echo "Next: just filestash admin"

# Open the admin console through a port-forward. The internet cannot reach
# /admin: the Gateway route and the admin-block Ingress redirect it.
# Usage: just filestash admin [--stack <name>] [--port <local-port>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[arg("port", long="port", short="p", help="Local port (default 8334)")]
[group('filestash')]
[no-cd]
admin stack="" namespace="" port="8334":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    PORT={{ quote(port) }}
    [[ "$PORT" =~ ^[0-9]+$ ]] || fs_die "--port must be a number."
    kubectl -n "$NS" port-forward svc/filestash "$PORT:8334" &
    pf=$!
    trap 'kill "$pf" 2>/dev/null || true' EXIT
    echo "Admin console: http://localhost:$PORT/admin  (file manager: http://localhost:$PORT/)"
    echo "Press Ctrl-C to close the port-forward."
    wait "$pf"

# Read-only health checks. Exits non-zero on the first failed check group.
# Usage: just filestash verify [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
verify stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    failures=0
    ok() { echo "  ok    $*"; }
    bad() { echo "  FAIL  $*"; failures=$((failures + 1)); }

    kubectl -n "$NS" rollout status deployment/filestash --timeout=60s >/dev/null 2>&1 \
        && ok "deployment/filestash ready" || bad "deployment/filestash not ready"
    phase=$(kubectl -n "$NS" get pvc filestash-state -o jsonpath='{.status.phase}' 2>/dev/null || true)
    [[ "$phase" == "Bound" ]] && ok "pvc/filestash-state Bound" || bad "pvc/filestash-state phase '$phase'"
    used=$(kubectl -n "$NS" exec deployment/filestash -c filestash -- df -P /app/data/state 2>/dev/null \
        | awk 'NR==2 {gsub("%","",$5); print $5}' || true)
    if [[ "$used" =~ ^[0-9]+$ && "$used" -le 80 ]]; then
        ok "state PVC use ${used}%"
    else
        bad "state PVC use '${used}%' (limit 80%) — grow it: just filestash resize-state --size <q>"
    fi

    expected=$( { yq -r '.plugins[].name, .office.name' filestash/plugins/plugins.lock.yaml; echo plg_search_stateless; echo plg_image_c; } | tr '\n' ' ')
    export FS_EXPECTED="$expected"
    if fs_filestash_run '
        curl -fsS "$FS_URL/healthz" >/dev/null || { echo "healthz failed"; exit 1; }
        about=$(curl -fsS "$FS_URL/about")
        for p in $FS_EXPECTED; do
            [[ "$about" == *"$p"* ]] || { echo "plugin $p missing from /about"; exit 1; }
        done
        code=$(curl -s -o /dev/null -w "%{http_code}" "$FS_URL/admin/tty/")
        [[ "$code" == "404" ]] || { echo "/admin/tty/ returned $code (browser shell must be off)"; exit 1; }
    '; then
        ok "/healthz, plugins on /about, /admin/tty/ off"
    else
        bad "filestash HTTP checks"
    fi

    if fs_minio_root '
        want=$(jq -S . filestash/policies/downloads-anonymous.json)
        got=$(mc anonymous get-json minio/downloads | jq -S .)
        [[ "$got" == "$want" ]] || { echo "downloads anonymous policy differs from the policy file"; exit 1; }
        mc anonymous get minio/uploads | grep -q "private" || { echo "uploads is not private"; exit 1; }
    '; then
        ok "downloads public-read policy, uploads private"
    else
        bad "bucket policies"
    fi

    max=$(fs_prop backup.maxAgeHours)
    if [[ ! "$max" =~ ^[1-9][0-9]*$ ]]; then
        bad "backup.maxAgeHours is unset in $CFG (recovery point target)"
    else
        # shellcheck disable=SC2016
        age=$(kubectl -n "$NS" exec deployment/filestash -c filestash -- sh -c '
            n=$(ls -1 /app/data/state/backups 2>/dev/null | grep -E "^[0-9]{8}T[0-9]{6}Z$" | sort | tail -n 1)
            [ -n "$n" ] && [ -f "/app/data/state/backups/$n/manifest.json" ] || exit 2
            t=$(date -u -d "$(echo "$n" | sed -E "s/(....)(..)(..)T(..)(..)(..)Z/\1-\2-\3 \4:\5:\6/")" +%s)
            echo $(( ($(date -u +%s) - t) / 3600 ))' 2>/dev/null || true)
        if [[ "$age" =~ ^[0-9]+$ && "$age" -lt "$max" ]]; then
            ok "newest state copy ${age}h old (limit ${max}h)"
        else
            bad "newest state copy age '${age}h' (limit ${max}h) — run: just filestash backup-now"
        fi
        vage=$(velero -n "$FS_VELERO_NS" backup get -o json 2>/dev/null | jq -r --arg ns "$NS" '
            [(.items // [.])[] | select(.status.phase == "Completed")
              | select((.spec.includedNamespaces // ["*"]) | (index("*") or index($ns)))
              | select(((.spec.excludedNamespaces // []) | index($ns)) | not)
              | .status.completionTimestamp | sub("\\.[0-9]+"; "") | fromdateiso8601]
            | max | if . == null then "none" else ((now - .) / 3600 | floor) end' || echo none)
        if [[ "$vage" =~ ^[0-9]+$ && "$vage" -lt "$max" ]]; then
            ok "newest velero backup of '$NS' ${vage}h old"
        else
            bad "newest velero backup of '$NS' age '${vage}h' (limit ${max}h)"
        fi
    fi
    excl=$(kubectl -n "$NS" get pvc filestash-state -o jsonpath='{.metadata.labels.velero\.io/exclude-from-backup}' 2>/dev/null || true)
    vols=$(kubectl -n "$NS" get pods -l app.kubernetes.io/name=filestash -o jsonpath='{.items[*].metadata.annotations.backup\.velero\.io/backup-volumes-excludes}' 2>/dev/null || true)
    sched=$(kubectl -n "$FS_VELERO_NS" get schedules.velero.io -o json 2>/dev/null \
        | jq -r --arg ns "$NS" '[.items[].spec.template.excludedNamespaces // [] | index($ns)] | any' || echo unknown)
    if [[ "$excl" != "true" && ",${vols}," != *",state,"* && "$sched" == "false" ]]; then
        ok "velero includes pvc/filestash-state"
    else
        bad "velero excludes the state (pvc label '$excl', pod volume excludes '$vols', schedule excludes namespace '$sched')"
    fi

    if [[ "$failures" -gt 0 ]]; then
        echo "filestash verify: $failures check group(s) failed." >&2
        exit 1
    fi
    echo "filestash verify: all checks passed in '$NS'."

# Grow the state PVC. A PVC cannot shrink; a smaller size fails before any
# change. Writes properties.storage.size, then runs deploy.
# Usage: just filestash resize-state --size <quantity> [--stack <name>]
[arg("size", long="size", help="New PVC size, for example 10Gi (required)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
resize-state size="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    SIZE={{ quote(size) }}
    [[ -n "$SIZE" ]] || fs_die "no size — pass --size (for example 10Gi)."
    to_bytes() {
        awk -v q="$1" 'BEGIN {
            n = q; sub(/[A-Za-z]+$/, "", n); u = substr(q, length(n) + 1)
            m["Ki"]=1024; m["Mi"]=1024^2; m["Gi"]=1024^3; m["Ti"]=1024^4
            m["K"]=1e3; m["M"]=1e6; m["G"]=1e9; m["T"]=1e12; m[""]=1
            if (n !~ /^[0-9]+$/ || !(u in m)) { print "x"; exit }
            printf "%.0f\n", n * m[u]
        }'
    }
    new=$(to_bytes "$SIZE")
    [[ "$new" != "x" ]] || fs_die "invalid --size '$SIZE' (use Mi, Gi, or Ti)."
    current=$(kubectl -n "$NS" get pvc filestash-state -o jsonpath='{.spec.resources.requests.storage}')
    cur=$(to_bytes "$current")
    [[ "$cur" != "x" ]] || fs_die "cannot read the current size of pvc/filestash-state."
    if (( new <= cur )); then
        fs_die "--size $SIZE is not larger than the current $current. A PVC cannot shrink."
    fi
    class=$(kubectl -n "$NS" get pvc filestash-state -o jsonpath='{.spec.storageClassName}')
    expand=$(kubectl get storageclass "$class" -o jsonpath='{.allowVolumeExpansion}')
    [[ "$expand" == "true" ]] || fs_die "storage class '$class' does not allow volume expansion."
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}"
    go run ./cmd/cluster-ops stack config set --folder filestash --stack "$STACK" \
        --key properties.storage.size --value "$SIZE"
    just filestash deploy --stack "$STACK"
    echo "pvc/filestash-state: $current -> $SIZE. Commit $CFG."

# Destroy the filestash stack. Destructive: the stack owns the state PVC, so
# destroy deletes config.json, every share link, and the state copies.
# Requires --delete-pvc yes. The buckets and their objects stay in MinIO.
# Usage: just filestash teardown --delete-pvc yes [--stack <name>]
[arg("delete_pvc", long="delete-pvc", help="Must be yes: the destroy deletes the state PVC")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
teardown delete_pvc="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    if [[ {{ quote(delete_pvc) }} != "yes" ]]; then
        fs_die "teardown deletes pvc/filestash-state (all share links). Pass --delete-pvc yes."
    fi
    echo "State files that the destroy deletes:"
    kubectl -n "$NS" exec deployment/filestash -c filestash -- ls -l /app/data/state/db 2>/dev/null || echo "  (pod not running)"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}"
    go run ./cmd/cluster-ops stack destroy --folder filestash --stack "$STACK"
    echo "filestash removed from '$NS'. Buckets uploads and downloads are unchanged."

# Copy approved files from uploads/<source>/ to downloads/<dataset>/ as
# filestash-publisher. Refuses to replace any existing object, sets the
# Content-Type from the file extension, and prints each direct URL.
# One publication at a time: lease/filestash-publish is the lock.
# Usage: just filestash publish --source <collaborator>/<path> --dataset <name> [--url-host <h>]
[arg("source", long="source", help="Prefix in uploads, for example alice/2026-batch (required)")]
[arg("dataset", long="dataset", help="Dataset folder in downloads: a-z 0-9 . _ - (required)")]
[arg("url_host", long="url-host", help="Public MinIO host for the printed URLs (default: from the gateway or minio stack)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
publish source="" dataset="" url_host="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    SOURCE={{ quote(source) }}
    DATASET={{ quote(dataset) }}
    [[ "$DATASET" =~ ^[a-z0-9][a-z0-9._-]*$ ]] || fs_die "invalid --dataset '$DATASET' (a-z 0-9 . _ -, first a letter or digit)."
    SOURCE="${SOURCE#/}"
    SOURCE="${SOURCE%/}"
    [[ -n "$SOURCE" ]] || fs_die "no --source (for example alice/2026-batch)."
    case "/$SOURCE/" in
        */../*|*/./*|*//*) fs_die "invalid --source '$SOURCE': no '.', '..', or empty path segments." ;;
    esac
    HOST={{ quote(url_host) }}
    [[ -n "$HOST" ]] || HOST=$(fs_downloads_host)
    [[ "$HOST" =~ ^[A-Za-z0-9.-]+$ ]] || fs_die "invalid --url-host '$HOST'."
    fs_lock_take
    trap fs_lock_release EXIT
    export FS_SOURCE="$SOURCE/" FS_DATASET="$DATASET" FS_HOST="$HOST"
    fs_minio_as publisher '
        content_type() {
            local ext="${1##*.}"
            ext=$(printf "%s" "$ext" | tr "[:upper:]" "[:lower:]")
            case "$ext" in
                csv) echo text/csv ;;
                tsv) echo text/tab-separated-values ;;
                txt|fa|fasta|gff|gff3) echo text/plain ;;
                json) echo application/json ;;
                gz) echo application/gzip ;;
                zip) echo application/zip ;;
                pdf) echo application/pdf ;;
                png) echo image/png ;;
                jpg|jpeg) echo image/jpeg ;;
                mp4) echo video/mp4 ;;
                *) echo application/octet-stream ;;
            esac
        }
        encode_path() {
            local IFS=/ seg out=""
            for seg in $1; do out+="/$(jq -rn --arg s "$seg" "\$s|@uri")"; done
            printf "%s" "${out#/}"
        }
        keys=()
        while IFS= read -r key; do
            [[ -n "$key" ]] && keys+=("$key")
        done < <(mc ls --recursive --json "minio/uploads/$FS_SOURCE" | jq -r "select(.type == \"file\") | .key")
        [[ ${#keys[@]} -gt 0 ]] || { echo "Error: no files under uploads/$FS_SOURCE" >&2; exit 1; }
        existing=()
        for key in "${keys[@]}"; do
            if mc stat "minio/downloads/$FS_DATASET/$key" >/dev/null 2>&1; then
                existing+=("downloads/$FS_DATASET/$key")
            fi
        done
        if [[ ${#existing[@]} -gt 0 ]]; then
            echo "Error: these objects exist already; publish never replaces a file:" >&2
            printf "  %s\n" "${existing[@]}" >&2
            exit 1
        fi
        for key in "${keys[@]}"; do
            mc cp --attr "Content-Type=$(content_type "$key")" \
                "minio/uploads/$FS_SOURCE$key" "minio/downloads/$FS_DATASET/$key" >/dev/null
            echo "https://$FS_HOST/downloads/$(encode_path "$FS_DATASET/$key")"
        done
    '
    echo "Published uploads/$SOURCE/ to downloads/$DATASET/."

# Delete one published file. Its direct URL then returns 404.
# Usage: just filestash unpublish-file --key <dataset>/<path> --confirm yes
[arg("key", long="key", help="Object key in downloads, for example strains-2026/table.csv (required)")]
[arg("confirm", long="confirm", help="Must be yes")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
unpublish-file key="" confirm="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    KEY={{ quote(key) }}
    KEY="${KEY#/}"
    [[ -n "$KEY" && "$KEY" == */* ]] || fs_die "pass --key <dataset>/<path>."
    case "/$KEY/" in
        */../*|*/./*|*//*) fs_die "invalid --key '$KEY'." ;;
    esac
    [[ {{ quote(confirm) }} == "yes" ]] || fs_die "deleting downloads/$KEY breaks its public URL. Pass --confirm yes."
    fs_lock_take
    trap fs_lock_release EXIT
    export FS_KEY="$KEY"
    fs_minio_as publisher 'mc rm "minio/downloads/$FS_KEY"'
    echo "Deleted downloads/$KEY. Its direct URL now returns 404."

# Remove lease/filestash-publish after a crashed publish. Deletes it only if
# its holder equals --holder (the value that publish printed).
# Usage: just filestash release-lock --holder <id>
[arg("holder", long="holder", help="Holder identity printed by the failed publish (required)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
release-lock holder="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    WANT={{ quote(holder) }}
    [[ -n "$WANT" ]] || fs_die "pass --holder <id>."
    got=$(kubectl -n "$NS" get lease filestash-publish -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
    [[ -n "$got" ]] || fs_die "lease/filestash-publish does not exist in '$NS'."
    [[ "$got" == "$WANT" ]] || fs_die "lease holder is '$got', not '$WANT'. Nothing deleted."
    kubectl -n "$NS" delete lease filestash-publish
    echo "Released lease/filestash-publish (holder '$got')."

# Make a state copy now, then a Velero backup of the Filestash resources
# (PVC, Secrets, ConfigMap, Deployment). Filestash stops for the copy.
# Usage: just filestash backup-now [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
backup-now stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    ts=$(date -u +%Y%m%d%H%M%S)
    job="filestash-state-backup-now-$ts"
    kubectl -n "$NS" create job --from=cronjob/filestash-state-backup "$job"
    if ! kubectl -n "$NS" wait --for=condition=complete "job/$job" --timeout=600s; then
        kubectl -n "$NS" logs "job/$job" --all-containers || true
        fs_die "state copy job $job failed."
    fi
    backup="filestash-$ts"
    velero -n "$FS_VELERO_NS" backup create "$backup" --include-namespaces "$NS" \
        --selector app.kubernetes.io/name=filestash --wait
    phase=$(velero -n "$FS_VELERO_NS" backup get "$backup" -o json | jq -r '.status.phase')
    [[ "$phase" == "Completed" ]] || fs_die "velero backup $backup phase '$phase'."
    echo "State copy and velero backup $backup completed."
    echo "Next: just filestash restore-state --backup $backup --target-namespace filestash-restore-test"

# Restore test: restore a Velero backup into an isolated namespace with no
# egress, replace the state with the newest completed copy, and start one
# Filestash pod with no Service and no route. With --in-place yes: disaster
# recovery on the live PVC from its own newest completed copy (destructive).
# Usage: just filestash restore-state --backup <name> --target-namespace <ns>
#        just filestash restore-state --in-place yes
[arg("backup", long="backup", help="Velero backup name (test restore)")]
[arg("target_namespace", long="target-namespace", help="New namespace for the test restore; must not exist")]
[arg("in_place", long="in-place", help="yes: replace the live state from its newest completed copy")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
restore-state backup="" target_namespace="" in_place="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    BACKUP={{ quote(backup) }}
    TARGET={{ quote(target_namespace) }}
    if [[ {{ quote(in_place) }} == "yes" ]]; then
        [[ -z "$BACKUP" && -z "$TARGET" ]] || fs_die "--in-place yes takes no --backup or --target-namespace."
        kubectl -n "$NS" get deployment filestash >/dev/null || fs_die "deployment/filestash not found in '$NS'."
        echo "Replacing the live state in '$NS' from its newest completed copy."
        trap 'kubectl -n "$NS" scale deployment/filestash --replicas=1 >/dev/null' EXIT
        kubectl -n "$NS" scale deployment/filestash --replicas=0
        fs_wait_no_pods "$NS"
        fs_run_restore_job "$NS"
        echo "Live state restored in '$NS'. Next: just filestash verify"
        exit 0
    fi

    # Preflight: everything below runs before the first change.
    [[ -n "$BACKUP" ]] || fs_die "pass --backup <velero-backup> (or --in-place yes)."
    [[ -n "$TARGET" ]] || fs_die "pass --target-namespace <new-namespace>."
    [[ "$TARGET" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || fs_die "invalid --target-namespace '$TARGET'."
    [[ "$TARGET" != "$NS" ]] || fs_die "--target-namespace is the live namespace '$NS'. Use --in-place yes for disaster recovery."
    if kubectl get namespace "$TARGET" >/dev/null 2>&1; then
        fs_die "namespace '$TARGET' exists. The test restore needs a new namespace."
    fi
    phase=$(velero -n "$FS_VELERO_NS" backup get "$BACKUP" -o json | jq -r '.status.phase')
    [[ "$phase" == "Completed" ]] || fs_die "velero backup $BACKUP phase '$phase' (want Completed)."
    details=$(velero -n "$FS_VELERO_NS" backup describe "$BACKUP" --details)
    [[ "$details" == *"$NS/filestash-state"* ]] || fs_die "backup $BACKUP has no pvc $NS/filestash-state."
    grep -Eq "^[[:space:]]+- $NS/filestash$" <<<"$details" || fs_die "backup $BACKUP has no Secret $NS/filestash (CONFIG_SECRET)."
    live=$(kubectl -n "$NS" get deployment filestash -o json)

    kubectl create namespace "$TARGET"
    kubectl label namespace "$TARGET" filestash-restore-test=true
    fs_render filestash/manifests/restore-deny-egress.yaml "$TARGET" | kubectl apply -f -
    velero -n "$FS_VELERO_NS" restore create "filestash-restore-$(date -u +%Y%m%d%H%M%S)" \
        --from-backup "$BACKUP" --namespace-mappings "$NS:$TARGET" \
        --include-resources persistentvolumeclaims,persistentvolumes,secrets,configmaps --wait
    fs_run_restore_job "$TARGET"
    jq --arg t "$TARGET" 'del(.status, .metadata.uid, .metadata.resourceVersion, .metadata.creationTimestamp,
        .metadata.managedFields, .metadata.annotations, .metadata.generation) | .metadata.namespace = $t' \
        <<<"$live" | kubectl apply -f -
    kubectl -n "$TARGET" rollout status deployment/filestash --timeout=300s
    kubectl -n "$TARGET" exec deployment/filestash -c filestash -- curl -fsS http://localhost:8334/healthz >/dev/null \
        || fs_die "restored Filestash in '$TARGET' fails /healthz."
    echo "Restore test passed in '$TARGET'."
    echo "Next: just filestash restore-cleanup --target-namespace $TARGET"

# Delete a restore test namespace. Refuses a namespace without the
# filestash-restore-test=true label that restore-state sets.
# Usage: just filestash restore-cleanup --target-namespace <ns>
[arg("target_namespace", long="target-namespace", help="Restore test namespace to delete (required)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
restore-cleanup target_namespace="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    TARGET={{ quote(target_namespace) }}
    [[ -n "$TARGET" && "$TARGET" != "$NS" ]] || fs_die "pass --target-namespace (not the live namespace '$NS')."
    label=$(kubectl get namespace "$TARGET" -o jsonpath='{.metadata.labels.filestash-restore-test}')
    [[ "$label" == "true" ]] || fs_die "namespace '$TARGET' is not a restore test namespace."
    kubectl delete namespace "$TARGET"
    echo "Deleted restore test namespace '$TARGET'."

# Upgrade the Filestash image: state copy and Velero backup first, then save
# the current image as previousImage, pin the new tag and digest, deploy, and
# run verify.
# Usage: just filestash upgrade --tag <t> --digest sha256:<d> [--stack <name>]
[arg("tag", long="tag", short="t", help="New image tag (required)")]
[arg("digest", long="digest", short="d", help="New image digest sha256:<64 hex> (required)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
upgrade tag="" digest="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    TAG={{ quote(tag) }}
    DIGEST={{ quote(digest) }}
    [[ "$TAG" =~ ^[A-Za-z0-9._-]+$ ]] || fs_die "pass --tag."
    [[ "$DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]] || fs_die "pass --digest sha256:<64 hex>."
    cur_name=$(fs_prop image.name); cur_tag=$(fs_prop image.tag); cur_digest=$(fs_prop image.digest)
    [[ "$cur_digest" != "$DIGEST" ]] || fs_die "the stack already pins $DIGEST."
    just filestash backup-now --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}"
    set_cfg() { go run ./cmd/cluster-ops stack config set --folder filestash --stack "$STACK" --key "$1" --value "$2"; }
    set_cfg properties.previousImage.name "${cur_name:-machines/filestash}"
    set_cfg properties.previousImage.tag "$cur_tag"
    set_cfg properties.previousImage.digest "$cur_digest"
    set_cfg properties.image.tag "$TAG"
    set_cfg properties.image.digest "$DIGEST"
    just filestash deploy --stack "$STACK"
    just filestash verify --stack "$STACK"
    echo "Upgraded to $TAG@$DIGEST. Commit $CFG."
    echo "Next: just filestash probe-admin --host <filestash-host>"

# Pin previousImage again and deploy. If the new release changed the state
# format, restore the copy that upgrade made: just filestash restore-state --in-place yes
# Usage: just filestash rollback [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
rollback stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    prev_tag=$(fs_prop previousImage.tag); prev_digest=$(fs_prop previousImage.digest)
    [[ -n "$prev_tag" && "$prev_digest" =~ ^sha256:[0-9a-f]{64}$ ]] \
        || fs_die "no previousImage in $CFG — rollback works only after an upgrade."
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}"
    go run ./cmd/cluster-ops stack config set --folder filestash --stack "$STACK" --key properties.image.tag --value "$prev_tag"
    go run ./cmd/cluster-ops stack config set --folder filestash --stack "$STACK" --key properties.image.digest --value "$prev_digest"
    just filestash deploy --stack "$STACK"
    echo "Rolled back to $prev_tag@$prev_digest. Commit $CFG."
    echo "If the state format changed: just filestash restore-state --in-place yes"

# Delete uploads older than --older-than days, as filestash-operator. A dry
# run (list only) unless --confirm yes. There is no default age: the
# retention period is a decision for the project.
# Usage: just filestash purge-uploads --older-than <days> [--confirm yes]
[arg("older_than", long="older-than", help="Age in days (required)")]
[arg("confirm", long="confirm", help="yes: delete; any other value lists only")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
purge-uploads older_than="" confirm="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    DAYS={{ quote(older_than) }}
    [[ "$DAYS" =~ ^[1-9][0-9]*$ ]] || fs_die "pass --older-than <days> (a positive number)."
    export FS_DAYS="$DAYS"
    if [[ {{ quote(confirm) }} == "yes" ]]; then
        fs_minio_as operator 'mc rm --recursive --force --older-than "${FS_DAYS}d" minio/uploads'
        echo "Deleted uploads older than $DAYS days in '$NS'."
    else
        fs_minio_as operator 'mc find minio/uploads --older-than "${FS_DAYS}d"'
        echo "Dry run: the list above is what --confirm yes deletes."
    fi

# List every share link from the newest completed state copy and fail on a
# rule break: an upload link with no expiry, a writable link, or a link path
# outside /uploads/ and /downloads/. Needs a local sqlite3.
# Usage: just filestash audit-shares [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
audit-shares stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    command -v sqlite3 >/dev/null 2>&1 || fs_die "sqlite3 not found."
    db="$(mktemp "${TMPDIR:-/tmp}/filestash-share.XXXXXX")"
    trap 'rm -f "$db"' EXIT
    # shellcheck disable=SC2016
    kubectl -n "$NS" exec deployment/filestash -c filestash -- sh -c '
        n=$(ls -1 /app/data/state/backups 2>/dev/null | grep -E "^[0-9]{8}T[0-9]{6}Z$" | sort | tail -n 1)
        [ -n "$n" ] || { echo "no completed state copy" >&2; exit 1; }
        cat "/app/data/state/backups/$n/db/share.sql"' > "$db" || fs_die "no completed state copy — run: just filestash backup-now"
    rows=$(sqlite3 -separator $'\t' "$db" "SELECT id, related_path, params FROM Share ORDER BY related_path")
    problems=0
    printf '%-24s %-40s %-8s %-8s %-8s %s\n' ID PATH READ UPLOAD WRITE "USERS / EXPIRE"
    while IFS=$'\t' read -r id path params; do
        [[ -n "$id" ]] || continue
        read -r r u w users expire < <(jq -r '[.can_read, .can_upload, .can_write, (.users // "-"), (.expire // "none")] | map(tostring) | join(" ")' <<<"$params")
        printf '%-24s %-40s %-8s %-8s %-8s %s %s\n' "$id" "$path" "$r" "$u" "$w" "$users" "$expire"
        [[ "$u" == "true" && "$expire" == "none" ]] && { echo "  RULE: upload link $id has no expiry"; problems=$((problems + 1)); }
        [[ "$w" == "true" ]] && { echo "  RULE: link $id can write"; problems=$((problems + 1)); }
        [[ "$path" == /uploads/* || "$path" == /downloads/* ]] || { echo "  RULE: link $id path $path is outside /uploads/ and /downloads/"; problems=$((problems + 1)); }
    done <<<"$rows"
    [[ "$problems" -eq 0 ]] || fs_die "$problems share rule break(s)."
    echo "All share links follow the rules."

# Check direct file URLs from outside the cluster: anonymous read works, with
# Range; anonymous list, write, and delete fail; uploads is closed.
# Usage: just filestash probe-public --host <minio-host> --key <dataset>/<file>
[arg("host", long="host", help="Public MinIO host, for example storage.dictycr.org (required)")]
[arg("key", long="key", help="A published key in downloads, for example strains-2026/table.csv (required)")]
[group('filestash')]
[no-cd]
probe-public host="" key="":
    #!/usr/bin/env bash
    set -euo pipefail
    HOST={{ quote(host) }}
    KEY={{ quote(key) }}
    [[ "$HOST" =~ ^[A-Za-z0-9.-]+$ ]] || { echo "Error: pass --host." >&2; exit 1; }
    [[ -n "$KEY" ]] || { echo "Error: pass --key <dataset>/<file>." >&2; exit 1; }
    base="https://$HOST"
    failures=0
    expect() {
        local want="$1" label="$2" got
        shift 2
        got=$(curl -s -o /dev/null -w '%{http_code}' "$@" || echo 000)
        if [[ "$got" == "$want" ]]; then echo "  ok    $label ($got)"; else echo "  FAIL  $label: got $got, want $want"; failures=$((failures + 1)); fi
    }
    expect 200 "anonymous GET downloads/$KEY" "$base/downloads/$KEY"
    expect 206 "anonymous Range GET" -H 'Range: bytes=0-9' "$base/downloads/$KEY"
    expect 403 "anonymous list of downloads" "$base/downloads/"
    expect 403 "anonymous list of uploads" "$base/uploads/"
    expect 403 "anonymous GET in uploads" "$base/uploads/probe-$$"
    expect 403 "anonymous PUT in downloads" -X PUT --data probe "$base/downloads/probe-$$"
    expect 403 "anonymous DELETE in downloads" -X DELETE "$base/downloads/$KEY"
    [[ "$failures" -eq 0 ]] || { echo "probe-public: $failures check(s) failed." >&2; exit 1; }
    echo "probe-public: all checks passed for $HOST."

# Check from outside the cluster that no admin path variant reaches the admin
# console or the admin API. Uses --path-as-is so curl sends each variant
# unchanged. A failure means: turn the route off again.
# Usage: just filestash probe-admin --host <filestash-host>
[arg("host", long="host", help="Public Filestash host (required)")]
[group('filestash')]
[no-cd]
probe-admin host="":
    #!/usr/bin/env bash
    set -euo pipefail
    HOST={{ quote(host) }}
    [[ "$HOST" =~ ^[A-Za-z0-9.-]+$ ]] || { echo "Error: pass --host." >&2; exit 1; }
    body="$(mktemp "${TMPDIR:-/tmp}/probe-admin.XXXXXX")"
    trap 'rm -f "$body"' EXIT
    failures=0
    for path in /admin /admin/ /Admin //admin /./admin /x/../admin /%61dmin /admin%2Fapi/config \
                /admin/api/session /admin/api/config /admin/api/logs /admin/tty/; do
        for method in GET POST PUT DELETE OPTIONS; do
            code=$(curl -s --path-as-is -X "$method" -o "$body" -w '%{http_code}' "https://$HOST$path" || echo 000)
            if grep -q 'backoffice' "$body" || { [[ "$code" == 2* ]] && grep -q '"status"' "$body"; }; then
                echo "  FAIL  $method $path ($code) reached admin content"
                failures=$((failures + 1))
            fi
        done
    done
    [[ "$failures" -eq 0 ]] || { echo "probe-admin: $failures request(s) reached admin content. Disable the route." >&2; exit 1; }
    echo "probe-admin: no admin path variant reached admin content on $HOST."
