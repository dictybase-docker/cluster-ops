package main

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/batch/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type SpecProperties struct {
	Apps       []string
	Arangodb   *AppProperties
	Postgresql *AppProperties
	Image      string
	Namespace  string
	Secret     string
	Tag        string
}

func (spec *SpecProperties) GetApps() []string {
	return spec.Apps
}

func (spec *SpecProperties) GetArangodb() *AppProperties {
	return spec.Arangodb
}

func (spec *SpecProperties) GetPostgresql() *AppProperties {
	return spec.Postgresql
}

func (spec *SpecProperties) GetImage() string {
	return spec.Image
}

func (spec *SpecProperties) GetNamespace() string {
	return spec.Namespace
}

func (spec *SpecProperties) GetSecret() string {
	return spec.Secret
}

func (spec *SpecProperties) GetTag() string {
	return spec.Tag
}

func (spec *SpecProperties) createGcpBucket(
	ctx *pulumi.Context,
	resource, bucket string,
) (*storage.Bucket, error) {
	bucketResource, err := storage.NewBucket(
		ctx,
		resource,
		&storage.BucketArgs{
			Name:                  pulumi.String(bucket),
			Location:              pulumi.String("US-CENTRAL1"),
			EnableObjectRetention: pulumi.Bool(true),
			Versioning: &storage.BucketVersioningArgs{
				Enabled: pulumi.Bool(true),
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error in creating bucket %s: %w",
			bucket,
			err,
		)
	}
	return bucketResource, nil
}

func (spec *SpecProperties) setupPostgresBackupCronJob(
	ctx *pulumi.Context,
) error {
	spec.Postgresql.JobName = jobName(spec.Postgresql.AppName)
	spec.Postgresql.VolumeName = volumeName(spec.Postgresql.AppName)

	_, err := batchv1.NewCronJob(
		ctx,
		spec.Postgresql.JobName,
		createPostgresJobSpec(spec),
		pulumi.DependsOn([]pulumi.Resource{spec.Postgresql.BucketInstance}),
	)
	if err != nil {
		return fmt.Errorf("error in running postgres backup cron job %s", err)
	}
	return nil
}

type AppProperties struct {
	JobName        string
	AppName        string
	VolumeName     string
	Bucket         string
	Schedule       string
	Secret         string
	Databases      []string
	BucketInstance *storage.Bucket
}

type jobProperties struct {
	Job  *batchv1.Job
	Spec *SpecProperties
}

func main() {
	pulumi.Run(execute)
}

func execute(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	props, err := setPropsFromConfig(cfg)
	if err != nil {
		return err
	}

	props.Arangodb.AppName = "arangodb"
	props.Postgresql.AppName = "postgresql"

	arangoBucket, err := props.createGcpBucket(
		ctx,
		fmt.Sprintf("%s-%s", props.Arangodb.Bucket, props.Namespace),
		props.Arangodb.Bucket,
	)
	if err != nil {
		return err
	}
	props.Arangodb.BucketInstance = arangoBucket

	pgBucket, err := props.createGcpBucket(
		ctx,
		fmt.Sprintf("%s-%s", props.Postgresql.Bucket, props.Namespace),
		props.Postgresql.Bucket,
	)
	if err != nil {
		return err
	}
	props.Postgresql.BucketInstance = pgBucket

	err = props.setupPostgresBackupCronJob(ctx)
	if err != nil {
		return err
	}

	return nil
}

func setPropsFromConfig(cfg *config.Config) (*SpecProperties, error) {
	specs := &SpecProperties{}
	if err := cfg.TryObject("properties", specs); err != nil {
		return nil, fmt.Errorf("error in mapping specs %s", err)
	}
	return specs, nil
}

func jobName(name string) string {
	return fmt.Sprintf("%s-backup", name)
}

func volumeName(name string) string {
	return fmt.Sprintf("%-backup-volume", name)
}

/* func createRepoJobs(
	ctx *pulumi.Context,
	cfg *config.Config,
	appNames []string,
	props *specProperties,
	bucket *storage.Bucket,
) (map[string]*jobProperties, error) {
	jobMap := make(map[string]*jobProperties)
	for _, name := range appNames {
		jobprop, err := createAndSetupJob(ctx, cfg, name, props, bucket)
		if err != nil {
			return nil, err
		}
		jobMap[name] = jobprop
	}
	return jobMap, nil
} */

/* func createAndSetupJob(
	ctx *pulumi.Context,
	cfg *config.Config,
	appName string,
	props *specProperties,
	bucket *storage.Bucket,
) (*jobProperties, error) {
	app := &appProperties{}
	if err := cfg.TryObject(appName, app); err != nil {
		return nil, fmt.Errorf(
			"could not resolve app properties for %s: %w",
			appName,
			err,
		)
	}
	app.appName = appName
	app.jobName = fmt.Sprintf("%s-create-repository", appName)
	app.volumeName = fmt.Sprintf("%s-create-repo-volume", appName)
	app.bucket = bucket.Name.ToStringOutput().ApplyT(func(name string) string {
		return fmt.Sprintf("gs://%s", name)
	}).(pulumi.StringOutput)
	props.app = app

	createJob, err := batchv1.NewJob(
		ctx,
		props.app.jobName,
		createRepoJobSpec(props),
		pulumi.DependsOn([]pulumi.Resource{bucket}),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error in running create repository job for %s: %w",
			appName, err,
		)
	}

	return &jobProperties{job: createJob, spec: props}, nil
} */
