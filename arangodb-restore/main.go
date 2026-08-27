package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func ReadConfig(ctx *pulumi.Context) (*RestoreConfig, error) {
	conf := config.New(ctx, "")
	cfg := &RestoreConfig{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read arangodb-restore config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid arangodb-restore config: %w", err)
	}
	return cfg, nil
}

type ArangoRestore struct {
	Config *RestoreConfig
}

func NewArangoRestore(cfg *RestoreConfig) *ArangoRestore {
	return &ArangoRestore{Config: cfg}
}

func (ar *ArangoRestore) Install(ctx *pulumi.Context) error {
	job, err := ar.createRestoreJob(ctx)
	if err != nil {
		return err
	}

	ctx.Export("jobName", job.Metadata.Name())
	ctx.Export("targetIdentity", pulumi.String(ar.Config.targetIdentity()))
	return nil
}

func (ar *ArangoRestore) createRestoreJob(
	ctx *pulumi.Context,
) (*batchv1.Job, error) {
	cfg := ar.Config
	name := cfg.jobName()

	job, err := batchv1.NewJob(ctx, name, &batchv1.JobArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(cfg.Namespace),
			Labels: pulumi.StringMap{
				"app":        pulumi.String("arangodb-restore"),
				"restore-id": pulumi.String(cfg.RestoreID),
			},
		},
		Spec: &batchv1.JobSpecArgs{
			BackoffLimit: pulumi.Int(0),
			// 1 hour, not arangodb-backup's 15 minutes: a restore is a rare,
			// surgical operation an operator wants to inspect (logs, exit
			// codes) after it finishes, not a routine nightly run.
			TtlSecondsAfterFinished: pulumi.Int(3600),
			Template:                ar.createPodTemplateSpec(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating arangodb-restore job: %w", err)
	}
	return job, nil
}

func (ar *ArangoRestore) createPodTemplateSpec() *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Spec: &corev1.PodSpecArgs{
			RestartPolicy: pulumi.String("Never"),
			InitContainers: corev1.ContainerArray{
				ar.createResticContainer(),
			},
			Containers: corev1.ContainerArray{
				ar.createArangorestoreContainer(),
			},
			Volumes: corev1.VolumeArray{
				ar.createScratchVolume(),
				ar.createGCSCredentialsVolume(),
			},
		},
	}
}

// createScratchVolume is a generic ephemeral volume, matching
// arangodb-backup's pattern: created fresh per Job run, deleted with the
// pod, never a long-lived PVC.
func (ar *ArangoRestore) createScratchVolume() *corev1.VolumeArgs {
	cfg := ar.Config
	return &corev1.VolumeArgs{
		Name: pulumi.String(cfg.Storage.Name),
		Ephemeral: &corev1.EphemeralVolumeSourceArgs{
			VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplateArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Labels: pulumi.StringMap{
						"type": pulumi.String("arangodb-restore-scratch"),
					},
				},
				Spec: &corev1.PersistentVolumeClaimSpecArgs{
					AccessModes: pulumi.StringArray{
						pulumi.String("ReadWriteOnce"),
					},
					StorageClassName: pulumi.String(cfg.Storage.Class),
					Resources: &corev1.VolumeResourceRequirementsArgs{
						Requests: pulumi.StringMap{
							"storage": pulumi.String(cfg.Storage.Size),
						},
					},
				},
			},
		},
	}
}

func (ar *ArangoRestore) createGCSCredentialsVolume() *corev1.VolumeArgs {
	cfg := ar.Config
	return &corev1.VolumeArgs{
		Name: pulumi.String("gcs-credentials"),
		Secret: &corev1.SecretVolumeSourceArgs{
			SecretName: pulumi.String(cfg.BucketSecret.Name),
			Items: corev1.KeyToPathArray{
				&corev1.KeyToPathArgs{
					Key:  pulumi.String(cfg.BucketSecret.Key),
					Path: pulumi.String("gcs-credentials"),
				},
			},
		},
	}
}

// createResticContainer restores the latest (or named) snapshot from GCS
// onto the scratch volume. No Command override: the restic/restic image's
// entrypoint.sh forwards Args to the restic binary directly.
func (ar *ArangoRestore) createResticContainer() *corev1.ContainerArgs {
	cfg := ar.Config
	return &corev1.ContainerArgs{
		Name:  pulumi.String(initContainerName),
		Image: pulumi.String(resticImageRef(cfg)),
		Args:  pulumi.ToStringArray(buildResticRestoreArgs(cfg)),
		Env: corev1.EnvVarArray{
			&corev1.EnvVarArgs{
				Name: pulumi.String("RESTIC_PASSWORD"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(cfg.ResticSecret.Name),
						Key:  pulumi.String(cfg.ResticSecret.Key),
					},
				},
			},
			&corev1.EnvVarArgs{
				Name: pulumi.String("GOOGLE_PROJECT_ID"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(cfg.ProjectSecret.Name),
						Key:  pulumi.String(cfg.ProjectSecret.Key),
					},
				},
			},
			&corev1.EnvVarArgs{
				Name:  pulumi.String("GOOGLE_APPLICATION_CREDENTIALS"),
				Value: pulumi.String("/var/secret/gcs-credentials"),
			},
		},
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name:      pulumi.String(cfg.Storage.Name),
				MountPath: pulumi.String(scratchMountPath),
			},
			&corev1.VolumeMountArgs{
				Name:      pulumi.String("gcs-credentials"),
				MountPath: pulumi.String("/var/secret"),
				ReadOnly:  pulumi.Bool(true),
			},
		},
	}
}

// createArangorestoreContainer runs after the restic init container
// completes. Command is overridden to "arangorestore" because the
// arangodb/arangodb image's default entrypoint starts an arangod server, not
// a restore client.
func (ar *ArangoRestore) createArangorestoreContainer() *corev1.ContainerArgs {
	cfg := ar.Config
	return &corev1.ContainerArgs{
		Name:    pulumi.String(mainContainerName),
		Image:   pulumi.String(arangoImageRef(cfg)),
		Command: pulumi.StringArray{pulumi.String("arangorestore")},
		Args:    pulumi.ToStringArray(buildArangorestoreArgs(cfg)),
		Env: corev1.EnvVarArray{
			&corev1.EnvVarArgs{
				Name: pulumi.String("ARANGO_PASSWORD"),
				ValueFrom: &corev1.EnvVarSourceArgs{
					SecretKeyRef: &corev1.SecretKeySelectorArgs{
						Name: pulumi.String(cfg.ArangodbSecret.Name),
						Key:  pulumi.String(cfg.ArangodbSecret.Key),
					},
				},
			},
		},
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name:      pulumi.String(cfg.Storage.Name),
				MountPath: pulumi.String(scratchMountPath),
				ReadOnly:  pulumi.Bool(true),
			},
		},
	}
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	ar := NewArangoRestore(cfg)
	return ar.Install(ctx)
}

func main() {
	pulumi.Run(Run)
}
