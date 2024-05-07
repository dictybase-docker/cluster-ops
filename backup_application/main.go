package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/batch/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type appProperties struct {
	Image string
	Tag   string
}

type specProperties struct {
	jobName    string
	appName    string
	namespace  string
	secretName string
	volumeName string
	app        *appProperties
}

func main() {
	pulumi.Run(execute)
}

func execute(ctx *pulumi.Context) error {
	props, err := getConfig(ctx)
	if err != nil {
		return err
	}
	_, err = batchv1.NewJob(ctx, props.jobName, createJobSpec(props))
	if err != nil {
		return fmt.Errorf("error in running create repository job %s", err)
	}
	return nil
}

func getConfig(ctx *pulumi.Context) (*specProperties, error) {
	cfg := config.New(ctx, "")
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
	appName, err := cfg.Try("name")
	if err != nil {
		return nil, fmt.Errorf("attribute name is missing %s", err)
	}
	jobName := fmt.Sprintf("%s-create-repo", appName)

	return &specProperties{
		jobName:    jobName,
		appName:    appName,
		namespace:  namespace,
		secretName: secret,
		volumeName: fmt.Sprintf("%s-volume", jobName),
		app:        &appProperties{Image: image, Tag: tag},
	}, nil
}
