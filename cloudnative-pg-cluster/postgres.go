package main

import (
	"fmt"
	"strconv"

	barmancloudv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/barmancloud/v1"
	cnpgv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/postgresql/v1"
	"github.com/dictybase-docker/cluster-ops/internal/nsprobe"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// BarmanCloudPluginName is the CNPG-I plugin identity for the Barman Cloud
// plugin deployed by the cnpg-backup-plugin stack.
const BarmanCloudPluginName = "barman-cloud.cloudnative-pg.io"

func (prop *Properties) CreatePostgresCluster(
	ctx *pulumi.Context,
	cluster Cluster,
	secret *corev1.Secret,
	basicAuthSecret *corev1.Secret,
	objectStore *barmancloudv1.ObjectStore,
	sourceObjectStore *barmancloudv1.ObjectStore,
) (*cnpgv1.Cluster, error) {
	// The plugin must be live before the Cluster CR references it — WAL
	// archiving via a missing plugin fails silently. The stack reference is
	// also a graph dependency of the Cluster.
	pluginRef, err := nsprobe.ProbePlugin(ctx)
	if err != nil {
		return nil, err
	}

	clusterArgs := prop.buildClusterArgs(cluster)
	depends := []pulumi.Resource{secret, basicAuthSecret, objectStore, pluginRef}
	if sourceObjectStore != nil {
		depends = append(depends, sourceObjectStore)
	}
	pgCluster, err := cnpgv1.NewCluster(
		ctx, cluster.Name,
		clusterArgs,
		pulumi.DependsOn(depends),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL cluster: %w", err)
	}

	// Create ScheduledBackup
	scheduledBackupArgs := prop.buildScheduledBackupArgs(cluster)
	_, err = cnpgv1.NewScheduledBackup(
		ctx,
		fmt.Sprintf("%s-scheduled-backup", cluster.Name),
		scheduledBackupArgs,
		pulumi.DependsOn([]pulumi.Resource{pgCluster}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ScheduledBackup: %w", err)
	}

	return pgCluster, nil
}

func (prop *Properties) buildScheduledBackupArgs(
	cluster Cluster,
) *cnpgv1.ScheduledBackupArgs {
	return &cnpgv1.ScheduledBackupArgs{
		ApiVersion: pulumi.String("postgresql.cnpg.io/v1"),
		Kind:       pulumi.String("ScheduledBackup"),
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(cluster.Backup.Name),
			Namespace: pulumi.String(cluster.Namespace),
		},
		Spec: &cnpgv1.ScheduledBackupSpecArgs{
			Schedule: pulumi.String(cluster.Backup.Schedule),
			Cluster: &cnpgv1.ScheduledBackupSpecClusterArgs{
				Name: pulumi.String(cluster.Name),
			},
			Target:               pulumi.String(cluster.Backup.Target),
			BackupOwnerReference: pulumi.String("self"),
			Immediate:            pulumi.Bool(true),
			// Plugin-based backups (in-tree barmanObjectStore is deprecated
			// since CNPG 1.26 and removed in 1.31).
			Method: pulumi.String("plugin"),
			PluginConfiguration: &cnpgv1.ScheduledBackupSpecPluginConfigurationArgs{
				Name: pulumi.String(BarmanCloudPluginName),
			},
		},
	}
}

func (prop *Properties) buildClusterArgs(cluster Cluster) *cnpgv1.ClusterArgs {
	return &cnpgv1.ClusterArgs{
		ApiVersion: pulumi.String("postgresql.cnpg.io/v1"),
		Kind:       pulumi.String("Cluster"),
		Metadata:   prop.buildMetadata(cluster),
		Spec:       prop.buildClusterSpec(cluster, objectStoreNameFor(cluster.Name)),
	}
}

func (prop *Properties) buildMetadata(cluster Cluster) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(cluster.Name),
		Namespace: pulumi.String(cluster.Namespace),
	}
}

