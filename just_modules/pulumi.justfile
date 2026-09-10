# Bootstrap the complete Pulumi backend: preflight (identity roles + PULUMI_* project
# match), manager SA key (skipped when the existing key authenticates), key-propagation
# wait, KMS keyring/key as sa-manager, GCS state bucket + login, then verify.
# Run once per GCP project, from an activated cluster shell.
# Usage: just gcp-pulumi bootstrap-backend
[group('pulumi-management')]
[no-cd]
bootstrap-backend:
    #!/usr/bin/env bash
    set -euo pipefail
    "{{ justfile_directory() }}/scripts/pulumi/bootstrap-backend.sh"

# Set up Pulumi with a GCS backend.
# This target sets up a Google Cloud Storage (GCS) bucket for Pulumi state management.
# Usage: just gcp-pulumi pulumi-gcs-setup [--sa-json-path <path>] [--gcs-bucket <bucket>] [--lifecycle-config <path>] [--location <zone>]
[arg("location", long="location", short="l", help="GCS bucket location")]
[arg("gcs_bucket", long="gcs-bucket", short="b", help="GCS bucket name (defaults from PULUMI_BACKEND_URL)")]
[arg("sa_json_path", long="sa-json-path", short="j", help="Path to SA JSON (defaults from PULUMI_GCP_CREDENTIALS)")]
[arg("lifecycle_config", long="lifecycle-config", short="c", help="Path to a lifecycle configuration file (optional)")]
[group('pulumi-management')]
pulumi-gcs-setup sa_json_path="" gcs_bucket="" lifecycle_config="" location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    sa_path="{{ sa_json_path }}"
    if [ -z "$sa_path" ]; then
        sa_path="${PULUMI_GCP_CREDENTIALS:-${GOOGLE_APPLICATION_CREDENTIALS:-}}"
    fi
    if [ -z "$sa_path" ]; then
        echo "ERROR: Service account key required — set PULUMI_GCP_CREDENTIALS or pass --sa-json-path."
        exit 1
    fi
    if [ ! -f "$sa_path" ]; then
        echo "ERROR: Service account key file not found: $sa_path"
        exit 1
    fi
    full_sa_json_path=$(realpath "$sa_path")

    export GOOGLE_APPLICATION_CREDENTIALS="$full_sa_json_path"

    project_id=$(jq -r '.project_id' "$full_sa_json_path")

    bucket="{{ gcs_bucket }}"
    if [ -z "$bucket" ] && [ -n "${PULUMI_BACKEND_URL:-}" ]; then
        bucket="${PULUMI_BACKEND_URL#gs://}"
    fi
    if [ -z "$bucket" ]; then
        bucket="pulumi-state-${project_id}"
    fi

    echo "Using project: $project_id"
    echo "Setting up GCS bucket: ${bucket}"
    echo "Location: {{ location }}"

    if ! gcloud storage buckets describe "gs://${bucket}" --project="$project_id" &>/dev/null; then
        echo "Bucket does not exist. Creating it..."
        gcloud storage buckets create "gs://${bucket}" --project="$project_id" --location="{{ location }}"
        gcloud storage buckets update "gs://${bucket}" --project="$project_id" --versioning
    else
        echo "Bucket already exists — ensuring object versioning is enabled."
        gcloud storage buckets update "gs://${bucket}" --project="$project_id" --versioning
    fi

    if [ -n "{{ lifecycle_config }}" ]; then
        echo "Applying lifecycle configuration from {{ lifecycle_config }}"
        gcloud storage buckets update "gs://${bucket}" --project="$project_id" --lifecycle-file="{{ lifecycle_config }}"
    fi

    pulumi login "gs://${bucket}"

    echo "Pulumi has been set up to use GCS bucket ${bucket} as the backend in location {{ location }} with object versioning enabled."
    if [ -n "{{ lifecycle_config }}" ]; then
        echo "Lifecycle configuration has been applied from {{ lifecycle_config }}."
    fi

