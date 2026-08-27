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
        echo "Bucket already exists."
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
    else
        echo "Stack '${stack_name}' not found in {{ folder }}. Initializing..."
        pulumi -C {{ folder }} stack init "${stack_name}" --secrets-provider "${PULUMI_SECRET_PROVIDER}"
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
    pulumi -C {{ folder }} stack rm -s "${stack_name}" --preserve-config --force

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

    failures=0

    ok()   { printf '\033[32mPASS\033[0m  %s\n' "$1"; }
    bad()  { printf '\033[31mFAIL\033[0m  %s\n' "$1"; failures=$((failures + 1)); }

    echo "Checking Pulumi workflow toolchain..."
    echo

    check_tool() {
        local bin="$1" label="$2" version=""
        if ! command -v "$bin" >/dev/null 2>&1; then
            bad "$label not found on PATH"
            return
        fi
        case "$bin" in
            pulumi)  version=$(pulumi version 2>/dev/null | head -n1) ;;
            gcloud)  version=$(gcloud version 2>/dev/null | awk '/^Google Cloud SDK/ {print $NF; exit}') ;;
            kubectl) version=$(kubectl version --client -o json 2>/dev/null | jq -r '.clientVersion.gitVersion' 2>/dev/null) ;;
            jq)      version=$(jq --version 2>/dev/null) ;;
            *)       version=$("$bin" --version 2>/dev/null | head -n1) ;;
        esac
        [[ -z "$version" || "$version" == "null" ]] && version="version unknown"
        ok "$label $version"
    }

    check_tool pulumi  "pulumi"
    check_tool gcloud  "gcloud"
    check_tool kubectl "kubectl"
    check_tool jq      "jq"

    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mAll required tools present.\033[0m\n'
    else
        printf '\033[31m%d tool(s) missing.\033[0m Install with: just install-tool --name <tool> --version <version>\n' "$failures"
        exit 1
    fi

# Verify the Pulumi backend wiring for the active cluster shell.
# Checks PULUMI_* variables, the GCS state bucket, the KMS key and the active login.
# Usage: just gcp-pulumi check-backend
[group('pulumi-management')]
[no-cd]
check-backend:
    #!/usr/bin/env bash
    set -uo pipefail

    failures=0

    ok()   { printf '\033[32mPASS\033[0m  %s\n' "$1"; }
    bad()  { printf '\033[31mFAIL\033[0m  %s\n' "$1"; failures=$((failures + 1)); }
    info() { printf '\033[34mINFO\033[0m  %s\n' "$1"; }

    echo "Checking Pulumi backend wiring..."
    echo

    # Required environment variables
    for var in PULUMI_GCP_CREDENTIALS PULUMI_SECRET_PROVIDER PULUMI_BACKEND_URL PULUMI_STACK; do
        if [[ -n "${!var:-}" ]]; then
            ok "$var is set"
        else
            bad "$var is empty — enter the cluster shell with 'just cluster-env'"
        fi
    done

    # Credentials file must exist before anything can authenticate
    creds="${PULUMI_GCP_CREDENTIALS:-}"
    if [[ -n "$creds" && -f "$creds" ]]; then
        ok "manager key present at $creds"
        export GOOGLE_APPLICATION_CREDENTIALS="$creds"
        project_id=$(jq -r '.project_id // empty' "$creds" 2>/dev/null)
        [[ -n "$project_id" ]] && info "project: $project_id"
    elif [[ -n "$creds" ]]; then
        bad "manager key missing at $creds — run 'just gcp-sa create-sa --sa-name pulumi-manager'"
        project_id=""
    else
        project_id=""
    fi

    # State bucket: exists and has versioning enabled
    bucket="${PULUMI_BACKEND_URL:-}"
    bucket="${bucket#gs://}"
    if [[ -n "$bucket" ]]; then
        if gcloud storage buckets describe "gs://${bucket}" ${project_id:+--project="$project_id"} >/dev/null 2>&1; then
            ok "state bucket gs://${bucket} exists"
            versioned=$(gcloud storage buckets describe "gs://${bucket}" \
                ${project_id:+--project="$project_id"} --format="value(versioning.enabled)" 2>/dev/null)
            if [[ "$versioned" == "True" || "$versioned" == "true" ]]; then
                ok "state bucket versioning enabled"
            else
                bad "state bucket versioning NOT enabled — re-run 'just gcp-pulumi pulumi-gcs-setup'"
            fi
        else
            bad "state bucket gs://${bucket} not found or unreachable"
        fi
    fi

    # KMS key referenced by the secrets provider must be readable.
    # gcloud needs KEY plus explicit --keyring/--location/--project, so parse the URI
    # rather than passing the full resource path as a bare positional.
    provider="${PULUMI_SECRET_PROVIDER:-}"
    if [[ "$provider" == gcpkms://* ]]; then
        key_path="${provider#gcpkms://}"
        key_path="${key_path%%\?*}"
        if [[ "$key_path" =~ ^projects/([^/]+)/locations/([^/]+)/keyRings/([^/]+)/cryptoKeys/([^/]+)$ ]]; then
            k_project="${BASH_REMATCH[1]}"
            k_location="${BASH_REMATCH[2]}"
            k_ring="${BASH_REMATCH[3]}"
            k_name="${BASH_REMATCH[4]}"
            if gcloud kms keys describe "$k_name" \
                    --keyring="$k_ring" --location="$k_location" --project="$k_project" \
                    >/dev/null 2>&1; then
                ok "KMS key $k_name reachable in keyring $k_ring"
            else
                bad "KMS key not reachable: $key_path — run 'just gcp-kms create-keyring-and-key'"
            fi
        else
            bad "PULUMI_SECRET_PROVIDER is not a well-formed gcpkms URI: $provider"
        fi
    elif [[ -n "$provider" ]]; then
        info "secrets provider is not gcpkms, skipping KMS check"
    fi

    # Active pulumi login must match the backend this shell expects.
    # Prefer --json; fall back to parsing --verbose for older Pulumi builds.
    if command -v pulumi >/dev/null 2>&1; then
        current=$(pulumi whoami --json 2>/dev/null | jq -r '.url // .backendURL // empty' 2>/dev/null)
        if [[ -z "$current" ]]; then
            current=$(pulumi whoami --verbose 2>/dev/null | awk -F': *' '/[Bb]ackend URL/ {print $2; exit}')
        fi
        if [[ -z "$current" ]]; then
            bad "cannot determine the active Pulumi backend (not logged in?) — run 'just gcp-pulumi pulumi-gcs-setup'"
        elif [[ -n "$bucket" && "$current" == *"$bucket"* ]]; then
            ok "active login matches $PULUMI_BACKEND_URL"
        else
            bad "active login is '$current', expected ${PULUMI_BACKEND_URL:-unset} — pulumi login is global, re-run pulumi-gcs-setup"
        fi
    fi

    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mBackend wiring looks correct.\033[0m\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See docs/reference/pulumi/backend-bootstrap.md\n' "$failures"
        exit 1
    fi

# Verify the StorageClasses this repo's database stacks depend on.
# Prints the provisioner for each class and exits non-zero if one is missing or wrong.
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

    ok()   { printf '\033[32mPASS\033[0m  %s\n' "$1"; }
    bad()  { printf '\033[31mFAIL\033[0m  %s\n' "$1"; failures=$((failures + 1)); }
    info() { printf '\033[34mINFO\033[0m  %s\n' "$1"; }

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
