package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/pulumi/pulumi-gcp/sdk/v7/go/gcp/storage"
	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/batch/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type appProperties struct {
	jobName    string
	appName    string
	volumeName string
	bucket     string
	schedule   string
	secret     string
	databases  []string
}

type jobProperties struct {
	job  *batchv1.Job
	spec *specProperties
}

type specProperties struct {
	namespace  string
	secretName string
	image      string
	tag        string
	app        *appProperties
}

func main() {
	pulumi.Run(execute)
}

func execute(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	props, err := configProps(cfg)
	if err != nil {
		return err
	}

	appNames, err := validateAppNames(cfg)
	if err != nil {
		return err
	}

	jobMap, err := createRepoJobs(ctx, cfg, appNames, props)
	if err != nil {
		return err
	}
	if err := setupAndCreatePostgresBackupCronJob(ctx, jobMap["postgresql"]); err != nil {
		return err
	}
	return nil
}

func setupAndCreatePostgresBackupCronJob(
	ctx *pulumi.Context,
	props *jobProperties,
) error {
	// setup postgres backup cronjob
	pgProps, pgCreateJob := setupCronSpecs(props)
	_, err := batchv1.NewCronJob(
		ctx,
		pgProps.app.jobName,
		createPostgresJobSpec(pgProps),
		pulumi.DependsOn([]pulumi.Resource{pgCreateJob}),
	)
	if err != nil {
		return fmt.Errorf("error in running postgres backup cron job %s", err)
	}
	return nil
}

func setupCronSpecs(props *jobProperties) (*specProperties, *batchv1.Job) {
	props.spec.app.jobName = fmt.Sprintf(
		"%s-backup",
		props.spec.app.appName,
	)
	props.spec.app.volumeName = fmt.Sprintf(
		"%s-backup-volume",
		props.spec.app.appName,
	)
	return props.spec, props.job
}

func createRepoJobs(
	ctx *pulumi.Context,
	cfg *config.Config,
	appNames []string,
	props *specProperties,
) (map[string]*jobProperties, error) {
	jobMap := make(map[string]*jobProperties)
	for _, name := range appNames {
		jobprop, err := createAndSetupJob(ctx, cfg, name, props)
		if err != nil {
			return nil, err
		}
		jobMap[name] = jobprop
	}
	return jobMap, nil
}

func createAndSetupJob(
	ctx *pulumi.Context,
	cfg *config.Config,
	appName string,
	props *specProperties,
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
	app.bucket = fmt.Sprintf("%s-%s", props.namespace, app.bucket)
	props.app = app

	createJob, err := batchv1.NewJob(
		ctx,
		props.app.jobName,
		createRepoJobSpec(props),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error in running create repository job for %s: %w",
			appName, err,
		)
	}

	return &jobProperties{job: createJob, spec: props}, nil
}

func validateAppNames(cfg *config.Config) ([]string, error) {
	appNames := make([]string, 0)
	if err := cfg.TryObject("apps", &appNames); err != nil {
		return nil, fmt.Errorf(
			"apps attribute is required in the configuration: %s",
			err,
		)
	}
	for _, vname := range []string{"postgresql", "arangodb"} {
		if !slices.Contains(appNames, vname) {
			return nil, errors.New(
				"need either of arangodb or postgresql as app names",
			)
		}
	}
	return appNames, nil
}

func configProps(cfg *config.Config) (*specProperties, error) {
	namespace, err := cfg.Try("namespace")
	if err != nil {
		return nil, fmt.Errorf("attribute namespace is missing %s", err)
	}
	tag, err := cfg.Try("tag")
	if err != nil {
		return nil, fmt.Errorf("attribute tag is missing %s", err)
	}
	image, err := cfg.Try("image")
	if err != nil {
		return nil, fmt.Errorf("attribute image is missing %s", err)
	}
	secret, err := cfg.Try("secret")
	if err != nil {
		return nil, fmt.Errorf("attribute secret is missing %s", err)
	}

	return &specProperties{
		namespace:  namespace,
		secretName: secret,
		image:      image,
		tag:        tag,
	}, nil
}

func createGcpBucket(
	bucket string,
	ctx *pulumi.Context,
) (*storage.Bucket, error) {
	bucketResource, err := storage.NewBucket(
		ctx,
		bucket,
		&storage.BucketArgs{
			Location: pulumi.String("US-CENTRAL1"),
			Versioning: &storage.BucketVersioningArgs{
				Enabled: pulumi.Bool(true),
			},
		},
	)
	if err != nil {
		return bucketResource, fmt.Errorf(
			"error in creating bucket %s %q",
			bucket,
			err,
		)
	}
	return bucketResource, nil
}