# Select a project's Pulumi stack, initializing it first if it does not exist yet.
# Stack name: --stack flag, else $PULUMI_STACK (set once per cluster by create-cluster-env),
# else the recipe fails — no silent fallback to "dev" for a name this consequential.
# Usage: just gcp-pulumi ensure-stack --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
ensure-stack folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    if [ -z "${stack_name}" ]; then
        stack_name="${PULUMI_STACK:-}"
    fi
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — set PULUMI_STACK (via cluster env) or pass --stack."
        exit 1
    fi
    if pulumi -C {{ folder }} stack select "${stack_name}" &>/dev/null; then
        echo "Selected existing stack '${stack_name}' in {{ folder }}."
    elif [ -f "{{ folder }}/Pulumi.${stack_name}.yaml" ]; then
        echo "Stack '${stack_name}' not found in {{ folder }}. Initializing from Pulumi.${stack_name}.yaml..."
        pulumi -C {{ folder }} stack init "${stack_name}" --secrets-provider "${PULUMI_SECRET_PROVIDER}"
    else
        echo "ERROR: stack '${stack_name}' does not exist in {{ folder }}, and there is no Pulumi.${stack_name}.yaml to initialize it from." >&2
        echo "       Initializing would create an empty stack and fail at preview with 'missing required configuration variable'." >&2
        echo "       Create the file first (fork a base config), or copy it from an existing stack:" >&2
        echo "         just gcp-pulumi fork-stack --to-stack ${stack_name} [--folder {{ folder }}]" >&2
        echo "         just gcp-pulumi new-stack-from --folder {{ folder }} --stack ${stack_name} --from-stack <existing-stack>" >&2
        exit 1
    fi

# Set an encrypted config value on a stack (config set --path --secret).
# Usage: just gcp-pulumi set-secret --folder <dir> --key <config.path> --value <secret> [--stack <name>]
[arg("value", long="value", short="v", help="Secret value to store")]
[arg("key", long="key", short="k", help="Config key path, e.g. properties.secret.password")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
set-secret folder key value stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} -s "${stack_name}" config set --path --secret "{{ key }}" "{{ value }}"

# Set a plain (unencrypted) config value on a stack (config set --path).
# Usage: just gcp-pulumi set-config --folder <dir> --key <config.path> --value <val> [--stack <name>]
[arg("value", long="value", short="v", help="Config value to store")]
[arg("key", long="key", short="k", help="Config key path, e.g. properties.restoreId")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
set-config folder key value stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} -s "${stack_name}" config set --path "{{ key }}" "{{ value }}"

# Preview Pulumi changes for a stack in a folder.
# Usage: just gcp-pulumi preview --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
preview folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} -s "${stack_name}" preview

# Create a new Pulumi stack in a given folder.
# Usage: just gcp-pulumi new-stack --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="New Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
new-stack folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} stack init "${stack_name}" --secrets-provider ${PULUMI_SECRET_PROVIDER}

# Create a new stack in a folder copied from an existing stack's config.
# Usage: just gcp-pulumi new-stack-from --folder <dir> [--stack <name>] [--from-stack <name>]
[arg("stack", long="stack", short="s", help="New Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("from-stack", long="from-stack", short="F", help="Stack to copy config from")]
[no-cd]
new-stack-from folder stack="" from-stack="experiments":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} stack init "${stack_name}" --copy-config-from {{ from-stack }} --secrets-provider ${PULUMI_SECRET_PROVIDER}

# Deploy resources for a stack.
# Usage: just gcp-pulumi create-resource --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
create-resource folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} up -s "${stack_name}" -f -y

# Destroy resources for a stack.
# Usage: just gcp-pulumi remove-resource --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
remove-resource folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} destroy -s "${stack_name}" -f -y

# Remove a stack, preserving its config.
# Usage: just gcp-pulumi cleanup-resource --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
cleanup-resource folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} stack rm -s "${stack_name}" --preserve-config --force --yes

# Create resources for multiple projects listed in a file.
# Usage: just gcp-pulumi create-multiple-resources --stack <name> --from-stack <name> --resources-file <path>
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[arg("from-stack", long="from-stack", short="F", help="Stack to copy config from")]
[arg("resources_file", long="resources-file", short="r", help="File listing resource projects")]
[no-cd]
create-multiple-resources stack from-stack resources_file:
    #!/usr/bin/env bash
    set -euo pipefail

    if [[ ! -f "{{ resources_file }}" ]]; then
        echo "Error: Resources file '{{ resources_file }}' not found"
        exit 1
    fi

    echo "Reading resources from: {{ resources_file }}"

    total_projects=$(grep -v "^$" "{{ resources_file }}" | wc -l)
    current=0

    while IFS= read -r project || [[ -n "$project" ]]; do
        if [[ -z "$project" || "${project:0:1}" == "#" ]]; then
            continue
        fi

        ((current++))
        echo "[${current}/${total_projects}] Processing project: $project"

        echo "Creating stack {{ stack }} for project $project from {{ from-stack }}"
        if ! just gcp-pulumi new-stack-from --folder "$project" --stack "{{ stack }}" --from-stack "{{ from-stack }}"; then
            echo "Failed to create stack for $project. Continuing with next project..."
            continue
        fi

        echo "Creating resources for project $project in stack {{ stack }}"
        if ! just gcp-pulumi create-resource --folder "$project" --stack "{{ stack }}"; then
            echo "Failed to create resources for $project. Continuing with next project..."
            continue
        fi

        echo "Successfully deployed $project"
    done < "{{ resources_file }}"

    echo "Deployment process completed!"

