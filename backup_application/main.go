package main

import (
	"errors"
	"fmt"
	"slices"

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
	job *batchv1.Job
	app *appProperties
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

	return nil
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
		createJobSpec(props),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error in running create repository job for %s: %w",
			appName, err,
		)
	}

	return &jobProperties{job: createJob, app: app}, nil
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
