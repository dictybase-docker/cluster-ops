package main

import (
	"fmt"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/nsprobe"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type BackupSecretsConfig struct {
	Secret struct {
		Name           string
		ResticPass     string
		GcsProject     string
		ServiceAccount struct {
			Filepath string
			Keyname  string
		}
	}
}

type BackupSecrets struct {
	Config *BackupSecretsConfig
}

func ReadConfig(ctx *pulumi.Context) (*BackupSecretsConfig, error) {
	conf := config.New(ctx, "")
	backupConfig := &BackupSecretsConfig{}
	if err := conf.TryObject("properties", backupConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read redis-backup-secrets config: %w",
			err,
		)
	}
	return backupConfig, nil
}

func NewBackupSecrets(config *BackupSecretsConfig) *BackupSecrets {
	return &BackupSecrets{
		Config: config,
	}
}

func (bsr *BackupSecrets) Install(ctx *pulumi.Context) error {
	// Namespaces are NOT created here — the namespace-bootstrap stack owns
	// them (just gcp-pulumi apply-namespaces, docs/pulumi-setup.md §5). Probe
	// it so a missing bootstrap fails at preview, before any secret work, and
	// take the app namespace from its appNamespace export — the single
	// source of truth for where the Secret lives.
	probe, appNamespace, err := nsprobe.Probe(ctx, "appNamespace")
	if err != nil {
		return err
	}

	// Read the content of the file specified by Filepath
	serviceAccountContent, err := os.ReadFile(
		bsr.Config.Secret.ServiceAccount.Filepath,
	)
	if err != nil {
		return fmt.Errorf("error reading service account file: %w", err)
	}

	secret, err := corev1.NewSecret(
		ctx,
		bsr.Config.Secret.Name,
		&corev1.SecretArgs{
			Metadata: bsr.createMetadata(appNamespace),
			StringData: pulumi.StringMap{
				"resticPass": pulumi.String(
					bsr.Config.Secret.ResticPass,
				),
				"gcsProject": pulumi.String(
					bsr.Config.Secret.GcsProject,
				),
				bsr.Config.Secret.ServiceAccount.Keyname: pulumi.String(
					string(serviceAccountContent),
				),
			},
		},
		pulumi.DependsOn([]pulumi.Resource{probe}),
	)
	if err != nil {
		return fmt.Errorf("error creating backup secret: %w", err)
	}

	ctx.Export("secretName", secret.Metadata.Name())
	return nil
}

func (bsr *BackupSecrets) createMetadata(
	namespace pulumi.StringInput,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(bsr.Config.Secret.Name),
		Namespace: namespace.ToStringPtrOutput(),
	}
}

func Run(ctx *pulumi.Context) error {
	backupConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	backupSecrets := NewBackupSecrets(backupConfig)

	if err := backupSecrets.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