# ── verification recipes ──────────────────────────────────────────────────────

# Verify the local toolchain required by this repo's Pulumi workflow.
# Prints one line per tool with its version and exits non-zero if any is missing.
# Usage: just gcp-pulumi check-tools
[group('pulumi-management')]
[no-cd]
check-tools:
    #!/usr/bin/env bash
    set -uo pipefail
    "{{ justfile_directory() }}/scripts/pulumi/check-tools.sh"

# Verify the Pulumi backend wiring for the active cluster shell.
# Checks PULUMI_* variables, the GCS state bucket, the KMS key and the active login.
# Usage: just gcp-pulumi check-backend
[group('pulumi-management')]
[no-cd]
check-backend:
    #!/usr/bin/env bash
    set -uo pipefail
    "{{ justfile_directory() }}/scripts/pulumi/check-backend.sh"

# Verify the StorageClasses this repo's database stacks depend on.
# Prints the provisioner for each class and exits non-zero if one is missing or wrong.
# Create per-cluster stack config files from an existing stack file, so every
# cluster gets unique stacks named after itself. Copies Pulumi.<from-stack>.yaml
# to Pulumi.<to-stack>.yaml in every project shipping the base (or just --folder).
# Never overwrites an existing target. Review the diff and commit, then create the
# cluster env file with --pulumi-stack <to-stack>.
# Usage: just gcp-pulumi fork-stack --to-stack <name> --from-stack <base> [--folder <dir>]
[arg("from-stack", long="from-stack", short="F", help="Base stack name — an existing Pulumi.<base>.yaml, normally the closest production cluster")]
[arg("to-stack", long="to-stack", short="t", help="New per-cluster stack name, normally the cluster name")]
[arg("folder", long="folder", short="f", help="Limit to one project folder")]
[group('pulumi-management')]
[no-cd]
fork-stack to-stack="" from-stack="" folder="":
    #!/usr/bin/env bash
    set -euo pipefail

    root="{{ justfile_directory() }}"
    to="{{ to-stack }}"
    if [ -z "${to}" ]; then
        echo "ERROR: --to-stack is required (normally the cluster name)." >&2
        exit 1
    fi
    from="{{ from-stack }}"
    if [ -z "${from}" ]; then
        echo "ERROR: --from-stack is required — the base whose Pulumi.<base>.yaml files are copied (normally the closest production cluster)." >&2
        exit 1
    fi
    only="{{ folder }}"

    created=0
    skipped=0
    for dir in "$root"/*/; do
        d="${dir%/}"
        [ -f "${d}/Pulumi.yaml" ] || continue
        if [ -n "${only}" ] && [ "$(basename "$d")" != "${only}" ]; then
            continue
        fi
        src="${d}/Pulumi.${from}.yaml"
        dst="${d}/Pulumi.${to}.yaml"
        [ -f "${src}" ] || continue
        if [ -f "${dst}" ]; then
            echo "exists, skipped: ${dst#${root}/}"
            skipped=$((skipped + 1))
            continue
        fi
        cp "${src}" "${dst}"
        echo "created: ${dst#${root}/}"
        created=$((created + 1))
    done

    if [ "$created" -eq 0 ] && [ "$skipped" -eq 0 ]; then
        echo "ERROR: no project ships Pulumi.${from}.yaml — nothing forked." >&2
        exit 1
    fi

    echo
    echo "Forked ${created}, skipped ${skipped}. Review the diff and commit the deltas, then:"
    echo "  just create-cluster-env --env <env> --cluster ${to} --pulumi-stack ${to} --force yes"

# Apply the StorageClass stack, then verify the classes it declares. Expected
# class names and provisioner are derived from storage_class/Pulumi.<stack>.yaml,
# so prod (two classes) and lab/local (one class, different provisioner) verify
# correctly without flags. Verification retries while the classes are not yet
# visible on the cluster.
# Usage: just gcp-pulumi apply-storageclass [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('pulumi-management')]
[no-cd]
apply-storageclass stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="storage_class"
    STACK="{{ stack }}"
    if [ -z "${STACK}" ]; then
        STACK="${PULUMI_STACK:-}"
    fi
    if [ -z "${STACK}" ]; then
        echo "ERROR: no stack name — set PULUMI_STACK (via cluster env) or pass --stack." >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "ERROR: yq not found on PATH — install it (see docs/reference/pulumi/prerequisites.md)." >&2
        exit 1
    fi

    echo "==> Applying ${FOLDER} on stack '${STACK}'"
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    cfg_file="${FOLDER}/Pulumi.${STACK}.yaml"
    if [ ! -f "$cfg_file" ]; then
        echo "ERROR: stack config not found: ${cfg_file}" >&2
        exit 1
    fi
    classes=$(yq -r '[.config."storage-class:properties" | if has("classes") then .classes[] else . end] | .[].name' "$cfg_file" | paste -sd, -)
    provisioner=$(yq -r '[.config."storage-class:properties" | if has("classes") then .classes[] else . end] | .[0].provisioner' "$cfg_file")
    if [ -z "$classes" ] || [ "$classes" = "null" ] || [ -z "$provisioner" ] || [ "$provisioner" = "null" ]; then
        echo "ERROR: could not derive classes/provisioner from ${cfg_file}" >&2
        exit 1
    fi

    echo "==> Verifying ${classes} (provisioner ${provisioner})"
    attempt=0
    until just gcp-pulumi check-storageclass --classes "$classes" --provisioner "$provisioner" >/dev/null 2>&1; do
        attempt=$((attempt + 1))
        if [ "$attempt" -ge 12 ]; then
            echo "StorageClass still not visible after ${attempt} attempts:" >&2
            just gcp-pulumi check-storageclass --classes "$classes" --provisioner "$provisioner"
            exit 1
        fi
        echo "    not visible yet (attempt ${attempt}/12) — waiting 5s"
        sleep 5
    done
    just gcp-pulumi check-storageclass --classes "$classes" --provisioner "$provisioner"
    echo "StorageClass ready."

# Verify StorageClasses exist with the expected provisioner and report the
# cluster default.
# Usage: just gcp-pulumi check-storageclass [--classes <comma-separated>] [--provisioner <name>]
[arg("classes", long="classes", short="c", help="Comma-separated StorageClass names to require")]
[arg("provisioner", long="provisioner", short="p", help="Expected provisioner")]
[group('pulumi-management')]
[no-cd]
check-storageclass classes="dictycr-balanced" provisioner="pd.csi.storage.gke.io":
    #!/usr/bin/env bash
    set -uo pipefail

    WANT_PROVISIONER="{{ provisioner }}"
    failures=0

    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"

    echo "Checking StorageClasses (expected provisioner: ${WANT_PROVISIONER})..."
    echo

    sc_json=$(kubectl get storageclass -o json 2>/dev/null)
    if [[ -z "$sc_json" ]]; then
        printf '\033[31mFAIL\033[0m  cannot reach the cluster — check KUBECONFIG\n'
        exit 1
    fi

    IFS=',' read -ra WANT_CLASSES <<< "{{ classes }}"
    for raw in "${WANT_CLASSES[@]}"; do
        sc="${raw// /}"
        [[ -z "$sc" ]] && continue
        found=$(printf '%s\n' "$sc_json" | jq -r --arg n "$sc" '.items[] | select(.metadata.name == $n) | .provisioner')
        if [[ -z "$found" ]]; then
            bad "StorageClass $sc missing — is it declared in storage_class/Pulumi.<stack>.yaml?"
            continue
        fi
        if [[ "$found" == "$WANT_PROVISIONER" ]]; then
            ok "StorageClass $sc uses $found"
        else
            bad "StorageClass $sc uses $found, expected $WANT_PROVISIONER"
        fi
    done

    # Report the default class, if any — a surprise default causes silent misplacement
    default_sc=$(printf '%s\n' "$sc_json" | jq -r '.items[]
        | select(.metadata.annotations["storageclass.kubernetes.io/is-default-class"] == "true")
        | .metadata.name' | paste -sd, -)
    if [[ -n "$default_sc" ]]; then
        info "default StorageClass: $default_sc"
    else
        info "no default StorageClass set — every PVC must name one explicitly"
    fi

    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mAll StorageClass checks passed.\033[0m\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m PVCs will stay Pending. See docs/reference/pulumi/storage-class.md\n' "$failures"
        exit 1
    fi
