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
