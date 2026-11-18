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

# Read-only preflight: the namespace, the encrypted stack values for Secrets
# filestash and filestash-users, Secret minio-root, and Service minio.
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
    for key in secrets.adminPasswordHash secrets.configSecret; do
        fs_cfg_has filestash "$key" || fs_die "$CFG has no properties.$key — run: just filestash create-secret"
    done
    for role in intake public operator publisher; do
        fs_cfg_has filestash "users.$role.secretKey" || fs_die "$CFG has no keys for $role — run: just filestash create-users"
    done
    fs_secret_exists minio-root || fs_die "Secret minio-root not found in '$NS'."
    kubectl -n "$NS" get service minio >/dev/null 2>&1 || fs_die "service/minio not found in '$NS'."
    echo "filestash prerequisites ready for stack '$STACK' in namespace '$NS'."
    echo "Next: just filestash deploy"

# Store the admin password hash and CONFIG_SECRET as KMS-encrypted values in
# filestash/Pulumi.<stack>.yaml. `deploy` makes Secret filestash from them.
# A second run replaces only the admin password hash: a new CONFIG_SECRET
# breaks every encrypted value in config.json.
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
    fs_stack_init filestash
    if fs_cfg_has filestash secrets.configSecret; then
        echo "CONFIG_SECRET exists in $CFG: keeping it."
    elif kubectl -n "$NS" get pvc filestash-state >/dev/null 2>&1; then
        fs_die "$CFG has no secrets.configSecret but pvc/filestash-state exists. A new CONFIG_SECRET breaks the encrypted config.json. Restore $CFG from Git."
    else
        fs_random 32 | fs_cfg_secret filestash secrets.configSecret
        echo "CONFIG_SECRET created in $CFG (encrypted)."
    fi
    read -rsp "Filestash admin password (input hidden): " pw
    echo
    [[ ${#pw} -ge 12 ]] || fs_die "the admin password needs at least 12 characters."
    hash=$(printf '%s' "$pw" | htpasswd -niBC 10 "" | tr -d ':\n')
    unset pw
    [[ "$hash" == \$2* ]] || fs_die "htpasswd did not return a bcrypt hash."
    printf '%s' "$hash" | fs_cfg_secret filestash secrets.adminPasswordHash
    echo "Admin password hash stored in $CFG (encrypted). Commit $CFG."
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

# Store a key pair for each of the four MinIO users as KMS-encrypted values
# in filestash/Pulumi.<stack>.yaml, then create the users and their policies
# (filestash/policies/*.json) in MinIO. `deploy` makes Secret filestash-users.
# A second run reuses the stored keys: a new key stops every share link that
# staff made with the old one.
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
    fs_stack_init filestash
    existing=$(fs_minio_root 'mc admin user list minio --json | jq -r ".accessKey // empty"') \
        || fs_die "could not list the MinIO users."
    for role in "${roles[@]}"; do
        if fs_cfg_has filestash "users.$role.secretKey"; then
            echo "  $role: reusing the stored keys"
            continue
        fi
        if grep -qx "filestash-$role" <<<"$existing"; then
            fs_die "$CFG has no keys for $role but MinIO user filestash-$role exists. New keys stop every share link. Restore $CFG from Git."
        fi
        fs_cfg_plain filestash "users.$role.accessKey" "filestash-$role"
        fs_random 40 | fs_cfg_secret filestash "users.$role.secretKey"
        echo "  $role: keys created in $CFG (encrypted)"
    done
    for role in "${roles[@]}"; do
        ak=$(yq -r ".config.\"filestash:properties\".users.$role.accessKey // \"\"" "$CFG")
        sk=$(fs_cfg_get filestash "users.$role.secretKey")
        [[ -n "$ak" && -n "$sk" ]] || fs_die "could not read the $role keys from $CFG."
        export "FS_AK_$role=$ak" "FS_SK_$role=$sk"
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
    echo "MinIO users filestash-intake, -public, -operator, and -publisher ready in '$NS'. Commit $CFG."
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

# Read-only health checks. Exits non-zero when any check group fails.
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
        # The CronJob status counts only its own runs. A backup-now Job has the
        # same component label, so take the newest success of both.
        cron=$(kubectl -n "$NS" get cronjob filestash-state-backup -o jsonpath='{.status.lastSuccessfulTime}' 2>/dev/null || true)
        jobs=$(kubectl -n "$NS" get jobs -l app.kubernetes.io/component=state-backup -o json 2>/dev/null \
            | jq -r '[.items[] | select((.status.succeeded // 0) > 0) | .status.completionTime // empty] | max // empty' || true)
        last=$(printf '%s\n%s\n' "$cron" "$jobs" | sort | tail -n 1)
        age=none
        [[ -n "$last" ]] && age=$(jq -rn --arg t "$last" '(now - ($t | fromdateiso8601)) / 3600 | floor')
        if [[ "$age" =~ ^[0-9]+$ && "$age" -lt "$max" ]]; then
            ok "newest restic snapshot ${age}h old (limit ${max}h)"
        else
            bad "newest successful state backup age '${age}h' (limit ${max}h) — run: just filestash backup-now"
        fi
    fi
    fs_secret_exists filestash-backup-auth && ok "Secret filestash-backup-auth exists" \
        || bad "Secret filestash-backup-auth missing — run: just filestash deploy-backup"
    excl=$(kubectl -n "$NS" get pvc filestash-state -o jsonpath='{.metadata.labels.velero\.io/exclude-from-backup}' 2>/dev/null || true)
    [[ "$excl" == "true" ]] && ok "Velero skips pvc/filestash-state" \
        || bad "pvc/filestash-state has no velero.io/exclude-from-backup=true label — run: just filestash deploy"

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
            local seg out="" segs=()
            IFS=/ read -ra segs <<<"$1"
            for seg in "${segs[@]}"; do out+="/$(jq -rn --arg s "$seg" "\$s|@uri")"; done
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

# Remove a lease after a crashed run. Deletes it only if its holder equals
# --holder (the value that the failed run printed).
# Usage: just filestash release-lock --holder <id> [--lease filestash-maintenance]
[arg("holder", long="holder", help="Holder identity printed by the failed run (required)")]
[arg("lease", long="lease", help="filestash-publish (default) or filestash-maintenance")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
release-lock holder="" lease="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    WANT={{ quote(holder) }}
    LEASE={{ quote(lease) }}
    [[ -n "$LEASE" ]] || LEASE=filestash-publish
    [[ "$LEASE" == filestash-publish || "$LEASE" == filestash-maintenance ]] || fs_die "invalid --lease '$LEASE'."
    [[ -n "$WANT" ]] || fs_die "pass --holder <id>."
    got=$(kubectl -n "$NS" get lease "$LEASE" -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)
    [[ -n "$got" ]] || fs_die "lease/$LEASE does not exist in '$NS'."
    [[ "$got" == "$WANT" ]] || fs_die "lease holder is '$got', not '$WANT'. Nothing deleted."
    kubectl -n "$NS" delete lease "$LEASE"
    echo "Released lease/$LEASE (holder '$got')."

# Configure the filestash-backup stack: the backup writer service account and
# its key, a random restic password (kept on a second run), and the GCS
# project, all as KMS-encrypted values in filestash-backup/Pulumi.<stack>.yaml.
# Every gcloud call runs as sa-manager from its key file: kops-cluster-creator
# cannot create service account keys.
# Usage: just filestash configure-backup [--project <id>] [--key-file <path>] [--manager-key <path>] [--stack <name>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID)")]
[arg("manager_key", long="manager-key", help="sa-manager JSON key (default credentials/<project>/sa-manager.json)")]
[arg("key_file", long="key-file", short="f", help="Service account key path (default credentials/<project>/filestash-backup-sa.json)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
configure-backup project="" key_file="" manager_key="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fs_backup_resolve
    PROJECT={{ quote(project) }}
    [[ -n "$PROJECT" ]] || PROJECT="${PROJECT_ID:-}"
    [[ -n "$PROJECT" ]] || fs_die "no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --project."
    BUCKET=$(fs_bprop bucket)
    [[ "$BUCKET" == "restic-filestash-backup-$PROJECT" ]] \
        || fs_die "bucket '$BUCKET' in $BCFG does not match project '$PROJECT'."
    KEY_FILE={{ quote(key_file) }}
    [[ -n "$KEY_FILE" ]] || KEY_FILE="credentials/${PROJECT}/filestash-backup-sa.json"
    MANAGER_KEY={{ quote(manager_key) }}
    [[ -n "$MANAGER_KEY" ]] || MANAGER_KEY="credentials/${PROJECT}/sa-manager.json"
    just filestash _setup-backup-sa --bucket "$BUCKET" --project "$PROJECT" --key-file "$KEY_FILE" --manager-key "$MANAGER_KEY"
    [[ -f "$KEY_FILE" ]] || fs_die "service account key '$KEY_FILE' does not exist."
    KEY_FILE="$(cd "$(dirname "$KEY_FILE")" && pwd)/$(basename "$KEY_FILE")"
    fs_stack_init filestash-backup
    if fs_cfg_has filestash-backup secret.resticPass; then
        echo "restic password exists in $BCFG: keeping it (a new one locks out every snapshot)."
    else
        fs_random 40 | fs_cfg_secret filestash-backup secret.resticPass
    fi
    printf '%s' "$PROJECT" | fs_cfg_secret filestash-backup secret.gcsProject
    printf '%s' "$KEY_FILE" | fs_cfg_secret filestash-backup secret.serviceAccountFile
    echo "filestash-backup configured. Commit $BCFG."
    echo "Next: just filestash deploy-backup"

[private]
[arg("bucket", long="bucket", short="b", help="Backup GCS bucket the key gets write access to")]
[arg("project", long="project", short="p", help="GCP project id")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key")]
[arg("manager_key", long="manager-key", help="sa-manager JSON key that runs every gcloud call")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name")]
[no-cd]
_setup-backup-sa bucket project key_file manager_key sa_name="filestash-backup-sa":
    #!/usr/bin/env bash
    set -euo pipefail
    BUCKET={{ quote(bucket) }}
    PROJECT={{ quote(project) }}
    KEY_FILE={{ quote(key_file) }}
    MANAGER_KEY={{ quote(manager_key) }}
    SA_NAME={{ quote(sa_name) }}
    SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
    # Bind the identity explicitly: every gcloud call and the key mint use the
    # sa-manager key, never the ambient gcloud account.
    [[ -f "$MANAGER_KEY" ]] || { echo "Error: sa-manager key '$MANAGER_KEY' not found." >&2; exit 1; }
    WHO=$(jq -r '.client_email // empty' "$MANAGER_KEY")
    if [[ "$WHO" != "sa-manager@${PROJECT}.iam.gserviceaccount.com" ]]; then
        echo "Error: '$MANAGER_KEY' is '${WHO:-not a service account key}', not sa-manager@${PROJECT}." >&2
        exit 1
    fi
    export CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="$MANAGER_KEY"
    export GOOGLE_APPLICATION_CREDENTIALS="$MANAGER_KEY"
    err="$(mktemp "${TMPDIR:-/tmp}/filestash-sa.XXXXXX")"
    trap 'rm -f "$err"' EXIT
    if gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT" >/dev/null 2>"$err"; then
        echo "Service account $SA_EMAIL exists — continuing."
    elif grep -Eq 'NOT_FOUND|does not exist' "$err"; then
        gcloud iam service-accounts create "$SA_NAME" --project "$PROJECT" \
            --display-name "Filestash state backup GCS writer"
    else
        echo "Error: could not describe $SA_EMAIL:" >&2
        cat "$err" >&2
        exit 1
    fi
    gcloud projects add-iam-policy-binding "$PROJECT" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.objectAdmin \
        --condition="expression=resource.name.startsWith(\"projects/_/buckets/${BUCKET}\"),title=filestash-backup-bucket-writer,description=Object admin limited to the Filestash restic backup bucket" \
        >/dev/null
    if [[ -f "$KEY_FILE" ]]; then
        echo "Key file $KEY_FILE exists — reusing it."
    else
        go run ./cmd/cluster-ops sa create-key --name "$SA_NAME" --project "$PROJECT" --output-file "$KEY_FILE"
        echo "Warning: service account keys accumulate. Audit them with 'gcloud iam service-accounts keys list'."
    fi

# Deploy the filestash-backup stack: the GCS bucket, Secret
# filestash-backup-auth, and the state backup CronJob. Deploy filestash first:
# the CronJob mounts its PVC.
# Usage: just filestash deploy-backup [--stack <name>] [--namespace <ns>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
deploy-backup stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fs_backup_resolve
    for key in secret.resticPass secret.gcsProject secret.serviceAccountFile; do
        fs_cfg_has filestash-backup "$key" || fs_die "$BCFG has no properties.$key — run: just filestash configure-backup"
    done
    kubectl -n "$NS" get pvc filestash-state >/dev/null 2>&1 || fs_die "pvc/filestash-state not found in '$NS' — run: just filestash deploy"
    export GOOGLE_APPLICATION_CREDENTIALS="$(fs_pulumi_creds)"
    go run ./cmd/cluster-ops stack ensure --folder filestash-backup --stack "$STACK" --create
    go run ./cmd/cluster-ops stack up --folder filestash-backup --stack "$STACK"
    echo "filestash-backup deployed in '$NS'."
    echo "Next: just filestash backup-now"

# Run one state backup now: an online copy of the SQLite files and
# config.json, then a restic snapshot to the GCS bucket of the
# filestash-backup stack. Filestash keeps running. Prints the snapshot id.
# Holds lease/filestash-maintenance and suspends the CronJob for the run, so
# no other manual backup, restore, or scheduled run overlaps it.
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
    fs_backup_resolve
    # One manual backup or restore at a time: the same Lease as restore-state.
    fs_lock_take filestash-maintenance
    trap 'fs_resume_backups; fs_lock_release' EXIT
    fs_suspend_backups
    fs_backup_idle || fs_die "a backup job is running in '$NS'. Wait for it, then run again."
    job="filestash-state-backup-now-$(date -u +%Y%m%d%H%M%S)"
    kubectl -n "$NS" create job --from=cronjob/filestash-state-backup "$job"
    if ! kubectl -n "$NS" wait --for=condition=complete "job/$job" --timeout=1800s; then
        kubectl -n "$NS" logs "job/$job" --all-containers || true
        fs_die "state backup job $job failed."
    fi
    snap=$(kubectl -n "$NS" logs "job/$job" -c restic | sed -n 's/^snapshot \([0-9a-f]*\) saved$/\1/p' | tail -n 1)
    [[ -n "$snap" ]] || fs_die "job $job finished, but its log has no restic snapshot id."
    echo "Snapshot $snap saved."
    echo "Restore it with: just filestash restore-state --in-place yes --snapshot $snap"

# Restore the Filestash state from a restic snapshot.
# Test restore (--target-namespace): a new namespace gets copies of the
# Secrets and the seed ConfigMap, a new PVC, the snapshot, and one Filestash
# pod with no Service, no route, and no egress.
# Disaster recovery (--in-place yes): a check Job proves the snapshot first;
# then Filestash stops, the restore Job replaces the live state, and
# Filestash starts again only after the Job succeeds. Destructive.
# Usage: just filestash restore-state --target-namespace <ns> [--snapshot <id>]
#        just filestash restore-state --in-place yes [--snapshot <id>]
[arg("snapshot", long="snapshot", help="restic snapshot id (default: latest)")]
[arg("target_namespace", long="target-namespace", help="New namespace for the test restore; must not exist")]
[arg("in_place", long="in-place", help="yes: replace the live state (destructive)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
restore-state snapshot="" target_namespace="" in_place="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    SNAP={{ quote(snapshot) }}
    [[ -n "$SNAP" ]] || SNAP=latest
    [[ "$SNAP" == latest || "$SNAP" =~ ^[0-9a-f]{8,64}$ ]] || fs_die "invalid --snapshot '$SNAP' (a restic snapshot id, or latest)."
    TARGET={{ quote(target_namespace) }}
    fs_backup_resolve
    fs_secret_exists filestash-backup-auth || fs_die "Secret filestash-backup-auth not found in '$NS' — run: just filestash deploy-backup"

    if [[ {{ quote(in_place) }} == "yes" ]]; then
        [[ -z "$TARGET" ]] || fs_die "--in-place yes takes no --target-namespace."
        kubectl -n "$NS" get deployment filestash >/dev/null || fs_die "deployment/filestash not found in '$NS'."
        fs_lock_take filestash-maintenance
        trap 'fs_resume_backups; fs_lock_release' EXIT
        fs_suspend_backups
        fs_backup_idle || fs_die "a backup job is running in '$NS'. Wait for it, then run again."
        fs_state_job "$NS" check "$SNAP" || fs_die "snapshot '$SNAP' fails its checks. Nothing changed."
        echo "Replacing the live state in '$NS' from snapshot '$SNAP'."
        kubectl -n "$NS" scale deployment/filestash --replicas=0
        fs_wait_no_pods "$NS"
        if ! fs_state_job "$NS" restore "$SNAP"; then
            echo "Error: the restore job failed. Filestash stays at 0 replicas." >&2
            echo "Read the job logs, fix the cause, and run restore-state again with another --snapshot." >&2
            exit 1
        fi
        kubectl -n "$NS" scale deployment/filestash --replicas=1
        echo "Live state restored in '$NS'. Next: just filestash verify"
        exit 0
    fi

    # Preflight: everything below runs before the first change.
    [[ -n "$TARGET" ]] || fs_die "pass --target-namespace <new-namespace> (or --in-place yes)."
    [[ "$TARGET" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || fs_die "invalid --target-namespace '$TARGET'."
    [[ "$TARGET" != "$NS" ]] || fs_die "--target-namespace is the live namespace '$NS'. Use --in-place yes for disaster recovery."
    if kubectl get namespace "$TARGET" >/dev/null 2>&1; then
        fs_die "namespace '$TARGET' exists. The test restore needs a new namespace."
    fi
    for obj in secret/filestash secret/filestash-backup-auth configmap/filestash-seed deployment/filestash pvc/filestash-state; do
        kubectl -n "$NS" get "$obj" >/dev/null 2>&1 || fs_die "$obj not found in '$NS'."
    done
    live=$(kubectl -n "$NS" get deployment filestash -o json)
    size=$(kubectl -n "$NS" get pvc filestash-state -o jsonpath='{.spec.resources.requests.storage}')
    class=$(kubectl -n "$NS" get pvc filestash-state -o jsonpath='{.spec.storageClassName}')

    kubectl create namespace "$TARGET"
    kubectl label namespace "$TARGET" filestash-restore-test=true
    sed -e "s|__NAMESPACE__|$TARGET|g" filestash/manifests/restore-deny-egress.yaml | kubectl apply -f -
    fs_copy_object secret filestash "$TARGET"
    fs_copy_object secret filestash-backup-auth "$TARGET"
    fs_copy_object configmap filestash-seed "$TARGET"
    jq -n --arg ns "$TARGET" --arg size "$size" --arg class "$class" '{ apiVersion: "v1", kind: "PersistentVolumeClaim",
        metadata: { name: "filestash-state", namespace: $ns },
        spec: { accessModes: ["ReadWriteOnce"], storageClassName: $class,
            resources: { requests: { storage: $size } } } }' \
        | kubectl apply -f -
    fs_state_job "$TARGET" restore "$SNAP" || fs_die "restore job failed in '$TARGET'."
    jq --arg t "$TARGET" 'del(.status, .metadata.uid, .metadata.resourceVersion, .metadata.creationTimestamp,
        .metadata.managedFields, .metadata.annotations, .metadata.generation) | .metadata.namespace = $t' \
        <<<"$live" | kubectl apply -f -
    kubectl -n "$TARGET" rollout status deployment/filestash --timeout=300s
    kubectl -n "$TARGET" exec deployment/filestash -c filestash -- curl -fsS http://localhost:8334/healthz >/dev/null \
        || fs_die "restored Filestash in '$TARGET' fails /healthz."
    echo "Restore test passed in '$TARGET' (snapshot '$SNAP')."
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

# Upgrade the Filestash image: a state backup first (note the snapshot id it
# prints), then save
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
# format, restore the snapshot that upgrade made: restore-state --in-place yes --snapshot <id>
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
    echo "If the state format changed: just filestash restore-state --in-place yes --snapshot <id printed by upgrade>"

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

# List every share link from a copy of the live share.sql and fail on a
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
    kubectl -n "$NS" exec deployment/filestash -c filestash -- cat /app/data/state/db/share.sql > "$db" \
        || fs_die "could not read share.sql from the Filestash pod."
    # A copy taken during a write can be torn. Check it before the audit.
    [[ "$(sqlite3 "$db" "PRAGMA quick_check")" == ok ]] || fs_die "the share.sql copy fails PRAGMA quick_check. Run audit-shares again."
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
# Range; anonymous list, write, and delete fail; uploads is closed. The write
# and delete probes use a throwaway key, never the published --key.
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
    expect 403 "anonymous DELETE in downloads" -X DELETE "$base/downloads/probe-$$-absent"
    [[ "$failures" -eq 0 ]] || { echo "probe-public: $failures check(s) failed." >&2; exit 1; }
    echo "probe-public: all checks passed for $HOST."

# Check from outside the cluster that no admin path variant reaches the admin
# console or the admin API, with any status code. Uses --path-as-is so curl sends each variant
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
    # Paths that must get the block response: a 3xx redirect to https://<host>/.
    blocked=(/admin /admin/ /admin/api/session /admin/api/config /admin/api/logs /admin/tty/)
    # Encoded or normalized variants: a 3xx or a 4xx is a pass.
    variants=(/Admin //admin /./admin /x/../admin /%61dmin /%61dmin/api/session /admin%2Fapi/config /admin%2Fapi/session)
    probe() {
        local kind="$1" path="$2" method="$3" res code location
        res=$(curl -s --path-as-is -X "$method" -o "$body" -w '%{http_code} %{redirect_url}' "https://$HOST$path" || echo "000 ")
        code="${res%% *}"; location="${res#* }"
        if [[ "$code" == 000 ]]; then
            echo "  FAIL  $method $path: no response"; failures=$((failures + 1)); return
        fi
        if grep -q 'backoffice' "$body" || grep -q '"status"' "$body"; then
            echo "  FAIL  $method $path ($code) reached admin content"; failures=$((failures + 1)); return
        fi
        if [[ "$kind" == blocked && ! ( "$code" == 3* && "$location" == "https://$HOST/" ) ]]; then
            echo "  FAIL  $method $path ($code) did not get the block redirect"; failures=$((failures + 1)); return
        fi
        if [[ "$kind" == variant && "$code" != 3* && "$code" != 4* && "$path" == *%* ]]; then
            echo "  FAIL  $method $path ($code) encoded admin path not blocked"; failures=$((failures + 1))
        fi
    }
    for method in GET POST PUT DELETE OPTIONS; do
        for path in "${blocked[@]}"; do probe blocked "$path" "$method"; done
        for path in "${variants[@]}"; do probe variant "$path" "$method"; done
    done
    [[ "$failures" -eq 0 ]] || { echo "probe-admin: $failures request(s) reached admin content. Disable the route." >&2; exit 1; }
    echo "probe-admin: no admin path variant reached admin content on $HOST."

# Print the MinIO keys of one staff login role from the encrypted stack
# values. Staff paste them into the Filestash login form. The publisher keys
# are for recipes only and this recipe refuses them.
# Usage: just filestash login-keys --role intake|public|operator
[arg("role", long="role", help="intake, public, or operator (required)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
login-keys role="" stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    ROLE={{ quote(role) }}
    case "$ROLE" in
        intake|public|operator) ;;
        *) fs_die "invalid --role '$ROLE' (intake, public, or operator)." ;;
    esac
    ak=$(yq -r ".config.\"filestash:properties\".users.$ROLE.accessKey // \"\"" "$CFG")
    sk=$(fs_cfg_get filestash "users.$ROLE.secretKey")
    [[ -n "$ak" && -n "$sk" ]] || fs_die "$CFG has no keys for $ROLE — run: just filestash create-users"
    echo "Access key: $ak"
    echo "Secret key: $sk"
    echo "Do not store these keys outside the operator password store."

# List the restic snapshots of the Filestash state, newest last. Needs a local
# restic CLI. The keys come from the encrypted filestash-backup stack values,
# in the environment only.
# Usage: just filestash list-snapshots [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("namespace", long="namespace", short="n", help="Namespace (defaults to properties.namespace of the stack file)")]
[group('filestash')]
[no-cd]
list-snapshots stack="" namespace="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    source scripts/lib/filestash.sh
    fs_resolve {{ quote(stack) }} {{ quote(namespace) }}
    fs_backup_resolve
    command -v restic >/dev/null 2>&1 || fs_die "restic not found — install the restic CLI."
    RESTIC_REPOSITORY="gs:$(fs_bprop bucket):/"
    RESTIC_PASSWORD=$(fs_cfg_get filestash-backup secret.resticPass)
    GOOGLE_PROJECT_ID=$(fs_cfg_get filestash-backup secret.gcsProject)
    key=$(fs_cfg_get filestash-backup secret.serviceAccountFile)
    export RESTIC_REPOSITORY RESTIC_PASSWORD GOOGLE_PROJECT_ID
    GOOGLE_APPLICATION_CREDENTIALS="$key" restic snapshots --tag filestash-backup --host filestash

# List the keys of service account filestash-backup-sa, as sa-manager. Keys
# accumulate when configure-backup makes a new key; delete the unused ones.
# Usage: just filestash backup-keys [--project <id>] [--manager-key <path>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID)")]
[arg("manager_key", long="manager-key", help="sa-manager JSON key (default credentials/<project>/sa-manager.json)")]
[group('filestash')]
[no-cd]
backup-keys project="" manager_key="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    PROJECT={{ quote(project) }}
    [[ -n "$PROJECT" ]] || PROJECT="${PROJECT_ID:-}"
    [[ -n "$PROJECT" ]] || { echo "Error: no GCP project id — pass --project." >&2; exit 1; }
    MANAGER_KEY={{ quote(manager_key) }}
    [[ -n "$MANAGER_KEY" ]] || MANAGER_KEY="credentials/${PROJECT}/sa-manager.json"
    [[ -f "$MANAGER_KEY" ]] || { echo "Error: sa-manager key '$MANAGER_KEY' not found." >&2; exit 1; }
    WHO=$(jq -r '.client_email // empty' "$MANAGER_KEY")
    if [[ "$WHO" != "sa-manager@${PROJECT}.iam.gserviceaccount.com" ]]; then
        echo "Error: '$MANAGER_KEY' is '${WHO:-not a service account key}', not sa-manager@${PROJECT}." >&2
        exit 1
    fi
    CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="$MANAGER_KEY" gcloud iam service-accounts keys list \
        --iam-account "filestash-backup-sa@${PROJECT}.iam.gserviceaccount.com" --project "$PROJECT" --managed-by user
