package main

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ArangoBackupConfig struct {
	Bucket    string
	Folder    string
	Namespace string
	Secret    struct {
		Value string
	}
	Server struct {
		Value string
	}
	User struct {
		Value string
	}
}

type ArangoBackup struct {
	Config *ArangoBackupConfig
}

func ReadConfig(ctx *pulumi.Context) (*ArangoBackupConfig, error) {
	conf := config.New(ctx, "arangodb-backup")
	backupConfig := &ArangoBackupConfig{}
	if err := conf.TryObject("properties", backupConfig); err != nil {
		return nil, fmt.Errorf("failed to read arangodb-backup config: %w", err)
	}
	return backupConfig, nil
}

func NewArangoBackup(config *ArangoBackupConfig) *ArangoBackup {
	return &ArangoBackup{
		Config: config,
	}
}

func (ab *ArangoBackup) Install(ctx *pulumi.Context) error {
	bucket, err := storage.NewBucket(ctx, ab.Config.Bucket, &storage.BucketArgs{
		Name:     pulumi.String(ab.Config.Bucket),
		Location: pulumi.String("US"),
		RetentionPolicy: &storage.BucketRetentionPolicyArgs{
			RetentionPeriod: pulumi.Int(
				60 * 24 * 60 * 60,
			), // 60 days in seconds
		},
		LifecycleRules: storage.BucketLifecycleRuleArray{
			&storage.BucketLifecycleRuleArgs{
				Action: &storage.BucketLifecycleRuleActionArgs{
					Type: pulumi.String("Delete"),
				},
				Condition: &storage.BucketLifecycleRuleConditionArgs{
					Age: pulumi.Int(65), // 65 days
				},
			},
			&storage.BucketLifecycleRuleArgs{
				Action: &storage.BucketLifecycleRuleActionArgs{
					Type: pulumi.String("Delete"),
				},
				Condition: &storage.BucketLifecycleRuleConditionArgs{
					NumNewerVersions: pulumi.Int(1),
				},
			},
		},
		Versioning: &storage.BucketVersioningArgs{
			Enabled: pulumi.Bool(false),
		},
	})
	if err != nil {
		return fmt.Errorf("error creating GCS bucket: %w", err)
	}

	ctx.Export("bucketName", bucket.Name)
	ctx.Export("bucketUrl", bucket.Url)

	return nil
}

func Run(ctx *pulumi.Context) error {
	backupConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	arangoBackup := NewArangoBackup(backupConfig)

	if err := arangoBackup.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