func (prop *Properties) buildClusterSpec(
	cluster Cluster,
	objectStoreName string,
) *cnpgv1.ClusterSpecArgs {
	return &cnpgv1.ClusterSpecArgs{
		Instances: pulumi.Int(cluster.Instances),
		ImageName: pulumi.String(
			fmt.Sprintf(
				"%s:%s",
				cluster.Image.Name,
				cluster.Image.Tag,
			),
		),
		Storage:               prop.buildStorageArgs(cluster),
		Postgresql:            prop.buildPostgresqlArgs(cluster),
		Bootstrap:             prop.buildBootstrapArgs(cluster),
		ExternalClusters:      prop.buildExternalClustersArgs(cluster),
		Affinity:              prop.buildAffinityArgs(cluster),
		EnableSuperuserAccess: pulumi.Bool(cluster.Superuser),
		// Plugin-based WAL archiving and backups (in-tree barmanObjectStore
		// is deprecated since CNPG 1.26 and removed in 1.31).
		Plugins: cnpgv1.ClusterSpecPluginsArray{
			&cnpgv1.ClusterSpecPluginsArgs{
				Name:          pulumi.String(BarmanCloudPluginName),
				IsWALArchiver: pulumi.Bool(true),
				Parameters: pulumi.StringMap{
					"barmanObjectName": pulumi.String(objectStoreName),
				},
			},
		},
		Managed: &cnpgv1.ClusterSpecManagedArgs{
			Roles: cnpgv1.ClusterSpecManagedRolesArray{
				&cnpgv1.ClusterSpecManagedRolesArgs{
					Name:   pulumi.String(cluster.Bootstrap.Owner),
					Ensure: pulumi.String("present"),
					Login:  pulumi.Bool(true),
					// Least privilege per CNPG docs: the app owner
					// does its own DDL via database ownership; roles
					// stay declarative (operator-reconciled).
					Createdb:   pulumi.Bool(false),
					Createrole: pulumi.Bool(false),
					// Password tracks the bootstrap user Secret, so the
					// operator reconciles the role password with the
					// Secret instead of leaving initdb's copy behind.
					PasswordSecret: &cnpgv1.ClusterSpecManagedRolesPasswordSecretArgs{
						Name: pulumi.String(cluster.Bootstrap.UserSecret.Name),
					},
				},
			},
		},
	}
}

func (prop *Properties) buildBootstrapArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecBootstrapArgs {
	if cluster.Bootstrap.Recovery != nil {
		return prop.buildRecoveryBootstrapArgs(cluster)
	}
	return &cnpgv1.ClusterSpecBootstrapArgs{
		Initdb: &cnpgv1.ClusterSpecBootstrapInitdbArgs{
			Database: pulumi.String(cluster.Bootstrap.Database),
			Owner:    pulumi.String(cluster.Bootstrap.Owner),
			Secret: &cnpgv1.ClusterSpecBootstrapInitdbSecretArgs{
				Name: pulumi.String(cluster.Bootstrap.UserSecret.Name),
			},
		},
	}
}

// buildRecoveryBootstrapArgs bootstraps from the external cluster's
// barman backup instead of initdb. The database/owner are the ones the
// source cluster had — bootstrap.database/owner are ignored on this path
// (the Secret is still created and reconciles the recovered role
// password via managed.roles).
func (prop *Properties) buildRecoveryBootstrapArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecBootstrapArgs {
	recovery := cluster.Bootstrap.Recovery
	args := &cnpgv1.ClusterSpecBootstrapRecoveryArgs{
		Source: pulumi.String(recovery.SourceCluster),
	}
	if recovery.TargetTime != "" {
		args.RecoveryTarget = &cnpgv1.ClusterSpecBootstrapRecoveryRecoveryTargetArgs{
			TargetTime: pulumi.String(recovery.TargetTime),
		}
	}
	return &cnpgv1.ClusterSpecBootstrapArgs{Recovery: args}
}

// buildExternalClustersArgs declares the source cluster's Barman Cloud
// plugin object store so bootstrap.recovery can read it. Returns nil when
// no recovery source is configured. The plugin configuration references an
// ObjectStore CR that createObjectStore builds alongside the cluster's own.
func (prop *Properties) buildExternalClustersArgs(
	cluster Cluster,
) cnpgv1.ClusterSpecExternalClustersArrayInput {
	recovery := cluster.Bootstrap.Recovery
	if recovery == nil {
		// CreateSourceSecret still fails with a clearer message when
		// recovery is set but properties.sourceSecret is missing.
		return nil
	}
	return cnpgv1.ClusterSpecExternalClustersArray{
		&cnpgv1.ClusterSpecExternalClustersArgs{
			Name: pulumi.String(recovery.SourceCluster),
			Plugin: &cnpgv1.ClusterSpecExternalClustersPluginArgs{
				Name: pulumi.String(BarmanCloudPluginName),
				Parameters: pulumi.StringMap{
					"barmanObjectName": pulumi.String(
						sourceObjectStoreNameFor(cluster.Name, recovery.SourceCluster),
					),
					// The backup folder in the source bucket is named after
					// the SOURCE cluster — without serverName, barman would
					// look under the target cluster's folder.
					"serverName": pulumi.String(recovery.SourceCluster),
				},
			},
		},
	}
}

