// Command source_backup_secrets creates the Secret `dictycr-source`, the
// read-only identity used to first-load this cluster from a restic snapshot
// that lives in a GCS bucket owned by a DIFFERENT GCP project.
//
// It is deliberately a separate Pulumi project from backup_secrets:
//
//   - backup_secrets owns namespaces `prod`/`operators` and Secret `dictycr`,
//     the NEW project's own ongoing-backup identity. A second stack of that
//     same program would fight over those Namespace resources.
//   - This program creates only a Secret. Namespace `prod` must already exist,
//     which it does once `just arangodb configure-backup-secrets` has run.
//
// The key NAMES are identical to `dictycr` (resticPass / gcsProject /
// gcsCredentials) because the arangodb-restore Job reads those exact key
// names; only the VALUES point at the source project. Never let the backup
// CronJob reference this Secret — that would write into the foreign bucket.
package main

import (
	"fmt"
	"os"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// SourceBackupSecretsConfig mirrors backup_secrets' config shape minus
// ExtraNamespace, since this program creates no namespaces.
type SourceBackupSecretsConfig struct {
	Namespace string
	Secret    struct {
		// ResticPass is the SOURCE repository's restic password, which may
		// differ from the new cluster's own restic password in `dictycr`.
		ResticPass string
		// GcsProject is the SOURCE GCP project id — the project that owns
		// the source bucket, not the project this cluster runs in.
		GcsProject     string
		Name           string
		ServiceAccount struct {
			Filepath string
			Keyname  string
		}
	}
}

type SourceBackupSecrets struct {
	Config *SourceBackupSecretsConfig
}

func ReadConfig(ctx *pulumi.Context) (*SourceBackupSecretsConfig, error) {
	conf := config.New(ctx, "")
	sourceConfig := &SourceBackupSecretsConfig{}
	if err := conf.TryObject("properties", sourceConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read source_backup_secrets config: %w",
			err,
		)
	}
	return sourceConfig, nil
}

func NewSourceBackupSecrets(
	config *SourceBackupSecretsConfig,
) *SourceBackupSecrets {
	return &SourceBackupSecrets{Config: config}
}

// Validate rejects an incomplete config before any Kubernetes call, so a
// missing value surfaces as a clear error instead of a Secret with empty or
// partial credentials that fails later inside the restore Job.
func (sbs *SourceBackupSecrets) Validate() error {
	if sbs.Config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if sbs.Config.Namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if sbs.Config.Secret.Name == "" {
		return fmt.Errorf("secret.name cannot be empty")
	}
	if sbs.Config.Secret.ResticPass == "" {
		return fmt.Errorf(
			"secret.resticPass cannot be empty; it is the SOURCE repository password",
		)
	}
	if sbs.Config.Secret.GcsProject == "" {
		return fmt.Errorf(
			"secret.gcsProject cannot be empty; it is the SOURCE project id",
		)
	}
	if sbs.Config.Secret.ServiceAccount.Filepath == "" {
		return fmt.Errorf("secret.serviceAccount.filepath cannot be empty")
	}
	if sbs.Config.Secret.ServiceAccount.Keyname == "" {
		return fmt.Errorf("secret.serviceAccount.keyname cannot be empty")
	}
	return nil
}

func (sbs *SourceBackupSecrets) Install(ctx *pulumi.Context) error {
	if err := sbs.Validate(); err != nil {
		return fmt.Errorf("invalid source_backup_secrets config: %w", err)
	}

	// Read at `pulumi up` time on the machine running the deploy, the same
	// os.ReadFile pattern backup_secrets uses. The recipe stores an absolute
	// path because `pulumi -C` changes the working directory.
	serviceAccountContent, err := os.ReadFile(
		sbs.Config.Secret.ServiceAccount.Filepath,
	)
	if err != nil {
		return fmt.Errorf("error reading source service account file: %w", err)
	}

	secret, err := corev1.NewSecret(
		ctx,
		sbs.Config.Secret.Name,
		&corev1.SecretArgs{
			Metadata: sbs.createMetadata(),
			StringData: pulumi.StringMap{
				"resticPass": pulumi.String(sbs.Config.Secret.ResticPass),
				"gcsProject": pulumi.String(sbs.Config.Secret.GcsProject),
				sbs.Config.Secret.ServiceAccount.Keyname: pulumi.String(
					string(serviceAccountContent),
				),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("error creating source backup secret: %w", err)
	}

	ctx.Export("secretName", secret.Metadata.Name())
	ctx.Export("namespaceName", pulumi.String(sbs.Config.Namespace))
	return nil
}

func (sbs *SourceBackupSecrets) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(sbs.Config.Secret.Name),
		Namespace: pulumi.String(sbs.Config.Namespace),
	}
}

func Run(ctx *pulumi.Context) error {
	sourceConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	return NewSourceBackupSecrets(sourceConfig).Install(ctx)
}

func main() {
	pulumi.Run(Run)
}
