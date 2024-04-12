package main

import (
	"fmt"
	"strings"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func containerEnvSpec(
	cfg *config.Config,
	ctx *pulumi.Context,
) corev1.EnvVarArray {
	return corev1.EnvVarArray{
		corev1.EnvVarArgs{
			Name: pulumi.String("ACCESS_KEY"),
			ValueFrom: corev1.EnvVarSourceArgs{
				SecretKeyRef: corev1.SecretKeySelectorArgs{
					Name: pulumi.StringPtr(config.Require(ctx, "secret")),
					Key:  pulumi.String("minio.accesskey"),
				},
			},
		},
		corev1.EnvVarArgs{
			Name: pulumi.String("SECRET_KEY"),
			ValueFrom: corev1.EnvVarSourceArgs{
				SecretKeyRef: corev1.SecretKeySelectorArgs{
					Name: pulumi.StringPtr(config.Require(ctx, "secret")),
					Key:  pulumi.String("minio.secretkey"),
				},
			},
		},
	}
}

func execute(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	name := cfg.Require("name")
	job, err := batchv1.NewJob(ctx, name, &batchv1.JobArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(cfg.Require("namespace")),
			Labels: pulumi.StringMap{
				"app": pulumi.String(fmt.Sprintf("%s-pulumi", name)),
			},
		},
		Spec: batchv1.JobSpecArgs{
			Template: &corev1.PodTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Name: pulumi.String(fmt.Sprintf("%s-template", name)),
					Labels: pulumi.StringMap{
						"app": pulumi.String(
							fmt.Sprintf("%s-pulumi-template", name),
						),
					},
				},
				Spec: &corev1.PodSpecArgs{
					RestartPolicy: pulumi.String("Never"),
					Containers:    containerSpec(cfg, ctx),
				},
			},
		},
	})
	if err != nil {
		return err
	}
	ctx.Export("name", job.Metadata.Name())
	return nil
}

func containerSpec(
	cfg *config.Config,
	ctx *pulumi.Context,
) corev1.ContainerArray {
	lexicalBuckets := make([]string, 0)
	cfg.RequireObject("s3-lexical-path", &lexicalBuckets)
	containers := make([]corev1.ContainerInput, 0)
	for _, bucket := range lexicalBuckets {
		containers = append(
			containers, corev1.ContainerArgs{
				Name: pulumi.String(
					fmt.Sprintf(
						"%s-container-%s",
						cfg.Require("name"),
						strings.ReplaceAll(bucket,"/","-"),
					),
				),
				Image: pulumi.String(
					fmt.Sprintf(
						"%s:%s",
						cfg.Require("image"),
						cfg.Require("tag"),
					),
				),
				Command: pulumi.ToStringArray(
					[]string{cfg.Require("command")},
				),
				Args: pulumi.ToStringArray([]string{
					"--log-level",
					cfg.Get("log-level"),
					cfg.Require("sub-command"),
					"--s3-bucket-path",
					bucket,
				}),
				Env: containerEnvSpec(cfg, ctx),
			})
	}
	return containers
}

func main() {
	pulumi.Run(execute)
}
