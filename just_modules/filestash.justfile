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
