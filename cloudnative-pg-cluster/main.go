package main

import (
	"fmt"
	"os"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	cnpgv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/postgresql/v1"
)

type Image struct {
	Name string `pulumi:"name"`
	Tag  string `pulumi:"tag"`
}

type Storage struct {
	Class string `pulumi:"class"`
	Size  string `pulumi:"size"`
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
	Bucket     string `pulumi:"bucket"`
	BucketPath string `pulumi:"bucketPath"`
	Retention  string `pulumi:"retention"`
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
	Cluster      Cluster      `pulumi:"cluster"`
	BackupSecret BackupSecret `pulumi:"backupSecret"`
}

func NewProperties(ctx *pulumi.Context) (*Properties, error) {
	props := &Properties{}
	cfg := config.New(ctx, "")
	if err := cfg.TryObject("properties", props); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return props, nil
}

func (prop *Properties) CreateSecret(
	ctx *pulumi.Context,
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
				Namespace: pulumi.String(prop.Cluster.Namespace),
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

func (prop *Properties) CreateBasicAuthSecret(
	ctx *pulumi.Context,
) (*corev1.Secret, error) {
	secret, err := corev1.NewSecret(ctx,
		prop.Cluster.Bootstrap.UserSecret.Name,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(
					prop.Cluster.Bootstrap.UserSecret.Name,
				),
				Namespace: pulumi.String(prop.Cluster.Namespace),
			},
			Type: pulumi.String("kubernetes.io/basic-auth"),
			StringData: pulumi.StringMap{
				"username": pulumi.String(prop.Cluster.Bootstrap.Owner),
				"password": pulumi.String(
					prop.Cluster.Bootstrap.UserSecret.Password,
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

	// Create the secret using the receiver method
	secret, err := props.CreateSecret(ctx)
	if err != nil {
		return err
	}

	// Create the basic auth secret
	basicAuthSecret, err := props.CreateBasicAuthSecret(ctx)
	if err != nil {
		return err
	}

	// Create the PostgreSQL Cluster, passing both secrets as dependencies
	cluster, err := props.CreatePostgresCluster(ctx, secret, basicAuthSecret)
	if err != nil {
		return err
	}

	// Export the secret names and cluster name
	ctx.Export("secretName", secret.Metadata.Name())
	ctx.Export("basicAuthSecretName", basicAuthSecret.Metadata.Name())
	ctx.Export("clusterName", cluster.Metadata.Name())

	return nil
}

func (prop *Properties) CreatePostgresCluster(
	ctx *pulumi.Context,
	secret *corev1.Secret,
	basicAuthSecret *corev1.Secret,
) (*cnpgv1.Cluster, error) {
	clusterArgs := prop.buildClusterArgs()
	cluster, err := cnpgv1.NewCluster(
		ctx, prop.Cluster.Name,
		clusterArgs,
		pulumi.DependsOn([]pulumi.Resource{secret, basicAuthSecret}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL cluster: %w", err)
	}
	return cluster, nil
}

func (prop *Properties) buildClusterArgs() *cnpgv1.ClusterArgs {
	return &cnpgv1.ClusterArgs{
		ApiVersion: pulumi.String("postgresql.cnpg.io/v1"),
		Kind:       pulumi.String("Cluster"),
		Metadata:   prop.buildMetadata(),
		Spec:       prop.buildClusterSpec(),
	}
}

func (prop *Properties) buildMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(prop.Cluster.Name),
		Namespace: pulumi.String(prop.Cluster.Namespace),
	}
}

func (prop *Properties) buildClusterSpec() *cnpgv1.ClusterSpecArgs {
	return &cnpgv1.ClusterSpecArgs{
		Instances: pulumi.Int(prop.Cluster.Instances),
		ImageName: pulumi.String(
			fmt.Sprintf(
				"%s:%s",
				prop.Cluster.Image.Name,
				prop.Cluster.Image.Tag,
			),
		),
		Storage:               prop.buildStorageArgs(),
		WalStorage:            prop.buildWalStorageArgs(),
		Postgresql:            prop.buildPostgresqlArgs(),
		Bootstrap:             prop.buildBootstrapArgs(),
		EnableSuperuserAccess: pulumi.Bool(prop.Cluster.Superuser),
		Backup:                prop.buildBackupArgs(),
	}
}

func (prop *Properties) buildWalBackupConfigurationArgs() *cnpgv1.ClusterSpecBackupBarmanObjectStoreWalArgs {
	return &cnpgv1.ClusterSpecBackupBarmanObjectStoreWalArgs{
		Compression: pulumi.String(prop.Cluster.WalBackup.Compression),
		MaxParallel: pulumi.Int(prop.Cluster.WalBackup.MaxParallel),
	}
}

func (prop *Properties) buildBackupArgs() *cnpgv1.ClusterSpecBackupArgs {
	return &cnpgv1.ClusterSpecBackupArgs{
		BarmanObjectStore: &cnpgv1.ClusterSpecBackupBarmanObjectStoreArgs{
			DestinationPath: pulumi.String(
				fmt.Sprintf(
					"%s/%s",
					prop.Cluster.Backup.Bucket,
					prop.Cluster.Backup.BucketPath,
				),
			),
			GoogleCredentials: &cnpgv1.ClusterSpecBackupBarmanObjectStoreGoogleCredentialsArgs{
				ApplicationCredentials: &cnpgv1.ClusterSpecBackupBarmanObjectStoreGoogleCredentialsApplicationCredentialsArgs{
					Name: pulumi.String(prop.BackupSecret.Name),
					Key:  pulumi.String(prop.BackupSecret.Key),
				},
			},
			Wal: prop.buildWalBackupConfigurationArgs(),
		},
		RetentionPolicy: pulumi.String(prop.Cluster.Backup.Retention),
	}
}

func (prop *Properties) buildBootstrapArgs() *cnpgv1.ClusterSpecBootstrapArgs {
	return &cnpgv1.ClusterSpecBootstrapArgs{
		Initdb: &cnpgv1.ClusterSpecBootstrapInitdbArgs{
			Database: pulumi.String(prop.Cluster.Bootstrap.Database),
			Owner:    pulumi.String(prop.Cluster.Bootstrap.Owner),
			Secret: &cnpgv1.ClusterSpecBootstrapInitdbSecretArgs{
				Name: pulumi.String(prop.Cluster.Bootstrap.UserSecret.Name),
			},
		},
	}
}

func (prop *Properties) buildPostgresqlArgs() *cnpgv1.ClusterSpecPostgresqlArgs {
	return &cnpgv1.ClusterSpecPostgresqlArgs{
		Parameters: pulumi.StringMap{
			"max_connections": pulumi.String(
				prop.Cluster.PgConfig.MaxConnections,
			),
			"shared_buffers": pulumi.String(
				prop.Cluster.PgConfig.SharedBuffer,
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

func (prop *Properties) buildStorageArgs() *cnpgv1.ClusterSpecStorageArgs {
	return &cnpgv1.ClusterSpecStorageArgs{
		StorageClass: pulumi.String(prop.Cluster.Storage.Class),
		Size:         pulumi.String(prop.Cluster.Storage.Size),
	}
}

func (prop *Properties) buildWalStorageArgs() *cnpgv1.ClusterSpecWalStorageArgs {
	return &cnpgv1.ClusterSpecWalStorageArgs{
		StorageClass: pulumi.String(prop.Cluster.WalStorage.Class),
		Size:         pulumi.String(prop.Cluster.WalStorage.Size),
	}
}

func main() {
	pulumi.Run(createResources)
}
