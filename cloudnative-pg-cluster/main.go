package main

import (
	"fmt"
	"os"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	cnpgv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/postgresql/v1"
	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
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

func (prop *Properties) CreateBackupSecret(
	ctx *pulumi.Context,
	cluster Cluster,
) (*corev1.Secret, error) {
	// Read the file content
	fileContent, err := os.ReadFile(prop.BackupSecret.Filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Create the secret
	secret, err := corev1.NewSecret(
		ctx,
		prop.BackupSecret.Name,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(prop.BackupSecret.Name),
				Namespace: pulumi.String(cluster.Namespace),
			},
			StringData: pulumi.StringMap{
				prop.BackupSecret.Key: pulumi.String(string(fileContent)),
			},
		},
	)
	if err != nil {
		return nil, err
	}

	return secret, nil
}

func (prop *Properties) CreateUserSecret(
	ctx *pulumi.Context,
	cluster Cluster,
) (*corev1.Secret, error) {
	secret, err := corev1.NewSecret(ctx,
		cluster.Bootstrap.UserSecret.Name,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(
					cluster.Bootstrap.UserSecret.Name,
				),
				Namespace: pulumi.String(cluster.Namespace),
			},
			Type: pulumi.String("kubernetes.io/basic-auth"),
			StringData: pulumi.StringMap{
				"username": pulumi.String(cluster.Bootstrap.Owner),
				"password": pulumi.String(
					cluster.Bootstrap.UserSecret.Password,
				),
			},
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create basic auth secret: %w", err)
	}

	return secret, nil
}

func createResources(ctx *pulumi.Context) error {
	// Initialize Properties using the constructor
	props, err := NewProperties(ctx)
	if err != nil {
		return err
	}

	for i, clusterWrapper := range props.Clusters {
		cluster := clusterWrapper.Cluster

		// Create GCS bucket for each cluster
		bucket, err := createBackupGCSBucket(
			ctx,
			cluster.Backup.Bucket,
			cluster.Backup.BucketLocation,
		)
		if err != nil {
			return err
		}
		ctx.Export(fmt.Sprintf("bucketName_%d", i), bucket.Name)

		// Create the secret using the receiver method
		secret, err := props.CreateBackupSecret(ctx, cluster)
		if err != nil {
			return err
		}

		// Create the basic auth secret for each cluster
		basicAuthSecret, err := props.CreateUserSecret(ctx, cluster)
		if err != nil {
			return err
		}

		// Create the PostgreSQL Cluster, passing both secrets and the bucket as dependencies
		pgCluster, err := props.CreatePostgresCluster(
			ctx,
			cluster,
			secret,
			basicAuthSecret,
			bucket,
		)
		if err != nil {
			return err
		}

		// Export the secret names and cluster name for each cluster
		ctx.Export(fmt.Sprintf("secretName_%d", i), secret.Metadata.Name())
		ctx.Export(
			fmt.Sprintf("basicAuthSecretName_%d", i),
			basicAuthSecret.Metadata.Name(),
		)
		ctx.Export(fmt.Sprintf("clusterName_%d", i), pgCluster.Metadata.Name())
	}

	return nil
}

func (prop *Properties) CreatePostgresCluster(
	ctx *pulumi.Context,
	cluster Cluster,
	secret *corev1.Secret,
	basicAuthSecret *corev1.Secret,
	bucket *storage.Bucket,
) (*cnpgv1.Cluster, error) {
	clusterArgs := prop.buildClusterArgs(cluster)
	pgCluster, err := cnpgv1.NewCluster(
		ctx, cluster.Name,
		clusterArgs,
		pulumi.DependsOn([]pulumi.Resource{secret, basicAuthSecret, bucket}),
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
		},
	}
}

func (prop *Properties) buildClusterArgs(cluster Cluster) *cnpgv1.ClusterArgs {
	return &cnpgv1.ClusterArgs{
		ApiVersion: pulumi.String("postgresql.cnpg.io/v1"),
		Kind:       pulumi.String("Cluster"),
		Metadata:   prop.buildMetadata(cluster),
		Spec:       prop.buildClusterSpec(cluster),
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
		WalStorage:            prop.buildWalStorageArgs(cluster),
		Postgresql:            prop.buildPostgresqlArgs(cluster),
		Bootstrap:             prop.buildBootstrapArgs(cluster),
		EnableSuperuserAccess: pulumi.Bool(cluster.Superuser),
		Backup:                prop.buildBackupArgs(cluster),
	}
}

