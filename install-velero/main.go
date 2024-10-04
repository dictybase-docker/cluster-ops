package main

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type VeleroConfig struct {
	Bucket    string
	Namespace string
	Plugins   []string
	Provider  string
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
	_, err := vel.createGCSBucket(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (vel *Velero) createGCSBucket(ctx *pulumi.Context) (*storage.Bucket, error) {
	bucket, err := storage.NewBucket(ctx, vel.Config.Bucket, &storage.BucketArgs{
		Name: pulumi.String(vel.Config.Bucket),
		RetentionPolicy: &storage.BucketRetentionPolicyArgs{
			RetentionPeriod: pulumi.Int(28 * 24 * 60 * 60), // 28 days in seconds
		},
		Versioning: &storage.BucketVersioningArgs{
			Enabled: pulumi.Bool(false),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating GCS bucket: %w", err)
	}
	return bucket, nil
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
