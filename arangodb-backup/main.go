package main

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ArangoBackupConfig struct {
	Bucket       string
	Folder       string
	Namespace    string
	Secret       string
	ResticSecret struct {
		Name string
		Key  string
	}
	BucketSecret struct {
		Name string
		Key  string
	}
	ProjectSecret struct {
		Name string
		Key  string
	}
	Server  string
	User    string
	Storage struct {
		Class string
		Name  string
		Size  string
	}
	Image struct {
		Name string
		Tag  string
	}
}

type ArangoBackup struct {
	Config *ArangoBackupConfig
}

func ReadConfig(ctx *pulumi.Context) (*ArangoBackupConfig, error) {
	conf := config.New(ctx, "")
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
	bucket, err := ab.createGCSBucket(ctx)
	if err != nil {
		return err
	}

	if err := ab.createBackupCronJob(ctx, bucket); err != nil {
		return err
	}

	return nil
}

func (ab *ArangoBackup) createGCSBucket(
	ctx *pulumi.Context,
) (*storage.Bucket, error) {
	bucket, err := storage.NewBucket(ctx, ab.Config.Bucket, &storage.BucketArgs{
		Name:           pulumi.String(ab.Config.Bucket),
		Location:       pulumi.String("US"),
		LifecycleRules: ab.createLifecycleRules(),
		Versioning: &storage.BucketVersioningArgs{
			Enabled: pulumi.Bool(true),
		},
		SoftDeletePolicy: &storage.BucketSoftDeletePolicyArgs{
			RetentionDurationSeconds: pulumi.Int(5011200), // 58 days in seconds
		},
		ForceDestroy: pulumi.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("error creating GCS bucket: %w", err)
	}

	ctx.Export("bucketName", bucket.Name)
	ctx.Export("bucketUrl", bucket.Url)
	return bucket, nil
}

func (ab *ArangoBackup) createLifecycleRules() storage.BucketLifecycleRuleArray {
	return storage.BucketLifecycleRuleArray{
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
				WithState:        pulumi.String("ARCHIVED"),
				NumNewerVersions: pulumi.Int(3),
			},
		},
	}
}

func (ab *ArangoBackup) createBackupCronJob(
	ctx *pulumi.Context,
	bucket *storage.Bucket,
) error {
	cronJobName := "arangodb-backup-cronjob"
	cronJobArgs := &batchv1.CronJobArgs{
		Metadata: ab.createCronJobMetadata(cronJobName),
		Spec:     ab.createCronJobSpec(bucket),
	}

	_, err := batchv1.NewCronJob(
		ctx,
		cronJobName,
		cronJobArgs,
		pulumi.DependsOn([]pulumi.Resource{bucket}),
	)
	if err != nil {
		return fmt.Errorf("error creating Kubernetes CronJob: %w", err)
	}
	return nil
}

func (ab *ArangoBackup) createCronJobMetadata(
	name string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(ab.Config.Namespace),
	}
}

func (ab *ArangoBackup) createCronJobSpec(
	bucket *storage.Bucket,
) *batchv1.CronJobSpecArgs {
	return &batchv1.CronJobSpecArgs{
		Schedule:    pulumi.String("0 2 * * *"), // Run at 2AM every night
		JobTemplate: ab.createJobTemplateSpec(bucket),
	}
}

func (ab *ArangoBackup) createJobTemplateSpec(
	bucket *storage.Bucket,
) *batchv1.JobTemplateSpecArgs {
	return &batchv1.JobTemplateSpecArgs{
		Spec: &batchv1.JobSpecArgs{
			Template: ab.createPodTemplateSpec(bucket),
		},
	}
}

func (ab *ArangoBackup) createPodTemplateSpec(
	bucket *storage.Bucket,
) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Spec: &corev1.PodSpecArgs{
			Containers: corev1.ContainerArray{
				ab.createBackupContainer(bucket),
			},
			RestartPolicy: pulumi.String("Never"),
			Volumes: corev1.VolumeArray{
				ab.createBackupVolume(),
				ab.createGCSCredentialsVolume(),
			},
		},
	}
}

