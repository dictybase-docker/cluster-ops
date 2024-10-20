package main

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type VeleroConfig struct {
	Bucket             string
	Namespace          string
	Plugins            []string
	Provider           string
	ServiceAccountJSON string
	Schedule           struct {
		Name string
		Run  string
		TTL  string
	}
}

type Velero struct {
	Config *VeleroConfig
}

func ReadConfig(ctx *pulumi.Context) (*VeleroConfig, error) {
	conf := config.New(ctx, "")
	veleroConfig := &VeleroConfig{}
	err := conf.TryObject("properties", veleroConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read velero config: %w", err)
	}
	return veleroConfig, nil
}

func NewVelero(config *VeleroConfig) *Velero {
	return &Velero{
		Config: config,
	}
}

func (vel *Velero) Install(ctx *pulumi.Context) error {
	bucket, err := vel.createGCSBucket(ctx)
	if err != nil {
		return err
	}

	installCommand, err := vel.runVeleroInstallCommand(ctx, bucket)
	if err != nil {
		return err
	}

	scheduleCommand, err := vel.createVeleroSchedule(ctx, installCommand)
	if err != nil {
		return err
	}

	err = vel.createImmediateBackup(ctx, scheduleCommand)
	if err != nil {
		return err
	}

	return nil
}

func (vel *Velero) createVeleroSchedule(
	ctx *pulumi.Context,
	installCommand *local.Command,
) (*local.Command, error) {
	createCommand := fmt.Sprintf(
		"velero schedule create %s --schedule=\"%s\" --ttl %s --exclude-resources cronjobs.batch,jobs.batch",
		vel.Config.Schedule.Name,
		vel.Config.Schedule.Run,
		vel.Config.Schedule.TTL,
	)

	deleteCommand := fmt.Sprintf(
		"velero schedule delete %s --confirm",
		vel.Config.Schedule.Name,
	)

	cmd, err := local.NewCommand(ctx, "velero-schedule", &local.CommandArgs{
		Create: pulumi.String(createCommand),
		Delete: pulumi.String(deleteCommand),
	}, pulumi.DependsOn([]pulumi.Resource{installCommand}))
	if err != nil {
		return nil, fmt.Errorf("error creating Velero schedule: %w", err)
	}

	return cmd, nil
}

func (vel *Velero) runVeleroInstallCommand(
	ctx *pulumi.Context,
	bucket *storage.Bucket,
) (*local.Command, error) {
	plugins := strings.Join(vel.Config.Plugins, ",")
	installCommand := fmt.Sprintf(
		"velero install --provider %s --plugins %s --bucket %s --secret-file %s --namespace %s --wait",
		vel.Config.Provider,
		plugins,
		vel.Config.Bucket,
		vel.Config.ServiceAccountJSON,
		vel.Config.Namespace,
	)

	uninstallCommand := fmt.Sprintf(
		"velero uninstall --namespace %s --force",
		vel.Config.Namespace,
	)

	cmd, err := local.NewCommand(ctx, "velero-install", &local.CommandArgs{
		Create: pulumi.String(installCommand),
		Delete: pulumi.String(uninstallCommand),
	}, pulumi.DependsOn([]pulumi.Resource{bucket}))
	if err != nil {
		return nil, fmt.Errorf("error running Velero install command: %w", err)
	}

	return cmd, nil
}

func (vel *Velero) createGCSBucket(
	ctx *pulumi.Context,
) (*storage.Bucket, error) {
	bucket, err := storage.NewBucket(
		ctx,
		vel.Config.Bucket,
		&storage.BucketArgs{
			Name:         pulumi.String(vel.Config.Bucket),
			Location:     pulumi.String("US"),
			ForceDestroy: pulumi.Bool(true),
			Versioning: &storage.BucketVersioningArgs{
				Enabled: pulumi.Bool(true),
			},
			LifecycleRules: &storage.BucketLifecycleRuleArray{
				&storage.BucketLifecycleRuleArgs{
					Action: &storage.BucketLifecycleRuleActionArgs{
						Type: pulumi.String("Delete"),
					},
					Condition: &storage.BucketLifecycleRuleConditionArgs{
						WithState:        pulumi.String("ARCHIVED"),
						NumNewerVersions: pulumi.Int(2),
					},
				},
			},
			SoftDeletePolicy: &storage.BucketSoftDeletePolicyArgs{
				RetentionDurationSeconds: pulumi.Int(
					3456000,
				), // 40 days in seconds
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating GCS bucket: %w", err)
	}
	return bucket, nil
}

func (vel *Velero) createImmediateBackup(
	ctx *pulumi.Context,
	scheduleCommand *local.Command,
) error {
	backupName := fmt.Sprintf("%s-initial", vel.Config.Schedule.Name)
	command := fmt.Sprintf(
		"velero backup create %s --from-schedule=%s",
		backupName,
		vel.Config.Schedule.Name,
	)

	_, err := local.NewCommand(
		ctx,
		"velero-immediate-backup",
		&local.CommandArgs{
			Create: pulumi.String(command),
		},
		pulumi.DependsOn([]pulumi.Resource{scheduleCommand}),
	)
	if err != nil {
		return fmt.Errorf("error creating immediate Velero backup: %w", err)
	}

	return nil
}

func Run(ctx *pulumi.Context) error {
	veleroConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	velero := NewVelero(veleroConfig)

	err = velero.Install(ctx)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