func (prop *Properties) buildPostgresqlArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecPostgresqlArgs {
	maxConn := strconv.Itoa(cluster.PgConfig.MaxConnections)
	return &cnpgv1.ClusterSpecPostgresqlArgs{
		Parameters: pulumi.StringMap{
			"max_connections": pulumi.String(maxConn),
			"shared_buffers": pulumi.String(
				cluster.PgConfig.SharedBuffers,
			),
			"max_locks_per_transaction":      pulumi.String("640"),
			"max_pred_locks_per_transaction": pulumi.String("640"),
			"work_mem":                       pulumi.String("200MB"),
			"maintenance_work_mem":           pulumi.String("200MB"),
			"temp_buffers":                   pulumi.String("30MB"),
			"wal_buffers":                    pulumi.String("15MB"),
			"wal_level":                      pulumi.String("logical"),
			"min_wal_size":                   pulumi.String("200MB"),
			"max_wal_size":                   pulumi.String("2GB"),
			"checkpoint_timeout":             pulumi.String("10min"),
			"checkpoint_completion_target":   pulumi.String("0.9"),
			"cpu_tuple_cost":                 pulumi.String("0.003"),
			"cpu_index_tuple_cost":           pulumi.String("0.01"),
			"cpu_operator_cost":              pulumi.String("0.0005"),
			"random_page_cost":               pulumi.String("2.5"),
			"default_statistics_target":      pulumi.String("250"),
			"effective_cache_size":           pulumi.String("1GB"),
			"geqo_threshold":                 pulumi.String("14"),
			"from_collapse_limit":            pulumi.String("14"),
			"join_collapse_limit":            pulumi.String("14"),
			"logging_collector":              pulumi.String("on"),
			"log_min_messages":               pulumi.String("warning"),
			"log_min_error_statement":        pulumi.String("warning"),
			"log_min_duration_statement":     pulumi.String("250"),
			"log_checkpoints":                pulumi.String("on"),
			"log_connections":                pulumi.String("on"),
			"log_disconnections":             pulumi.String("on"),
			"log_line_prefix": pulumi.String(
				"[%m] [%u@%d] [%p] %r >",
			),
			"log_lock_waits":                 pulumi.String("on"),
			"log_statement":                  pulumi.String("mod"),
			"log_temp_files":                 pulumi.String("0"),
			"log_error_verbosity":            pulumi.String("default"),
			"log_timezone":                   pulumi.String("America/Chicago"),
			"autovacuum":                     pulumi.String("on"),
			"autovacuum_vacuum_scale_factor": pulumi.String("0.1"),
			"autovacuum_max_workers":         pulumi.String("4"),
			"datestyle":                      pulumi.String("mdy"),
			"timezone":                       pulumi.String("US/Central"),
			"lc_messages":                    pulumi.String("C"),
			"lc_monetary":                    pulumi.String("C"),
			"lc_numeric":                     pulumi.String("C"),
			"lc_time":                        pulumi.String("C"),
			"default_text_search_config": pulumi.String(
				"pg_catalog.english",
			),
		},
	}
}

// buildAffinityArgs pins instance pods to the configured node pool and
// tolerates its taint. Returns nil when no pool is configured so existing
// stacks without a placement block keep scheduling anywhere.
func (prop *Properties) buildAffinityArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecAffinityArgs {
	if cluster.Placement.Pool == "" {
		return nil
	}
	args := &cnpgv1.ClusterSpecAffinityArgs{
		NodeSelector: pulumi.StringMap{
			"pool": pulumi.String(cluster.Placement.Pool),
		},
		Tolerations: cnpgv1.ClusterSpecAffinityTolerationsArray{
			&cnpgv1.ClusterSpecAffinityTolerationsArgs{
				Key:      pulumi.String("dedicated"),
				Operator: pulumi.String("Equal"),
				Value:    pulumi.String(cluster.Placement.Pool),
				Effect:   pulumi.String("NoSchedule"),
			},
		},
	}
	if cluster.Placement.TopologyKey != "" {
		args.TopologyKey = pulumi.String(cluster.Placement.TopologyKey)
	}
	if cluster.Placement.PodAntiAffinityType != "" {
		args.PodAntiAffinityType = pulumi.String(
			cluster.Placement.PodAntiAffinityType,
		)
	}
	return args
}

func (prop *Properties) buildStorageArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecStorageArgs {
	return &cnpgv1.ClusterSpecStorageArgs{
		StorageClass: pulumi.String(cluster.Storage.Class),
		Size:         pulumi.String(cluster.Storage.Size),
	}
}

/* func (prop *Properties) buildWalStorageArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecWalStorageArgs {
	return &cnpgv1.ClusterSpecWalStorageArgs{
		StorageClass: pulumi.String(cluster.WalStorage.Class),
		Size:         pulumi.String(cluster.WalStorage.Size),
	}
} */
