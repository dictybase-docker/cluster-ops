package main

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	storagev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/storage/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type StorageClassConfig struct {
	Properties struct {
		DiskType    string
		Name        string
		Provisioner string
	}
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	storageConfig, err := NewStorageClassConfig(ctx)
	if err != nil {
		return err
	}

	if err := storageConfig.CreateStorageClass(ctx); err != nil {
		return err
	}

	return nil
}

func NewStorageClassConfig(ctx *pulumi.Context) (*StorageClassConfig, error) {
	conf := config.New(ctx, "storage-class")
	storageConfig := &StorageClassConfig{}
	if err := conf.TryObject("properties", storageConfig); err != nil {
		return nil, fmt.Errorf("failed to read storage class config: %w", err)
	}
	return storageConfig, nil
}

func (sconf *StorageClassConfig) CreateStorageClass(ctx *pulumi.Context) error {
	_, err := storagev1.NewStorageClass(
		ctx,
		sconf.Properties.Name,
		&storagev1.StorageClassArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(sconf.Properties.Name),
			},
			Provisioner: pulumi.String(sconf.Properties.Provisioner),
			Parameters: pulumi.StringMap{
				"type": pulumi.String(sconf.Properties.DiskType),
			},
			AllowVolumeExpansion: pulumi.Bool(true),
			VolumeBindingMode:    pulumi.String("WaitForFirstConsumer"),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create storage class: %w", err)
	}
	return nil
}
