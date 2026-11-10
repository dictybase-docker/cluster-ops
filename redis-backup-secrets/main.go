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

type SecretEntry struct {
	Name           string
	ResticPass     string
	GcsProject     string
	ServiceAccount struct {
		Filepath string
		Keyname  string
	}
}

type BackupSecretsConfig struct {
	Secret       SecretEntry
	SourceSecret SecretEntry
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
	probe, appNamespace, err := nsprobe.Probe(ctx, "appNamespace")
	if err != nil {
		return err
	}

	if bsr.Config.Secret.Name != "" && bsr.Config.Secret.ServiceAccount.Filepath != "" {
		secret, err := createSecretFromEntry(ctx, probe, appNamespace, bsr.Config.Secret)
		if err != nil {
			return err
		}
		ctx.Export("secretName", secret.Metadata.Name())
	}

	if bsr.Config.SourceSecret.Name != "" && bsr.Config.SourceSecret.ServiceAccount.Filepath != "" {
		sourceSecret, err := createSecretFromEntry(ctx, probe, appNamespace, bsr.Config.SourceSecret)
		if err != nil {
			return err
		}
		ctx.Export("sourceSecretName", sourceSecret.Metadata.Name())
	}

	return nil
}

func createSecretFromEntry(
	ctx *pulumi.Context,
	probe pulumi.Resource,
	namespace pulumi.StringInput,
	entry SecretEntry,
) (*corev1.Secret, error) {
	serviceAccountContent, err := os.ReadFile(entry.ServiceAccount.Filepath)
	if err != nil {
		return nil, fmt.Errorf("error reading service account file: %w", err)
	}

	secret, err := corev1.NewSecret(
		ctx,
		entry.Name,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(entry.Name),
				Namespace: namespace.ToStringPtrOutput(),
			},
			StringData: pulumi.StringMap{
				"resticPass": pulumi.String(entry.ResticPass),
				"gcsProject": pulumi.String(entry.GcsProject),
				entry.ServiceAccount.Keyname: pulumi.String(
					string(serviceAccountContent),
				),
			},
		},
		pulumi.DependsOn([]pulumi.Resource{probe}),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating backup secret %s: %w", entry.Name, err)
	}
	return secret, nil
}

func Run(ctx *pulumi.Context) error {
	backupConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	backupSecrets := NewBackupSecrets(backupConfig)
	return backupSecrets.Install(ctx)
}

func main() {
	pulumi.Run(Run)
}
