package main

import (
	"fmt"
	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(execute)
}
func execute(ctx *pulumi.Context) error {
		// Load configuration
		conf := config.New(ctx, "")
		namespace := conf.Require("namespace")
		name := conf.Require("name")
		image := conf.Get("image")
		if image == "" {
			image = "dictybase/graphql-server"
		}
		tag := conf.Require("tag")
		logLevel := conf.Get("logLevel")
		if logLevel == "" {
			logLevel = "error"
		}
		port := conf.GetInt("port")
		if port == 0 {
			port = 8080
		}
		secretName := conf.Get("secret")
		if secretName == "" {
			secretName = "dictycr-secret"
		}
		configMapName := conf.Get("configMap")
		if configMapName == "" {
			configMapName = "dictycr-configuration"
		}
		s3Bucket := conf.Get("s3Bucket")
		if s3Bucket == "" {
			s3Bucket = "editor"
		}
		s3BucketPath := conf.Get("s3BucketPath")
		if s3BucketPath == "" {
			s3BucketPath = "assets"
		}

		// Define allowed origins
		allowedOrigins := []string{
			"http://localhost:*",
			"https://dictybase.org",
			"https://*.dictybase.org",
			"https://dictycr.org",
			"https://*.dictycr.org",
			"https://dictybase.dev",
			"https://*.dictybase.dev",
			"https://dictybase.dev*",
		}

		// Create deployment
		deployment, err := appsv1.NewDeployment(ctx, fmt.Sprintf("%s-api-server", name), &appsv1.DeploymentArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(fmt.Sprintf("%s-api-server", name)),
				Namespace: pulumi.String(namespace),
			},
			Spec: &appsv1.DeploymentSpecArgs{
				Selector: &metav1.LabelSelectorArgs{
					MatchLabels: pulumi.StringMap{
						"app": pulumi.String(fmt.Sprintf("%s-api-server", name)),
					},
				},
				Template: &corev1.PodTemplateSpecArgs{
					Metadata: &metav1.ObjectMetaArgs{
						Labels: pulumi.StringMap{
							"app": pulumi.String(fmt.Sprintf("%s-api-server", name)),
						},
					},
					Spec: &corev1.PodSpecArgs{
						Containers: corev1.ContainerArray{
							&corev1.ContainerArgs{
								Name:  pulumi.String(fmt.Sprintf("%s-container", name)),
								Image: pulumi.String(fmt.Sprintf("%s:%s", image, tag)),
								Args: pulumi.StringArray{
									pulumi.String("--log-level"),
									pulumi.String(logLevel),
									pulumi.String("start-server"),
								},
								Env: corev1.EnvVarArray{
									&corev1.EnvVarArgs{
										Name: pulumi.String("SECRET_KEY"),
										ValueFrom: &corev1.EnvVarSourceArgs{
											SecretKeyRef: &corev1.SecretKeySelectorArgs{
												Name: pulumi.String(secretName),
												Key:  pulumi.String("minio.secretkey"),
											},
										},
									},
									&corev1.EnvVarArgs{
										Name: pulumi.String("ACCESS_KEY"),
										ValueFrom: &corev1.EnvVarSourceArgs{
											SecretKeyRef: &corev1.SecretKeySelectorArgs{
												Name: pulumi.String(secretName),
												Key:  pulumi.String("minio.accesskey"),
											},
										},
									},
									// Add other environment variables here
								},
								Ports: corev1.ContainerPortArray{
									&corev1.ContainerPortArgs{
										Name:          pulumi.String(fmt.Sprintf("%s-api", name)),
										ContainerPort: pulumi.Int(port),
										Protocol:      pulumi.String("TCP"),
									},
								},
							},
						},
					},
				},
			},
		})
		if err != nil {
			return err
		}

		// Create service
		_, err = corev1.NewService(ctx, fmt.Sprintf("%s-api", name), &corev1.ServiceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(fmt.Sprintf("%s-api", name)),
				Namespace: pulumi.String(namespace),
			},
			Spec: &corev1.ServiceSpecArgs{
				Selector: pulumi.StringMap{
					"app": pulumi.String(fmt.Sprintf("%s-api-server", name)),
				},
				Ports: corev1.ServicePortArray{
					&corev1.ServicePortArgs{
						Port:       pulumi.Int(port),
						TargetPort: pulumi.Int(port),
					},
				},
				Type: pulumi.String("NodePort"),
			},
		})
		if err != nil {
			return err
		}

		return nil
	}
