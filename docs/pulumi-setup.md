# Pulumi Post-Provisioning

**Last tested**: 2026-07-16

## Overview

Once a kops cluster is running (see `docs/kops-setup-draft.md`), deploy the application stack on top using Pulumi.

## Deploy

1.  **Initialize & Deploy**:
    ```bash
    # Set these once for your environment, then the command is copy-paste-ready:
    export STACK_NAME="<stack-name>"        # e.g., dev-my-app
    export FROM_STACK="<base-stack>"         # base stack to inherit config from, or use "" for none
    export PROJECT_ID="<your-gcp-project-id>"
    export KEYRING="<kms-keyring>"           # created during cluster bootstrap
    export KEY="<kms-key>"                    # created during cluster bootstrap
    export BUCKET="<pulumi-state-bucket>"     # e.g., pulumi-state-my-cluster

    just pulumi-init-and-deploy ${STACK_NAME} ${FROM_STACK} ${PROJECT_ID} ${KEYRING} ${KEY} ${BUCKET}
    ```
    *Creates Pulumi IAM, KMS keys, state bucket, and deploys initial resources.*

    **Expected healthy output**:
    ```
    Updating stack '<STACK_NAME>'
    ...
    Resources:
        + N created
    Duration: XmYs
    ```
    followed by a summary of created resources (service accounts, KMS keyrings, storage buckets).

2.  **Verify the deployment**:
    ```bash
    pulumi stack output --stack <STACK_NAME>
    ```
    **Expected output**: Lists the stack's exported values (e.g., service account emails, bucket names). If this returns nothing or an error, the deployment may not have completed — re-run the deploy command.

## Troubleshooting

**If Pulumi fails with an IAM or permissions error**: This typically means the `kops-cluster-creator` key doesn't have the roles Pulumi needs (Pulumi requires its own `pulumi-manager` service account, which `pulumi-init-and-deploy` creates). Check that `GOOGLE_APPLICATION_CREDENTIALS` still points at `kops-cluster-creator.json`:

```bash
echo $GOOGLE_APPLICATION_CREDENTIALS
```

If it points at `sa-manager.json`, re-run `just cluster-cred <env> <cluster> credentials/kops-cluster-creator.json`, then `exit` and `just cluster-env <env> <cluster>`. If the error persists after 2 retries, escalate to the platform-lead.
