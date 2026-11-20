# Falcon Registry Access Details

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## What It Does

`grant-registry-access` gives the sa-manager service account the `roles/artifactregistry.admin` role on the cluster project. `mirror-image` uses this role to create the repository, to change the repository IAM policy, and to push the image.

No other person or account is necessary. The sa-manager service account has the `roles/resourcemanager.projectIamAdmin` role, so it can add the role to itself.

## Command

```bash
just falcon grant-registry-access
```

## Behavior

**Checks:**

1. Make sure that `PROJECT_ID` is set and that `GOOGLE_APPLICATION_CREDENTIALS` is the sa-manager key of that project.
2. Make sure that `properties.registry.location` and `properties.registry.repository` are valid.
3. Read the project IAM policy.
4. Make sure that the Artifact Registry API is on.

**Changes:**

1. Add the role binding when the project IAM policy does not have it. When the binding exists, make no change.
2. Wait until Artifact Registry accepts the sa-manager key. The wait starts at `--interval` seconds and doubles to a maximum of 60 seconds.

The recipe retries only the `PERMISSION_DENIED` response. Each other error stops the recipe.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
| `--retries` | No | `8` | Probe attempts after the grant |
| `--interval` | No | `5` | First wait in seconds |

## Why a Project Role

The repository does not exist before the first `mirror-image`. A role on the repository cannot give permission to create it. Of the predefined roles, only `roles/artifactregistry.admin` lets an account create repositories and change their IAM policies.

## Organization Policy

An organization policy can block IAM changes. In that condition, `gcloud` shows the policy name in the error. Ask the organization administrator to change the policy.
