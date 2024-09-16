package main

import (
	"fmt"
	"os"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (prop *Properties) CreateUserSecret(
	ctx *pulumi.Context,
	cluster Cluster,
) (*corev1.Secret, error) {
	secret, err := corev1.NewSecret(ctx,
		cluster.Bootstrap.UserSecret.Name,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(
					cluster.Bootstrap.UserSecret.Name,
				),
				Namespace: pulumi.String(cluster.Namespace),
			},
			Type: pulumi.String("kubernetes.io/basic-auth"),
			StringData: pulumi.StringMap{
				"username": pulumi.String(cluster.Bootstrap.Owner),
				"password": pulumi.String(
					cluster.Bootstrap.UserSecret.Password,
				),
			},
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create basic auth secret: %w", err)
	}

	return secret, nil
}

func (prop *Properties) CreateBackupSecret(
	ctx *pulumi.Context,
	cluster Cluster,
) (*corev1.Secret, error) {
	// Read the file content
	fileContent, err := os.ReadFile(prop.BackupSecret.Filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Create the secret
	secret, err := corev1.NewSecret(
		ctx,
		prop.BackupSecret.Name,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(prop.BackupSecret.Name),
				Namespace: pulumi.String(cluster.Namespace),
			},
			StringData: pulumi.StringMap{
				prop.BackupSecret.Key: pulumi.String(string(fileContent)),
			},
		},
	)
	if err != nil {
		return nil, err
	}

	return secret, nil
}

func createBackupGCSBucket(
	ctx *pulumi.Context,
	name string,
	location string,
) (*storage.Bucket, error) {
	bucket, err := storage.NewBucket(ctx, name, &storage.BucketArgs{
		Name:     pulumi.String(name),
		Location: pulumi.String(location),
		Versioning: &storage.BucketVersioningArgs{
			Enabled: pulumi.Bool(true),
		},
		LifecycleRules: storage.BucketLifecycleRuleArray{
			&storage.BucketLifecycleRuleArgs{
				Action: &storage.BucketLifecycleRuleActionArgs{
					Type: pulumi.String("Delete"),
				},
				Condition: &storage.BucketLifecycleRuleConditionArgs{
					Age:              pulumi.Int(90),
					NumNewerVersions: pulumi.Int(1),
				},
			},
		},
		SoftDeletePolicy: &storage.BucketSoftDeletePolicyArgs{
			RetentionDurationSeconds: pulumi.Int(
				30 * 24 * 60 * 60,
			), // 30 days in seconds
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating GCS bucket: %v", err)
	}

	return bucket, nil
}
