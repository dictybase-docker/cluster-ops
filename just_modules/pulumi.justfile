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
    if pulumi -C {{ quote(folder) }} stack select "${stack_name}" &>/dev/null; then
        echo "Selected existing stack '${stack_name}' in {{ folder }}."
    elif [ -f "{{ folder }}/Pulumi.${stack_name}.yaml" ]; then
        echo "Stack '${stack_name}' not found in {{ folder }}. Initializing from Pulumi.${stack_name}.yaml..."
        pulumi -C {{ quote(folder) }} stack init "${stack_name}" --secrets-provider "${PULUMI_SECRET_PROVIDER}"
    else
        echo "ERROR: stack '${stack_name}' does not exist in {{ folder }}, and there is no Pulumi.${stack_name}.yaml to initialize it from." >&2
        echo "       Initializing would create an empty stack and fail at preview with 'missing required configuration variable'." >&2
        echo "       Create the file first (fork a base config), or copy it from an existing stack:" >&2
        echo "         just gcp-pulumi fork-stack --to-stack ${stack_name} [--folder {{ folder }}]" >&2
        echo "         just gcp-pulumi new-stack-from --folder {{ folder }} --stack ${stack_name} --from-stack <existing-stack>" >&2
        exit 1
    fi

# Set an encrypted config value on a stack (config set --path --secret).
# Omit --value to enter the secret at a hidden prompt. That path keeps the
# value out of the shell history and the process arguments.
# Usage: just gcp-pulumi set-secret --folder <dir> --key <config.path> [--value <secret>] [--stack <name>]
[arg("value", long="value", short="v", help="Secret value to store; omit to enter it at a hidden prompt")]
[arg("key", long="key", short="k", help="Config key path, e.g. properties.secret.password")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
set-secret folder key value="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name={{ quote(stack) }}
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    secret_value={{ quote(value) }}
    if [ -n "${secret_value}" ]; then
        pulumi -C {{ quote(folder) }} -s "${stack_name}" config set --path --secret {{ quote(key) }} "${secret_value}"
    else
        read -rsp "Secret value (input hidden): " secret_value
        echo
        printf '%s' "${secret_value}" | pulumi -C {{ quote(folder) }} -s "${stack_name}" config set --path --secret {{ quote(key) }}
    fi

# Set a plain (unencrypted) config value on a stack (config set --path).
# Usage: just gcp-pulumi set-config --folder <dir> --key <config.path> --value <val> [--stack <name>] [--plaintext <yes>]
[arg("value", long="value", short="v", help="Config value to store")]
[arg("key", long="key", short="k", help="Config key path, e.g. properties.restoreId")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("plaintext", long="plaintext", help="Pass --plaintext when the value is not a secret but the CLI would guess otherwise")]
[no-cd]
set-config folder key value stack="" plaintext="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name={{ quote(stack) }}
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    if [ -n "{{ plaintext }}" ]; then
        pulumi -C {{ quote(folder) }} -s "${stack_name}" config set --path --plaintext {{ quote(key) }} {{ quote(value) }}
    else
        pulumi -C {{ quote(folder) }} -s "${stack_name}" config set --path {{ quote(key) }} {{ quote(value) }}
    fi

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
    pulumi -C {{ quote(folder) }} -s "${stack_name}" preview

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
    pulumi -C {{ quote(folder) }} stack init "${stack_name}" --secrets-provider ${PULUMI_SECRET_PROVIDER}

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
    pulumi -C {{ quote(folder) }} stack init "${stack_name}" --copy-config-from {{ quote(from-stack) }} --secrets-provider ${PULUMI_SECRET_PROVIDER}

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
    pulumi -C {{ quote(folder) }} up -s "${stack_name}" -f -y

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
    pulumi -C {{ quote(folder) }} destroy -s "${stack_name}" -f -y

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
    pulumi -C {{ quote(folder) }} stack rm -s "${stack_name}" --preserve-config --force --yes

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

# Apply the namespace-bootstrap stack: the shared `operators` and app
# (`prod` on production, `dev` on lab stacks) namespaces. Run once per cluster
# during setup, right after apply-storageclass — operator programs
# (cloudnative-pg-operator, arangodb-operator) probe this stack via
# StackReference + a live GetNamespace read, and backup_secrets assumes it.
# The recipe applies the stack, then verifies exactly the namespaces
# Pulumi.<stack>.yaml declares.
# Usage: just gcp-pulumi apply-namespaces [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('pulumi-management')]
[no-cd]
apply-namespaces stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="namespace-bootstrap"
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

    cfg_file="${FOLDER}/Pulumi.${STACK}.yaml"
    if [ ! -f "$cfg_file" ]; then
        echo "ERROR: stack config not found: ${cfg_file} — fork one with 'just gcp-pulumi fork-stack'." >&2
        exit 1
    fi

    echo "==> Applying ${FOLDER} on stack '${STACK}'"
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
    failures=0
    echo "==> Verifying namespaces from ${cfg_file}"
    for ns in $(yq -r '.config."namespace-bootstrap:properties" | .operatorNamespace + " " + .appNamespace' "$cfg_file"); do
        if kubectl get namespace "$ns" >/dev/null 2>&1; then
            ok "namespace ${ns} exists"
        else
            bad "namespace ${ns} missing"
        fi
    done

    echo
    if [ "$failures" -eq 0 ]; then
        printf '\033[32mNamespaces ready.\033[0m\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See docs/reference/pulumi/namespaces.md\n' "$failures"
        exit 1
    fi

# Scaffold a backend service stack config from the registry + template.
# Derives appName (folder minus any 'modware-' prefix), image name
# (dictybase/<folder>), config key prefix, and the default ArangoDB Secret
# name (appName) from --folder; namespace and KMS secrets provider come from
# the cluster registry entry whose 'stack' matches. Runs `pulumi stack init
# --secrets-provider` to register the stack, then appends the config block
# from config/templates/backend-stack.yaml.tmpl. Describe-then-create: fails
# when Pulumi.<stack>.yaml already exists — never overwrites tuned config.
# The template's image tag is a 'bootstrap' placeholder; CI overwrites the
# tag at deploy time, and the manual first deploy (bootstrap-service) takes
# --image-tag.
# Usage: just gcp-pulumi scaffold-backend-stack --folder <dir> [--stack <name>] [--port <n>] [--secret-name <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK); must match a registry entry's stack key")]
[arg("folder", long="folder", short="f", help="Service project folder (e.g. modware-order)")]
[arg("port", long="port", short="p", help="gRPC server port")]
[arg("secret_name", long="secret-name", help="ArangoDB credentials Secret name (default: appName — one Secret per service)")]
[group('pulumi-management')]
[no-cd]
scaffold-backend-stack folder stack="" port="9250" secret_name="":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    [ -z "${stack_name}" ] && stack_name="${PULUMI_STACK:-}"
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — pass --stack or set PULUMI_STACK (via cluster env)." >&2
        exit 1
    fi

    folder="{{ folder }}"
    folder="${folder%/}"
    if [ ! -d "${folder}" ]; then
        echo "ERROR: folder '${folder}' does not exist." >&2
        exit 1
    fi
    project="$(basename "${folder}")"
    app="${project#modware-}"

    # Registry lookup by stack key — data-driven, no stack->namespace table here.
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
        echo "       Add one, then re-run. See docs/reference/backend/cluster-registry.md." >&2
        exit 1
    fi
    namespace=$(yq -r '.namespace' "${entry}")
    kms=$(yq -r '.kms_secrets_provider' "${entry}")

    sec_name="{{ secret_name }}"
    [ -z "${sec_name}" ] && sec_name="${app}"

    cfg="${folder}/Pulumi.${stack_name}.yaml"
    if [ -e "${cfg}" ]; then
        echo "ERROR: ${cfg} already exists — refusing to overwrite (describe-then-create)." >&2
        echo "       Edit it by hand, or delete it only if you own it." >&2
        exit 1
    fi

    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS:-}"
    pulumi -C "${folder}" stack init "${stack_name}" --secrets-provider "${kms}"

    tmp=$(mktemp)
    trap 'rm -f "${tmp}"' EXIT
    sed \
        -e "s|__PROJECT__|${project}|g" \
        -e "s|__APP__|${app}|g" \
        -e "s|__IMAGE_REPO__|${project}|g" \
        -e "s|__SECRET_NAME__|${sec_name}|g" \
        -e "s|__NAMESPACE__|${namespace}|g" \
        -e "s|__PORT__|{{ port }}|g" \
        config/templates/backend-stack.yaml.tmpl > "${tmp}"
    cat "${tmp}" >> "${cfg}"

    # Validation: parses, required keys present, no mutable :latest tag.
    yq -r '.config' "${cfg}" >/dev/null
    got_app=$(yq -r ".config.\"${project}:properties\".appName" "${cfg}")
    got_ns=$(yq -r ".config.\"${project}:properties\".namespace" "${cfg}")
    if [ "${got_app}" != "${app}" ] || [ "${got_ns}" != "${namespace}" ]; then
        echo "ERROR: rendered config failed validation (appName=${got_app}, namespace=${got_ns})." >&2
        exit 1
    fi
    if grep -q ':latest' "${cfg}"; then
        echo "ERROR: rendered config contains :latest — prod stacks must never carry a mutable tag." >&2
        exit 1
    fi

    echo "Scaffolded ${cfg}"
    echo "  appName=${app} image=dictybase/${project}:bootstrap namespace=${namespace} secret=${sec_name} port={{ port }}"
    echo "Next: just gcp-pulumi check-backend-prereqs --stack ${stack_name} --folder ${folder}"

