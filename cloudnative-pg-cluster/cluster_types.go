package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Image struct {
	Name string `pulumi:"name"`
	Tag  string `pulumi:"tag"`
}

type Storage struct {
	Class string `pulumi:"class"`
	Size  string `pulumi:"size"`
}

type ClusterWrapper struct {
	Cluster Cluster `json:"cluster"`
}

type Cluster struct {
	Image      Image            `pulumi:"image"`
	Instances  int              `pulumi:"instances"`
	Name       string           `pulumi:"name"`
	Namespace  string           `pulumi:"namespace"`
	Storage    Storage          `pulumi:"storage"`
	WalStorage Storage          `pulumi:"walStorage"`
	PgConfig   PostgresqlConfig `pulumi:"pgconfig"`
	Bootstrap  Bootstrap        `pulumi:"bootstrap"`
	Superuser  bool             `pulumi:"superuser"`
	Backup     Backup           `pulumi:"backup"`
	WalBackup  WalBackup        `pulumi:"walBackup"`
}

type WalBackup struct {
	Compression string `pulumi:"compression"`
	MaxParallel int    `pulumi:"maxParallel"`
}

type Backup struct {
	Bucket         string `pulumi:"bucket"`
	BucketLocation string `pulumi:"bucketLocation"`
	BucketPath     string `pulumi:"bucketPath"`
	Retention      string `pulumi:"retention"`
	Name           string `pulumi:"name"`
	Schedule       string `pulumi:"schedule"`
	Target         string `pulumi:"target"`
}

type BackupSecret struct {
	Name     string `pulumi:"name"`
	Key      string `pulumi:"key"`
	Filepath string `pulumi:"filepath"`
}

type Bootstrap struct {
	Database   string          `pulumi:"database"`
	Owner      string          `pulumi:"owner"`
	UserSecret BootstrapSecret `pulumi:"userSecret"`
}

type BootstrapSecret struct {
	Name     string `pulumi:"name"`
	Password string `pulumi:"password"`
}

type PostgresqlConfig struct {
	MaxConnections string `pulumi:"max_connections"`
	SharedBuffer   string `pulumi:"shared_buffer"`
}

type Properties struct {
	Clusters     []ClusterWrapper `pulumi:"clusters"`
	BackupSecret BackupSecret     `pulumi:"backupSecret"`
}

func NewProperties(ctx *pulumi.Context) (*Properties, error) {
	props := &Properties{}
	cfg := config.New(ctx, "")
	if err := cfg.TryObject("properties", props); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return props, nil
}
