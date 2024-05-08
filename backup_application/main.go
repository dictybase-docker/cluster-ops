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
	appNames := make([]string, 0)
	if err := cfg.TryObject("apps", &appNames); err != nil {
		return fmt.Errorf(
			"apps attribute is required in the configuration %s",
			err,
		)
	}
	if !slices.Contains(appNames, "arangodb") ||
		!slices.Contains(appNames, "postgresql") {
		return errors.New("need either of arangodb or postgresql as app names")
	}
	jobMap := make(map[string]*batchv1.Job)
	for _, name := range appNames {
		app := &appProperties{}
		if err := cfg.TryObject(name, app); err != nil {
			return fmt.Errorf("app name %s is required %s", name, err)
		}
		app.appName = name
		app.jobName = fmt.Sprintf("%s-create-repository", name)
		app.volumeName = fmt.Sprintf("%s-create-repo-volume", name)
		app.bucket = fmt.Sprintf("%s-%s", props.namespace, app.bucket)
		props.app = app
		createJob, err := batchv1.NewJob(
			ctx,
			props.app.jobName,
			createJobSpec(props),
		)
		if err != nil {
			return fmt.Errorf("error in running create repository job %s", err)
		}
		jobMap[name] = createJob
	}
	return nil
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
