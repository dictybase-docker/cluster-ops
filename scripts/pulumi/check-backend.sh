#!/usr/bin/env bash
# Verify the Pulumi backend wiring for the active cluster shell.
# Checks PULUMI_* variables, the GCS state bucket, the KMS key and the active login.
#
# Invoked by: just gcp-pulumi check-backend   (no arguments, env-driven)
# Reads: PULUMI_GCP_CREDENTIALS, PULUMI_SECRET_PROVIDER, PULUMI_BACKEND_URL, PULUMI_STACK
#
# Function contract:
#   check_credentials sets script-scope project_id (used by check_bucket)
#   check_bucket      sets script-scope bucket      (used by check_login)
#   all functions print PASS/FAIL via scripts/lib/check-helpers.sh and
#   increment the shared `failures` counter on FAIL.

set -uo pipefail

source "$(dirname "$0")/../lib/check-helpers.sh"

failures=0
project_id=""
bucket=""

check_env_vars() {
    for var in PULUMI_GCP_CREDENTIALS PULUMI_SECRET_PROVIDER PULUMI_BACKEND_URL PULUMI_STACK; do
        if [[ -n "${!var:-}" ]]; then
            ok "$var is set"
        else
            bad "$var is empty — enter the cluster shell with 'just cluster-env'"
        fi
    done
}

check_credentials() {
    # Credentials file must exist before anything can authenticate
    local creds="${PULUMI_GCP_CREDENTIALS:-}"
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
}

check_bucket() {
    # State bucket: exists and has versioning enabled
    bucket="${PULUMI_BACKEND_URL:-}"
    bucket="${bucket#gs://}"
    if [[ -n "$bucket" ]]; then
        if gcloud storage buckets describe "gs://${bucket}" ${project_id:+--project="$project_id"} >/dev/null 2>&1; then
            ok "state bucket gs://${bucket} exists"
            versioned=$(gcloud storage buckets describe "gs://${bucket}" \
                ${project_id:+--project="$project_id"} --format="value(versioning_enabled)" 2>/dev/null)
            if [[ "$versioned" == "True" || "$versioned" == "true" ]]; then
                ok "state bucket versioning enabled"
            else
                bad "state bucket versioning NOT enabled — re-run 'just gcp-pulumi pulumi-gcs-setup'"
            fi
        else
            bad "state bucket gs://${bucket} not found or unreachable"
        fi
    fi
}

check_kms() {
    # KMS key referenced by the secrets provider must be readable.
    # gcloud needs KEY plus explicit --keyring/--location/--project, so parse the URI
    # rather than passing the full resource path as a bare positional.
    local provider="${PULUMI_SECRET_PROVIDER:-}"
    if [[ "$provider" == gcpkms://* ]]; then
        local key_path="${provider#gcpkms://}"
        key_path="${key_path%%\?*}"
        if [[ "$key_path" =~ ^projects/([^/]+)/locations/([^/]+)/keyRings/([^/]+)/cryptoKeys/([^/]+)$ ]]; then
            local k_project="${BASH_REMATCH[1]}"
            local k_location="${BASH_REMATCH[2]}"
            local k_ring="${BASH_REMATCH[3]}"
            local k_name="${BASH_REMATCH[4]}"
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
}

check_login() {
    # Active pulumi login must match the backend this shell expects.
    # Prefer --json; fall back to parsing --verbose for older Pulumi builds.
    if command -v pulumi >/dev/null 2>&1; then
        local current
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
}

echo "Checking Pulumi backend wiring..."
echo

check_env_vars
check_credentials
check_bucket
check_kms
check_login

echo
if [[ "$failures" -eq 0 ]]; then
    printf '\033[32mBackend wiring looks correct.\033[0m\n'
else
    printf '\033[31m%d check(s) failed.\033[0m See docs/reference/pulumi/backend-bootstrap.md\n' "$failures"
    exit 1
fi