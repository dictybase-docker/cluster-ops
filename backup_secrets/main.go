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
	Namespace      string
	ExtraNamespace string
	Secret         struct {
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
	// Create main namespace
	namespace, err := corev1.NewNamespace(
		ctx,
		bsr.Config.Namespace,
		&corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(bsr.Config.Namespace),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("error creating main namespace: %w", err)
	}

	// Create extra namespace
	extraNamespace, err := corev1.NewNamespace(
		ctx,
		bsr.Config.ExtraNamespace,
		&corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(bsr.Config.ExtraNamespace),
			},
		},
	)
	if err != nil {
		return fmt.Errorf("error creating extra namespace: %w", err)
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
			Metadata: bsr.createMetadata(),
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
		pulumi.DependsOn([]pulumi.Resource{namespace, extraNamespace}),
	)
	if err != nil {
		return fmt.Errorf("error creating backup secret: %w", err)
	}

	ctx.Export("secretName", secret.Metadata.Name())
	ctx.Export("namespaceName", namespace.Metadata.Name())
	ctx.Export("extraNamespaceName", extraNamespace.Metadata.Name())
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
