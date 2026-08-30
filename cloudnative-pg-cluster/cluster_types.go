package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
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

// Placement pins instance pods to a labeled, tainted node pool.
// Zero value (empty Pool) disables placement — pods schedule anywhere.
type Placement struct {
	// Pool is the value of the node label `pool` and of the
	// `dedicated=<pool>:NoSchedule` taint to tolerate.
	Pool string `pulumi:"pool"`
	// TopologyKey spreads instances across this topology domain
	// (e.g. topology.kubernetes.io/zone). Empty keeps the operator
	// default (hostname).
	TopologyKey string `pulumi:"topologyKey"`
	// PodAntiAffinityType is "preferred" or "required".
	// Empty keeps the operator default (preferred).
	PodAntiAffinityType string `pulumi:"podAntiAffinityType"`
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
	Placement  Placement        `pulumi:"placement"`
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

// RecoverySource describes a cross-project barman object store to
// bootstrap from. Set by `just postgres configure-source` only when
// importing data from another cluster's CloudNativePG backup.
// Zero value (empty Bucket) keeps initdb bootstrap.
type RecoverySource struct {
	// SourceCluster is the CNPG Cluster name in the source bucket.
	// CNPG uses it as BOTH the externalClusters entry name and the
	// backup folder name — they cannot differ.
	SourceCluster string `pulumi:"sourceCluster"`
	// Bucket is the source GCS bucket (no gs:// prefix).
	Bucket string `pulumi:"bucket"`
	// BucketPath is the path inside the bucket (source cluster's
	// backup.bucketPath).
	BucketPath string `pulumi:"bucketPath"`
	// TargetTime is an optional RFC3339 PITR timestamp. Empty means
	// latest available.
	TargetTime string `pulumi:"targetTime"`
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
	// Recovery, when set, replaces initdb: the first instance is built
	// from the RecoverySource barman backup instead of an empty database.
	Recovery *RecoverySource `pulumi:"recovery"`
}

type BootstrapSecret struct {
	Name     string `pulumi:"name"`
	Password string `pulumi:"password"`
}

type PostgresqlConfig struct {
	MaxConnections int    `pulumi:"maxConnections"`
	SharedBuffers  string `pulumi:"sharedBuffers"`
}

type Properties struct {
	Clusters     []ClusterWrapper `pulumi:"clusters"`
	BackupSecret BackupSecret     `pulumi:"backupSecret"`
	// SourceSecret holds the source cluster's reader credentials,
	// set by `just postgres configure-source`. Optional.
	SourceSecret *BackupSecret `pulumi:"sourceSecret"`
}

func NewProperties(ctx *pulumi.Context) (*Properties, error) {
	props := &Properties{}
	cfg := config.New(ctx, "")
	if err := cfg.TryObject("properties", props); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return props, nil
}