# Composite read-only prerequisite gate for a backend service deploy.
# Checks, in order: registry entry (by stack), namespace exists, ArangoDB
# credentials Secret exists with the configured keys, the application
# database exists and the credentials work **through the same Service the app
# connects to** (port-forward svc/arangodb, then ArangoDB REST API), and the
# port is in range. Zero mutations; exits non-zero listing every missing
# prerequisite; never prints Secret values. bootstrap-service runs this first
# and aborts before any mutation on failure.
# Usage: just gcp-pulumi check-backend-prereqs --folder <dir> [--stack <name>] [--arango-service <name>] [--arango-port <n>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK); must match a registry entry's stack key")]
[arg("folder", long="folder", short="f", help="Service project folder (stack config source)")]
[arg("arango_service", long="arango-service", short="a", help="ArangoDB Service name the app connects to (default arangodb — the source of ARANGODB_SERVICE_HOST)")]
[arg("arango_port", long="arango-port", short="p", help="Local port for the port-forward probe (default 18529)")]
[group('pulumi-management')]
[no-cd]
check-backend-prereqs folder stack="" arango_service="arangodb" arango_port="18529":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    [ -z "${stack_name}" ] && stack_name="${PULUMI_STACK:-}"
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — pass --stack or set PULUMI_STACK (via cluster env)." >&2
        exit 1
    fi
    folder="{{ folder }}"
    folder="${folder%/}"
    cfg="${folder}/Pulumi.${stack_name}.yaml"
    if [ ! -f "${cfg}" ]; then
        echo "MISSING: ${cfg} — scaffold it first (scaffold-backend-stack)." >&2
        exit 1
    fi

    # Registry lookup by stack key.
    entry=""
    for f in config/clusters/*.yaml; do
        [ -e "${f}" ] || continue
        if [ "$(yq -r '.stack' "${f}")" = "${stack_name}" ]; then
            entry="${f}"
            break
        fi
    done
    if [ -z "${entry}" ]; then
        echo "MISSING: no registry entry with stack '${stack_name}' (checked config/clusters/*.yaml)." >&2
        exit 1
    fi
    namespace=$(yq -r '.namespace' "${entry}")
    project="$(basename "${folder}")"
    props=".config.\"${project}:properties\""

    secret_name=$(yq -r "${props}.arangodbSecret.name" "${cfg}")
    userkey=$(yq -r "${props}.arangodbSecret.userkey" "${cfg}")
    passkey=$(yq -r "${props}.arangodbSecret.passkey" "${cfg}")
    port=$(yq -r "${props}.port" "${cfg}")
    app=$(yq -r "${props}.appName" "${cfg}")

    missing=0

    echo "=== namespace ${namespace} ==="
    if ! kubectl get ns "${namespace}" >/dev/null 2>&1; then
        echo "MISSING: namespace ${namespace} — run namespace-bootstrap for this cluster." >&2
        missing=1
    fi

    echo "=== credentials Secret ${secret_name} (keys ${userkey}/${passkey}) ==="
    if ! kubectl get secret "${secret_name}" -n "${namespace}" >/dev/null 2>&1; then
        echo "MISSING: secret ${secret_name} in namespace ${namespace} — create it with the ArangoDB credentials." >&2
        missing=1
    else
        for k in "${userkey}" "${passkey}"; do
            if [ -z "$(kubectl get secret "${secret_name}" -n "${namespace}" -o "jsonpath={.data.${k}}" 2>/dev/null)" ]; then
                echo "MISSING: secret ${secret_name} key '${k}' — key names must match the stack config." >&2
                missing=1
            fi
        done
    fi

    echo "=== ArangoDB database ${app} via svc/{{ arango_service }} ==="
    if [ "${missing}" -eq 0 ]; then
        dbuser=$(kubectl get secret "${secret_name}" -n "${namespace}" -o "jsonpath={.data.${userkey}}" | base64 -d)
        dbpass=$(kubectl get secret "${secret_name}" -n "${namespace}" -o "jsonpath={.data.${passkey}}" | base64 -d)
        local_port="{{ arango_port }}"
        pf_log=$(mktemp)
        kubectl port-forward -n "${namespace}" "svc/{{ arango_service }}" "${local_port}:8529" > "${pf_log}" 2>&1 &
        pf_pid=$!
        cleanup_pf() { kill "${pf_pid}" 2>/dev/null || true; rm -f "${pf_log}"; }
        trap cleanup_pf EXIT
        pf_up=0
        for _ in $(seq 1 15); do
            nc -z localhost "${local_port}" 2>/dev/null && { pf_up=1; break; }
            sleep 1
        done
        if [ "${pf_up}" -ne 1 ]; then
            echo "MISSING: could not reach svc/{{ arango_service }} in ${namespace} — port-forward never came up. Check the Service exists and its pods are ready." >&2
            sed 's/^/         /' "${pf_log}" >&2
            missing=1
        else
            code=$(curl -s -o /dev/null -w '%{http_code}' \
                -u "${dbuser}:${dbpass}" \
                "http://localhost:${local_port}/_db/${app}/_api/database/current")
            case "${code}" in
                200) : ;;
                401)
                    echo "MISSING: credentials rejected for database ${app} (401) — the Secret does not match the ArangoDB user; check create-arangodb-databases." >&2
                    missing=1
                    ;;
                404)
                    echo "MISSING: database ${app} not found (404) via svc/{{ arango_service }} — apply the create-arangodb-databases stack." >&2
                    missing=1
                    ;;
                *)
                    echo "MISSING: unexpected HTTP ${code} from /_db/${app}/_api/database/current via svc/{{ arango_service }}." >&2
                    missing=1
                    ;;
            esac
        fi
        cleanup_pf
        trap - EXIT
        unset dbuser dbpass
    fi

    echo "=== port ${port} ==="
    if ! [[ "${port}" =~ ^[0-9]+$ ]] || [ "${port}" -lt 1024 ] || [ "${port}" -gt 65535 ]; then
        echo "MISSING: port '${port}' is not in the 1024-65535 range." >&2
        missing=1
    fi

    if [ "${missing}" -ne 0 ]; then
        echo "ERROR: prerequisite gate failed — fix the MISSING lines above, then re-run." >&2
        exit 1
    fi
    echo "All prerequisites green for ${project}/${stack_name}."
    echo "Next: just gcp-pulumi bootstrap-service --stack ${stack_name} --folder ${folder} --image-tag <published-tag>"

# Run the first deploy of a scaffolded backend stack, preflight-gated.
# Fixed order (the contract test asserts it): check-backend-prereqs →
# ensure-stack → preview → update → rollout wait + image check. Aborts before
# any mutation when prerequisites fail. Idempotent: when the deployment
# already runs the wanted image tag, the recipe verifies rollout only and
# touches nothing. --image-tag is optional: without it the tag comes from
# the repo's highest semver tag (just ci latest-tag). The stack file commit
# and push stay manual — CI deploys clone cluster-ops develop, so an unpushed
# stack file deploys nothing. Named bootstrap-service because bootstrap-backend
# already means the Pulumi state backend.
# Usage: just gcp-pulumi bootstrap-service --folder <dir> [--stack <name>] [--image-tag <tag>] [--arango-service <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK); must match a registry entry's stack key")]
[arg("folder", long="folder", short="f", help="Service project folder")]
[arg("image_tag", long="image-tag", short="t", help="Image tag to deploy (default: the repo's highest semver tag via just ci latest-tag)")]
[arg("arango_service", long="arango-service", short="a", help="ArangoDB Service name for the prereq gate (default arangodb)")]
[group('pulumi-management')]
[no-cd]
bootstrap-service folder image_tag="" stack="" arango_service="arangodb":
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"

    stack_name="{{ stack }}"
    [ -z "${stack_name}" ] && stack_name="${PULUMI_STACK:-}"
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — pass --stack or set PULUMI_STACK (via cluster env)." >&2
        exit 1
    fi
    folder="{{ folder }}"
    folder="${folder%/}"
    tag="{{ image_tag }}"
    project="$(basename "${folder}")"
    if [ -z "${tag}" ]; then
        tag=$(just ci latest-tag --repo "dictybase/${project}")
        echo "Resolved image tag from repo tags: ${tag}"
    fi
    app=$(yq -r ".config.\"${project}:properties\".appName" "${folder}/Pulumi.${stack_name}.yaml")
    namespace=$(yq -r ".config.\"${project}:properties\".namespace" "${folder}/Pulumi.${stack_name}.yaml")

    gate_args=(--stack "${stack_name}" --folder "${folder}")
    [ -n "{{ arango_service }}" ] && gate_args+=(--arango-service "{{ arango_service }}")

    echo "=== 1/5: prerequisite gate (read-only) ==="
    just gcp-pulumi check-backend-prereqs "${gate_args[@]}"

    deploy="${app}-api-server"
    if kubectl -n "${namespace}" get "deploy/${deploy}" >/dev/null 2>&1; then
        running=$(kubectl -n "${namespace}" get "deploy/${deploy}" -o jsonpath='{.spec.template.spec.containers[0].image}')
        if [ "${running}" = "dictybase/${project}:${tag}" ]; then
            echo "Deployment already runs ${running} — verifying rollout only (idempotent skip)."
            kubectl -n "${namespace}" rollout status "deploy/${deploy}"
            exit 0
        fi
        echo "Deployment runs ${running}; converging to dictybase/${project}:${tag}."
    fi

    echo "=== 2/5: ensure stack (select or init — fails when the stack file is absent) ==="
    just gcp-pulumi ensure-stack --stack "${stack_name}" --folder "${folder}"

    echo "=== 3/5: set image tag ${tag} + preview ==="
    just gcp-pulumi set-config --stack "${stack_name}" --folder "${folder}" \
        --key properties.image.tag --value "${tag}" --plaintext yes
    just gcp-pulumi preview --stack "${stack_name}" --folder "${folder}"

    echo "=== 4/5: update (pulumi up) ==="
    just gcp-pulumi create-resource --stack "${stack_name}" --folder "${folder}"

    echo "=== 5/5: rollout + image check ==="
    kubectl -n "${namespace}" rollout status "deploy/${deploy}"
    running=$(kubectl -n "${namespace}" get "deploy/${deploy}" -o jsonpath='{.spec.template.spec.containers[0].image}')
    if [ "${running}" != "dictybase/${project}:${tag}" ]; then
        echo "ERROR: running image '${running}' does not match the requested tag '${tag}'." >&2
        exit 1
    fi
    echo "Deployed ${deploy} in ${namespace} with image ${running}."
    echo "Next: commit ${folder}/Pulumi.${stack_name}.yaml, push cluster-ops develop, then just ci check-deploy-credentials."
