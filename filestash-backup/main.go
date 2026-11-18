package main

import (
	"fmt"
	"os"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	cronJobName     = "filestash-state-backup"
	nameLabel       = "app.kubernetes.io/name"
	componentLabel  = "app.kubernetes.io/component"
	appName         = "filestash"
	component       = "state-backup"
	stateClaim      = "filestash-state"
	filestashUID    = 1000
	credentialsPath = "/var/secret"
	activeDeadline  = 1800
	softDeleteSecs  = 5011200 // 58 days, as the other restic buckets
	newerVersions   = 3
	historyLimit    = 3
)

// Backup installs the GCS bucket, Secret filestash-backup-auth, and the
// state backup CronJob.
type Backup struct {
	Config *BackupConfig
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}
	return NewBackup(cfg).Install(ctx)
}

func NewBackup(cfg *BackupConfig) *Backup {
	cfg.ApplyDefaults()
	return &Backup{Config: cfg}
}

func (b *Backup) Install(ctx *pulumi.Context) error {
	bucket, err := b.createBucket(ctx)
	if err != nil {
		return err
	}
	secret, err := b.createSecret(ctx)
	if err != nil {
		return err
	}
	return b.createCronJob(ctx, bucket, secret)
}

func (b *Backup) createBucket(ctx *pulumi.Context) (*storage.Bucket, error) {
	name := b.Config.Bucket
	bucket, err := storage.NewBucket(ctx, name, &storage.BucketArgs{
		Name:       pulumi.String(name),
		Location:   pulumi.String("US"),
		Versioning: &storage.BucketVersioningArgs{Enabled: pulumi.Bool(true)},
		SoftDeletePolicy: &storage.BucketSoftDeletePolicyArgs{
			RetentionDurationSeconds: pulumi.Int(softDeleteSecs),
		},
		LifecycleRules: storage.BucketLifecycleRuleArray{
			&storage.BucketLifecycleRuleArgs{
				Action: &storage.BucketLifecycleRuleActionArgs{Type: pulumi.String("Delete")},
				Condition: &storage.BucketLifecycleRuleConditionArgs{
					WithState:        pulumi.String("ARCHIVED"),
					NumNewerVersions: pulumi.Int(newerVersions),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating GCS bucket %s: %w", name, err)
	}
	ctx.Export("bucketName", bucket.Name)
	return bucket, nil
}

func (b *Backup) createSecret(ctx *pulumi.Context) (*corev1.Secret, error) {
	sec := b.Config.Secret
	key, err := os.ReadFile(sec.ServiceAccountFile)
	if err != nil {
		return nil, fmt.Errorf("error reading the backup service account key: %w", err)
	}
	// ToSecret keeps the StringMap type: the assertion cannot fail.
	data := pulumi.ToSecret(pulumi.StringMap{ //nolint:errcheck // see above
		"resticPass":     pulumi.String(sec.ResticPass),
		"gcsProject":     pulumi.String(sec.GcsProject),
		"gcsCredentials": pulumi.String(string(key)),
	}).(pulumi.StringMapOutput)
	secret, err := corev1.NewSecret(ctx, sec.Name, &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(sec.Name),
			Namespace: pulumi.String(b.Config.Namespace),
			Labels:    pulumi.StringMap{nameLabel: pulumi.String(appName)},
		},
		StringData: data,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating secret %s: %w", sec.Name, err)
	}
	return secret, nil
}

func (b *Backup) createCronJob(ctx *pulumi.Context, deps ...pulumi.Resource) error {
	cfg := b.Config
	labels := pulumi.StringMap{componentLabel: pulumi.String(component)}
	_, err := batchv1.NewCronJob(ctx, cronJobName, &batchv1.CronJobArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(cronJobName),
			Namespace: pulumi.String(cfg.Namespace),
			Labels:    labels,
		},
		Spec: &batchv1.CronJobSpecArgs{
			Schedule:                   pulumi.String(cfg.Schedule),
			ConcurrencyPolicy:          pulumi.String("Forbid"),
			SuccessfulJobsHistoryLimit: pulumi.Int(historyLimit),
			FailedJobsHistoryLimit:     pulumi.Int(historyLimit),
			JobTemplate: &batchv1.JobTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{Labels: labels},
				Spec: &batchv1.JobSpecArgs{
					BackoffLimit:          pulumi.Int(0),
					ActiveDeadlineSeconds: pulumi.Int(activeDeadline),
					Template: &corev1.PodTemplateSpecArgs{
						Metadata: &metav1.ObjectMetaArgs{Labels: labels},
						Spec:     b.podSpec(),
					},
				},
			},
		},
	}, pulumi.DependsOn(deps))
	if err != nil {
		return fmt.Errorf("error creating cronjob %s: %w", cronJobName, err)
	}
	return nil
}