func (ab *ArangoBackup) createGCSCredentialsVolume() *corev1.VolumeArgs {
	return &corev1.VolumeArgs{
		Name: pulumi.String("gcs-credentials"),
		Secret: &corev1.SecretVolumeSourceArgs{
			SecretName: pulumi.String(ab.Config.BucketSecret.Name),
			Items: corev1.KeyToPathArray{
				&corev1.KeyToPathArgs{
					Key:  pulumi.String(ab.Config.BucketSecret.Key),
					Path: pulumi.String("gcs-credentials"),
				},
			},
		},
	}
}

func (ab *ArangoBackup) createBackupVolume() *corev1.VolumeArgs {
	return &corev1.VolumeArgs{
		Name: pulumi.String(ab.Config.Storage.Name),
		Ephemeral: &corev1.EphemeralVolumeSourceArgs{
			VolumeClaimTemplate: ab.createVolumeClaimTemplate(),
		},
	}
}

func (ab *ArangoBackup) createVolumeClaimTemplate() *corev1.PersistentVolumeClaimTemplateArgs {
	return &corev1.PersistentVolumeClaimTemplateArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Labels: pulumi.StringMap{
				"type": pulumi.String("arangodb-backup-ephemeral"),
			},
		},
		Spec: ab.createPersistentVolumeClaimSpec(),
	}
}

func (ab *ArangoBackup) createPersistentVolumeClaimSpec() *corev1.PersistentVolumeClaimSpecArgs {
	return &corev1.PersistentVolumeClaimSpecArgs{
		AccessModes:      pulumi.StringArray{pulumi.String("ReadWriteOnce")},
		StorageClassName: pulumi.String(ab.Config.Storage.Class),
		Resources: &corev1.VolumeResourceRequirementsArgs{
			Requests: pulumi.StringMap{
				"storage": pulumi.String(ab.Config.Storage.Size),
			},
		},
	}
}

func (ab *ArangoBackup) createBackupContainer(
	bucket *storage.Bucket,
) *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name: pulumi.String("backup"),
		Image: pulumi.Sprintf(
			"%s:%s",
			ab.Config.Image.Name,
			ab.Config.Image.Tag,
		),
		Command: pulumi.StringArray{
			pulumi.String("app"),
		},
		Args: ab.createBackupArgs(bucket),
		Env:  ab.createBackupEnv(),
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name:      pulumi.String(ab.Config.Storage.Name),
				MountPath: pulumi.String(ab.Config.Folder),
			},
			&corev1.VolumeMountArgs{
				Name:      pulumi.String("gcs-credentials"),
				MountPath: pulumi.String("/var/secret"),
				ReadOnly:  pulumi.Bool(true),
			},
		},
	}
}

func (ab *ArangoBackup) createBackupArgs(
	bucket *storage.Bucket,
) pulumi.StringArray {
	return pulumi.StringArray{
		pulumi.String("arangodb-backup"),
		pulumi.String("--user"), pulumi.String(ab.Config.User),
		pulumi.String("--password"), pulumi.String("$(PASSWORD)"),
		pulumi.String("--server"), pulumi.String(ab.Config.Server),
		pulumi.String("--output"), pulumi.String(ab.Config.Folder),
		pulumi.String("--repository"), pulumi.Sprintf("gs:%s:/", bucket.Name),
	}
}

func (ab *ArangoBackup) createBackupEnv() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name: pulumi.String("PASSWORD"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(ab.Config.Secret),
					Key:  pulumi.String("password"),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("RESTIC_PASSWORD"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(ab.Config.ResticSecret.Name),
					Key:  pulumi.String(ab.Config.ResticSecret.Key),
				},
			},
		},
		&corev1.EnvVarArgs{
			Name:  pulumi.String("GOOGLE_APPLICATION_CREDENTIALS"),
			Value: pulumi.String("/var/secret/gcs-credentials"),
		},
		&corev1.EnvVarArgs{
			Name: pulumi.String("GOOGLE_PROJECT_ID"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(ab.Config.ProjectSecret.Name),
					Key:  pulumi.String(ab.Config.ProjectSecret.Key),
				},
			},
		},
	}
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
