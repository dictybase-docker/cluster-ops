#!/usr/bin/env bash
# Bootstrap the complete Pulumi backend: preflights (identity roles,
# PULUMI_* project match), manager SA key (skip when the existing key
# authenticates), key-propagation wait, KMS keyring/key as sa-manager,
# GCS state bucket + login, then verify. Run once per GCP project.
#
# Invoked by: just gcp-pulumi bootstrap-backend   (no arguments, env-driven; the
# recipe is [no-cd], so this inherits the invocation cwd — its sub-recipe calls
# require running from the repo root, exactly as they did pre-extraction)
#
# Function contract:
#   preflight_identity    sets script-scope project_id, exits non-zero on failure
#   preflight_pulumi_vars uses project_id
#   stage_sa_key          mints a pulumi-manager key only when needed
#   stage_kms             uses GOOGLE_APPLICATION_CREDENTIALS (sa-manager)

set -euo pipefail

preflight_identity() {
    echo "==> Preflight: identity must hold the bootstrap admin roles"
    local cred="${GOOGLE_APPLICATION_CREDENTIALS:-}"
    if [ -z "${cred}" ] || [ ! -f "${cred}" ]; then
        echo "ERROR: GOOGLE_APPLICATION_CREDENTIALS unset or missing — enter the cluster shell with 'just cluster-env'." >&2
        exit 1
    fi
    local email
    email=$(jq -r '.client_email // empty' "${cred}")
    project_id="${PROJECT_ID:-$(jq -r '.project_id // empty' "${cred}")}"
    if [ -z "${project_id}" ]; then
        echo "ERROR: no project id — set PROJECT_ID or enter the cluster shell." >&2
        exit 1
    fi
    local policy
    policy=$(gcloud projects get-iam-policy "${project_id}" --format=json 2>/dev/null || true)
    if [ -z "${policy}" ]; then
        echo "ERROR: cannot read the IAM policy of ${project_id} as ${email}." >&2
        echo "Rotate to the admin identity and retry: just gcp-cluster rotate-to-manager" >&2
        exit 1
    fi
    local active_account
    active_account=$(gcloud config get-value account 2>/dev/null || true)
    if [ -z "${active_account}" ] || [ "${active_account}" != "${email}" ]; then
        echo "ERROR: gcloud active account is '${active_account}', but the credential file is '${email}' — the shell and gcloud identity drifted. Re-enter the shell:" >&2
        echo "  exit && just cluster-env --env <env> --cluster <cluster-name>" >&2
        exit 1
    fi
    local member="serviceAccount:${email}"
    local missing=""
    for role in roles/iam.serviceAccountAdmin roles/iam.serviceAccountKeyAdmin roles/resourcemanager.projectIamAdmin roles/cloudkms.admin roles/storage.admin; do
        if ! jq -e --arg m "${member}" --arg r "${role}" \
            '.bindings[]? | select(.role == $r) | .members[]? | select(. == $m)' <<<"${policy}" >/dev/null 2>&1; then
            missing="${missing}  ${role}\n"
        fi
    done
    if [ -n "${missing}" ]; then
        echo "ERROR: active identity ${email} lacks roles required by the bootstrap:" >&2
        printf '%b' "${missing}" >&2
        echo "Rotate to the admin identity and retry:" >&2
        echo "  just gcp-cluster rotate-to-manager" >&2
        echo "  exit && just cluster-env --env <env> --cluster <cluster-name>" >&2
        exit 1
    fi
    echo "    identity ${email} holds all required admin roles"
}

preflight_pulumi_vars() {
    if [ -n "${PULUMI_GCP_CREDENTIALS:-}" ] && [ -f "${PULUMI_GCP_CREDENTIALS}" ]; then
        local pulumi_cred_project
        pulumi_cred_project=$(jq -r '.project_id // empty' "${PULUMI_GCP_CREDENTIALS}" 2>/dev/null || true)
        if [ -n "${pulumi_cred_project}" ] && [ "${pulumi_cred_project}" != "${project_id}" ]; then
            echo "ERROR: PULUMI_GCP_CREDENTIALS (${PULUMI_GCP_CREDENTIALS}) belongs to project '${pulumi_cred_project}', not '${project_id}' — regenerate the env file:" >&2
            echo "  just create-cluster-env --env <env> --cluster <cluster-name> --force yes" >&2
            exit 1
        fi
    fi
    if [ -n "${PULUMI_SECRET_PROVIDER:-}" ] && ! [[ "${PULUMI_SECRET_PROVIDER}" =~ ^gcpkms://projects/${project_id}/locations/ ]]; then
        echo "ERROR: PULUMI_SECRET_PROVIDER points at another project: ${PULUMI_SECRET_PROVIDER}" >&2
        echo "  Regenerate the env file: just create-cluster-env --env <env> --cluster <cluster-name> --force yes" >&2
        exit 1
    fi
}

stage_sa_key() {
    echo "==> [1/5] pulumi-manager service account and key"
    local expected_pulumi_sa="pulumi-manager@${project_id}.iam.gserviceaccount.com"
    local minted="no"
    if [ -n "${PULUMI_GCP_CREDENTIALS:-}" ] && [ -f "${PULUMI_GCP_CREDENTIALS}" ] \
        && [ "$(jq -r '.client_email // empty' "${PULUMI_GCP_CREDENTIALS}" 2>/dev/null)" = "${expected_pulumi_sa}" ] \
        && GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}" \
            gcloud auth application-default print-access-token >/dev/null 2>&1; then
        echo "    existing key at ${PULUMI_GCP_CREDENTIALS} authenticates — skipping mint"
    else
        just gcp-sa create-sa --sa-name pulumi-manager
        minted="yes"
    fi

    if [ "${minted}" = "yes" ]; then
        echo "==> [2/5] Waiting for the fresh key to authenticate"
        local attempt=0
        until GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}" \
            gcloud auth application-default print-access-token >/dev/null 2>&1; do
            attempt=$((attempt + 1))
            if [ "${attempt}" -ge 12 ]; then
                echo "ERROR: freshly minted key never authenticated — check the SA: ${PULUMI_GCP_CREDENTIALS}" >&2
                exit 1
            fi
            echo "    key not visible yet (attempt ${attempt}/12) — waiting 10s"
            sleep 10
        done
        echo "    key authenticates"
    fi
}

stage_kms() {
    echo "==> [3/5] KMS keyring and crypto key (as sa-manager)"
    just gcp-kms create-keyring-and-key --credentials-file "${GOOGLE_APPLICATION_CREDENTIALS}"
}

stage_bucket() {
    echo "==> [4/5] GCS state bucket and pulumi login"
    just gcp-pulumi pulumi-gcs-setup
}

stage_verify() {
    echo "==> [5/5] Verify backend wiring"
    just gcp-pulumi check-backend
}

project_id=""

preflight_identity
preflight_pulumi_vars
stage_sa_key
stage_kms
stage_bucket
stage_verify