func (prop *Properties) buildWalBackupConfigurationArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecBackupBarmanObjectStoreWalArgs {
	return &cnpgv1.ClusterSpecBackupBarmanObjectStoreWalArgs{
		Compression: pulumi.String(cluster.WalBackup.Compression),
		MaxParallel: pulumi.Int(cluster.WalBackup.MaxParallel),
	}
}

func (prop *Properties) buildBackupArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecBackupArgs {
	return &cnpgv1.ClusterSpecBackupArgs{
		BarmanObjectStore: &cnpgv1.ClusterSpecBackupBarmanObjectStoreArgs{
			DestinationPath: pulumi.String(
				fmt.Sprintf(
					"%s/%s",
					cluster.Backup.Bucket,
					cluster.Backup.BucketPath,
				),
			),
			GoogleCredentials: &cnpgv1.ClusterSpecBackupBarmanObjectStoreGoogleCredentialsArgs{
				ApplicationCredentials: &cnpgv1.ClusterSpecBackupBarmanObjectStoreGoogleCredentialsApplicationCredentialsArgs{
					Name: pulumi.String(prop.BackupSecret.Name),
					Key:  pulumi.String(prop.BackupSecret.Key),
				},
			},
			Wal: prop.buildWalBackupConfigurationArgs(cluster),
			Data: &cnpgv1.ClusterSpecBackupBarmanObjectStoreDataArgs{
				Compression: pulumi.String(cluster.WalBackup.Compression),
			},
		},
		RetentionPolicy: pulumi.String(cluster.Backup.Retention),
	}
}

func (prop *Properties) buildBootstrapArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecBootstrapArgs {
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

func (prop *Properties) buildPostgresqlArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecPostgresqlArgs {
	return &cnpgv1.ClusterSpecPostgresqlArgs{
		Parameters: pulumi.StringMap{
			"max_connections": pulumi.String(
				cluster.PgConfig.MaxConnections,
			),
			"shared_buffers": pulumi.String(
				cluster.PgConfig.SharedBuffer,
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
			"log_destination":                pulumi.String("stderr"),
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
			"datestyle":                      pulumi.String("iso mdy"),
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

func (prop *Properties) buildStorageArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecStorageArgs {
	return &cnpgv1.ClusterSpecStorageArgs{
		StorageClass: pulumi.String(cluster.Storage.Class),
		Size:         pulumi.String(cluster.Storage.Size),
	}
}

func (prop *Properties) buildWalStorageArgs(
	cluster Cluster,
) *cnpgv1.ClusterSpecWalStorageArgs {
	return &cnpgv1.ClusterSpecWalStorageArgs{
		StorageClass: pulumi.String(cluster.WalStorage.Class),
		Size:         pulumi.String(cluster.WalStorage.Size),
	}
}

func createBackupGCSBucket(
	ctx *pulumi.Context,
	name string,
	location string,
) (*storage.Bucket, error) {
	bucket, err := storage.NewBucket(ctx, name, &storage.BucketArgs{
		Name:     pulumi.String(name),
		Location: pulumi.String(location),
		Versioning: &storage.BucketVersioningArgs{
			Enabled: pulumi.Bool(true),
		},
		LifecycleRules: storage.BucketLifecycleRuleArray{
			&storage.BucketLifecycleRuleArgs{
				Action: &storage.BucketLifecycleRuleActionArgs{
					Type: pulumi.String("Delete"),
				},
				Condition: &storage.BucketLifecycleRuleConditionArgs{
					Age:              pulumi.Int(90),
					NumNewerVersions: pulumi.Int(1),
				},
			},
		},
		SoftDeletePolicy: &storage.BucketSoftDeletePolicyArgs{
			RetentionDurationSeconds: pulumi.Int(
				30 * 24 * 60 * 60,
			), // 30 days in seconds
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating GCS bucket: %v", err)
	}

	return bucket, nil
}

func main() {
	pulumi.Run(createResources)
}
