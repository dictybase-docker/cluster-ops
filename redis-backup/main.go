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

type RedisBackupConfig struct {
	Bucket       string
	Namespace    string
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
	Server string
	Image  struct {
		Name string
		Tag  string
	}
}

type RedisBackup struct {
	Config *RedisBackupConfig
}

func ReadConfig(ctx *pulumi.Context) (*RedisBackupConfig, error) {
	conf := config.New(ctx, "")
	backupConfig := &RedisBackupConfig{}
	if err := conf.TryObject("properties", backupConfig); err != nil {
		return nil, fmt.Errorf("failed to read redis-backup config: %w", err)
	}
	return backupConfig, nil
}

func NewRedisBackup(config *RedisBackupConfig) *RedisBackup {
	return &RedisBackup{
		Config: config,
	}
}

func (rb *RedisBackup) Install(ctx *pulumi.Context) error {
	bucket, err := rb.createGCSBucket(ctx)
	if err != nil {
		return err
	}

	if err := rb.createBackupCronJob(ctx, bucket); err != nil {
		return err
	}

	if err := rb.createImmediateBackupJob(ctx, bucket); err != nil {
		return err
	}

	return nil
}

func (rb *RedisBackup) createBackupCronJob(
	ctx *pulumi.Context,
	bucket *storage.Bucket,
) error {
	cronJobName := "redis-backup-cronjob"
	cronJobArgs := &batchv1.CronJobArgs{
		Metadata: rb.createCronJobMetadata(cronJobName),
		Spec:     rb.createCronJobSpec(bucket),
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

func (rb *RedisBackup) createImmediateBackupJob(
	ctx *pulumi.Context,
	bucket *storage.Bucket,
) error {
	jobName := "redis-immediate-backup-job"
	jobArgs := &batchv1.JobArgs{
		Metadata: rb.createJobMetadata(jobName),
		Spec:     rb.createJobSpec(bucket, true), // Pass true for immediate backup
	}

	_, err := batchv1.NewJob(
		ctx,
		jobName,
		jobArgs,
		pulumi.DependsOn([]pulumi.Resource{bucket}),
	)
	if err != nil {
		return fmt.Errorf(
			"error creating Kubernetes immediate backup Job: %w",
			err,
		)
	}
	return nil
}

func (rb *RedisBackup) createJobMetadata(
	name string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(rb.Config.Namespace),
	}
}

func (rb *RedisBackup) createJobSpec(
	bucket *storage.Bucket,
	isImmediateBackup bool,
) *batchv1.JobSpecArgs {
	jobSpec := &batchv1.JobSpecArgs{
		Template:     rb.createPodTemplateSpec(bucket),
		BackoffLimit: pulumi.Int(0),
	}

	if isImmediateBackup {
		jobSpec.TtlSecondsAfterFinished = pulumi.Int(900) // 15 minutes
	}

	return jobSpec
}

func (rb *RedisBackup) createGCSBucket(
	ctx *pulumi.Context,
) (*storage.Bucket, error) {
	bucket, err := storage.NewBucket(ctx, rb.Config.Bucket, &storage.BucketArgs{
		Name:           pulumi.String(rb.Config.Bucket),
		Location:       pulumi.String("US"),
		LifecycleRules: rb.createLifecycleRules(),
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

func (rb *RedisBackup) createLifecycleRules() storage.BucketLifecycleRuleArray {
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

func (rb *RedisBackup) createCronJobMetadata(
	name string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(rb.Config.Namespace),
	}
}

func (rb *RedisBackup) createCronJobSpec(
	bucket *storage.Bucket,
) *batchv1.CronJobSpecArgs {
	return &batchv1.CronJobSpecArgs{
		Schedule: pulumi.String(
			"0 1 * * *",
		), // Run at 1AM every night (1 hour before ArangoDB backup)
		JobTemplate: &batchv1.JobTemplateSpecArgs{
			Spec: rb.createJobSpec(bucket, false), // Pass false for cron job
		},
	}
}

func (rb *RedisBackup) createPodTemplateSpec(
	bucket *storage.Bucket,
) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Spec: &corev1.PodSpecArgs{
			Containers: corev1.ContainerArray{
				rb.createBackupContainer(bucket),
			},
			RestartPolicy: pulumi.String("Never"),
			Volumes: corev1.VolumeArray{
				rb.createGCSCredentialsVolume(),
			},
		},
	}
}

func (rb *RedisBackup) createGCSCredentialsVolume() *corev1.VolumeArgs {
	return &corev1.VolumeArgs{
		Name: pulumi.String("gcs-credentials"),
		Secret: &corev1.SecretVolumeSourceArgs{
			SecretName: pulumi.String(rb.Config.BucketSecret.Name),
			Items: corev1.KeyToPathArray{
				&corev1.KeyToPathArgs{
					Key:  pulumi.String(rb.Config.BucketSecret.Key),
					Path: pulumi.String("gcs-credentials"),
				},
			},
		},
	}
}

func (rb *RedisBackup) createBackupContainer(
	bucket *storage.Bucket,
) *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name: pulumi.String("backup"),
		Image: pulumi.Sprintf(
			"%s:%s",
			rb.Config.Image.Name,
			rb.Config.Image.Tag,
		),
		Command: pulumi.StringArray{
			pulumi.String("app"),
		},
		Args: rb.createBackupArgs(bucket),
		Env:  rb.createBackupEnv(),
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name:      pulumi.String("gcs-credentials"),
				MountPath: pulumi.String("/var/secret"),
				ReadOnly:  pulumi.Bool(true),
			},
		},
	}
}

func (rb *RedisBackup) createBackupArgs(
	bucket *storage.Bucket,
) pulumi.StringArray {
	return pulumi.StringArray{
		pulumi.String("redis-backup"),
		pulumi.String("--host"), pulumi.String(rb.Config.Server),
		pulumi.String("--repository"), pulumi.Sprintf("gs:%s:/", bucket.Name),
	}
}

func (rb *RedisBackup) createBackupEnv() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name: pulumi.String("RESTIC_PASSWORD"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(rb.Config.ResticSecret.Name),
					Key:  pulumi.String(rb.Config.ResticSecret.Key),
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
					Name: pulumi.String(rb.Config.ProjectSecret.Name),
					Key:  pulumi.String(rb.Config.ProjectSecret.Key),
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

	redisBackup := NewRedisBackup(backupConfig)

	if err := redisBackup.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
