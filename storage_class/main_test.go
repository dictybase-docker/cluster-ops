package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const defaultProvisioner = "pd.csi.storage.gke.io"

func TestStorageConfig_GetItems_Single(t *testing.T) {
	cfg := &StorageConfig{
		DiskType:    "pd-balanced",
		Name:        "dictycr-balanced",
		Provisioner: defaultProvisioner,
	}

	items := cfg.GetItems()
	assert.Len(t, items, 1)
	assert.Equal(t, "dictycr-balanced", items[0].Name)
	assert.Equal(t, "pd-balanced", items[0].DiskType)
}

func TestStorageConfig_GetItems_Multi(t *testing.T) {
	cfg := &StorageConfig{
		Classes: []StorageClassItem{
			{
				Name:        "dictycr-balanced",
				DiskType:    "pd-balanced",
				Provisioner: defaultProvisioner,
			},
			{
				Name:        "dictycr-ssd",
				DiskType:    "pd-ssd",
				Provisioner: defaultProvisioner,
			},
		},
	}

	items := cfg.GetItems()
	assert.Len(t, items, 2)
	assert.Equal(t, "dictycr-balanced", items[0].Name)
	assert.Equal(t, "dictycr-ssd", items[1].Name)
}
