package main

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	storagev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/storage/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type StorageClassItem struct {
	DiskType    string `json:"diskType"`
	Name        string `json:"name"`
	Provisioner string `json:"provisioner"`
}

type StorageConfig struct {
	// For backward compatibility with single-class stacks
	DiskType    string `json:"diskType,omitempty"`
	Name        string `json:"name,omitempty"`
	Provisioner string `json:"provisioner,omitempty"`
	// For multi-class stacks (e.g. prod with balanced + ssd)
	Classes []StorageClassItem `json:"classes,omitempty"`
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	storageConfig, err := ReadStorageConfig(ctx)
	if err != nil {
		return err
	}

	return storageConfig.CreateStorageClasses(ctx)
}

func ReadStorageConfig(ctx *pulumi.Context) (*StorageConfig, error) {
	conf := config.New(ctx, "")
	storageConfig := &StorageConfig{}
	if err := conf.TryObject("properties", storageConfig); err != nil {
		return nil, fmt.Errorf("failed to read storage class config: %w", err)
	}
	return storageConfig, nil
}

func (sconf *StorageConfig) GetItems() []StorageClassItem {
	if len(sconf.Classes) > 0 {
		return sconf.Classes
	}
	if sconf.Name != "" {
		return []StorageClassItem{
			{
				DiskType:    sconf.DiskType,
				Name:        sconf.Name,
				Provisioner: sconf.Provisioner,
			},
		}
	}
	return nil
}

func (sconf *StorageConfig) CreateStorageClasses(ctx *pulumi.Context) error {
	items := sconf.GetItems()
	if len(items) == 0 {
		return fmt.Errorf("no storage classes defined in configuration")
	}

	for _, item := range items {
		_, err := storagev1.NewStorageClass(
			ctx,
			item.Name,
			&storagev1.StorageClassArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Name: pulumi.String(item.Name),
				},
				Provisioner: pulumi.String(item.Provisioner),
				Parameters: pulumi.StringMap{
					"type": pulumi.String(item.DiskType),
				},
				AllowVolumeExpansion: pulumi.Bool(true),
				VolumeBindingMode:    pulumi.String("WaitForFirstConsumer"),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create storage class %s: %w", item.Name, err)
		}
	}
	return nil
}
