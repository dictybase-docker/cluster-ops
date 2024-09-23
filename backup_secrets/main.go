package main

import (
	"fmt"
	"os"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type BackupSecretsConfig struct {
	Secret struct {
		Name       string
		ResticPass string
		ServiceAccount struct {
			Filepath string
			Keyname  string
			Key      string
		}
	}
	Namespace string
}

type BackupSecrets struct {
	Config *BackupSecretsConfig
}

func ReadConfig(ctx *pulumi.Context) (*BackupSecretsConfig, error) {
	conf := config.New(ctx, "backup_secrets")
	backupConfig := &BackupSecretsConfig{}
	if err := conf.TryObject("properties", backupConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read backup_secrets config: %w",
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
	// Read the content of the file specified by Filepath
	serviceAccountContent, err := os.ReadFile(bsr.Config.Secret.ServiceAccount.Filepath)
	if err != nil {
		return fmt.Errorf("error reading service account file: %w", err)
	}

	secret, err := corev1.NewSecret(
		ctx,
		bsr.Config.Secret.Name,
		&corev1.SecretArgs{
			Metadata: bsr.createMetadata(),
			StringData: pulumi.StringMap{
				"resticPass":                     pulumi.String(bsr.Config.Secret.ResticPass),
				bsr.Config.Secret.ServiceAccount.Keyname: pulumi.String(string(serviceAccountContent)),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("error creating backup secret: %w", err)
	}

	ctx.Export("secretName", secret.Metadata.Name())
	return nil
}

func (bsr *BackupSecrets) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(bsr.Config.Secret.Name),
		Namespace: pulumi.String(bsr.Config.Namespace),
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