// podSpec runs on the node of the Filestash pod, because the state PVC is
// ReadWriteOnce. Filestash keeps running: the copy uses sqlite3 .backup.
func (b *Backup) podSpec() *corev1.PodSpecArgs {
	cfg := b.Config
	return &corev1.PodSpecArgs{
		RestartPolicy: pulumi.String("Never"),
		SecurityContext: &corev1.PodSecurityContextArgs{
			RunAsNonRoot: pulumi.Bool(true),
			RunAsUser:    pulumi.Int(filestashUID),
			RunAsGroup:   pulumi.Int(filestashUID),
			FsGroup:      pulumi.Int(filestashUID),
		},
		Affinity: &corev1.AffinityArgs{
			PodAffinity: &corev1.PodAffinityArgs{
				RequiredDuringSchedulingIgnoredDuringExecution: corev1.PodAffinityTermArray{
					&corev1.PodAffinityTermArgs{
						TopologyKey: pulumi.String("kubernetes.io/hostname"),
						LabelSelector: &metav1.LabelSelectorArgs{
							MatchLabels: pulumi.StringMap{nameLabel: pulumi.String(appName)},
						},
					},
				},
			},
		},
		InitContainers: corev1.ContainerArray{
			&corev1.ContainerArgs{
				Name:    pulumi.String("copy"),
				Image:   pulumi.String(cfg.SqliteImage),
				Command: pulumi.StringArray{pulumi.String("sh"), pulumi.String("-c"), pulumi.String(copyScript)},
				VolumeMounts: corev1.VolumeMountArray{
					mount("state", statePath, false),
					mount("scratch", backupPath, false),
				},
			},
		},
		Containers: corev1.ContainerArray{
			&corev1.ContainerArgs{
				Name:    pulumi.String("restic"),
				Image:   pulumi.String(cfg.ResticImage),
				Command: pulumi.StringArray{pulumi.String("sh"), pulumi.String("-c"), pulumi.String(resticScript(cfg))},
				Env:     b.resticEnv(),
				VolumeMounts: corev1.VolumeMountArray{
					mount("scratch", backupPath, true),
					mount("gcs-credentials", credentialsPath, true),
				},
			},
		},
		Volumes: corev1.VolumeArray{
			&corev1.VolumeArgs{
				Name: pulumi.String("state"),
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSourceArgs{
					ClaimName: pulumi.String(stateClaim),
				},
			},
			&corev1.VolumeArgs{Name: pulumi.String("scratch"), EmptyDir: &corev1.EmptyDirVolumeSourceArgs{}},
			&corev1.VolumeArgs{
				Name: pulumi.String("gcs-credentials"),
				Secret: &corev1.SecretVolumeSourceArgs{
					SecretName: pulumi.String(cfg.Secret.Name),
					Items: corev1.KeyToPathArray{
						&corev1.KeyToPathArgs{Key: pulumi.String("gcsCredentials"), Path: pulumi.String("gcs-credentials")},
					},
				},
			},
		},
	}
}

func (b *Backup) resticEnv() corev1.EnvVarArray {
	sec := b.Config.Secret.Name
	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name:  pulumi.String("RESTIC_REPOSITORY"),
			Value: pulumi.String(fmt.Sprintf("gs:%s:/", b.Config.Bucket)),
		},
		secretEnv("RESTIC_PASSWORD", sec, "resticPass"),
		secretEnv("GOOGLE_PROJECT_ID", sec, "gcsProject"),
		&corev1.EnvVarArgs{
			Name:  pulumi.String("GOOGLE_APPLICATION_CREDENTIALS"),
			Value: pulumi.String(credentialsPath + "/gcs-credentials"),
		},
		&corev1.EnvVarArgs{Name: pulumi.String("RESTIC_CACHE_DIR"), Value: pulumi.String("/tmp/restic-cache")},
	}
}

func secretEnv(name, secret, key string) *corev1.EnvVarArgs {
	return &corev1.EnvVarArgs{
		Name: pulumi.String(name),
		ValueFrom: &corev1.EnvVarSourceArgs{
			SecretKeyRef: &corev1.SecretKeySelectorArgs{Name: pulumi.String(secret), Key: pulumi.String(key)},
		},
	}
}

func mount(name, path string, readOnly bool) *corev1.VolumeMountArgs {
	return &corev1.VolumeMountArgs{
		Name:      pulumi.String(name),
		MountPath: pulumi.String(path),
		ReadOnly:  pulumi.Bool(readOnly),
	}
}
