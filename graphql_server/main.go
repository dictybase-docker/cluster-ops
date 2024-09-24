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

var allowedOrigins = []string{
	"http://localhost:*",
	"https://dictybase.org",
	"https://*.dictybase.org",
	"https://dictycr.org",
	"https://*.dictycr.org",
	"https://dictybase.dev",
	"https://*.dictybase.dev",
	"https://dictybase.dev*",
}

type Config struct {
	Namespace     string
	Name          string
	Image         string
	Tag           string
	LogLevel      string
	Port          int
	SecretName    string
	ConfigMapName string
	S3Bucket      string
	S3BucketPath  string
}

func main() {
	pulumi.Run(execute)
}

func loadConfig(ctx *pulumi.Context) (*Config, error) {
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

	return &Config{
		Namespace:     namespace,
		Name:          name,
		Image:         image,
		Tag:           tag,
		LogLevel:      logLevel,
		Port:          port,
		SecretName:    secretName,
		ConfigMapName: configMapName,
		S3Bucket:      s3Bucket,
		S3BucketPath:  s3BucketPath,
		AllowedOrigins: allowedOrigins,
	}, nil
}

func execute(ctx *pulumi.Context) error {
	// Load configuration
	config, err := loadConfig(ctx)
	if err != nil {
		return err
	}


	// Create deployment
	deployment, err := appsv1.NewDeployment(ctx, fmt.Sprintf("%s-api-server", config.Name), &appsv1.DeploymentArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(fmt.Sprintf("%s-api-server", config.Name)),
			Namespace: pulumi.String(config.Namespace),
		},
		Spec: &appsv1.DeploymentSpecArgs{
			Selector: &metav1.LabelSelectorArgs{
				MatchLabels: pulumi.StringMap{
					"app": pulumi.String(fmt.Sprintf("%s-api-server", config.Name)),
				},
			},
			Template: &corev1.PodTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Labels: pulumi.StringMap{
						"app": pulumi.String(fmt.Sprintf("%s-api-server", config.Name)),
					},
				},
				Spec: &corev1.PodSpecArgs{
					Containers: corev1.ContainerArray{
						&corev1.ContainerArgs{
							Name:  pulumi.String(fmt.Sprintf("%s-container", config.Name)),
							Image: pulumi.String(fmt.Sprintf("%s:%s", config.Image, config.Tag)),
							Args: pulumi.StringArray{
								pulumi.String("--log-level"),
								pulumi.String(config.LogLevel),
								pulumi.String("start-server"),
							},
							Env: corev1.EnvVarArray{
								&corev1.EnvVarArgs{
									Name: pulumi.String("SECRET_KEY"),
									ValueFrom: &corev1.EnvVarSourceArgs{
										SecretKeyRef: &corev1.SecretKeySelectorArgs{
											Name: pulumi.String(config.SecretName),
											Key:  pulumi.String("minio.secretkey"),
										},
									},
								},
								&corev1.EnvVarArgs{
									Name: pulumi.String("ACCESS_KEY"),
									ValueFrom: &corev1.EnvVarSourceArgs{
										SecretKeyRef: &corev1.SecretKeySelectorArgs{
											Name: pulumi.String(config.SecretName),
											Key:  pulumi.String("minio.accesskey"),
										},
									},
								},
								// Add other environment variables here
							},
							Ports: corev1.ContainerPortArray{
								&corev1.ContainerPortArgs{
									Name:          pulumi.String(fmt.Sprintf("%s-api", config.Name)),
									ContainerPort: pulumi.Int(config.Port),
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
	_, err = corev1.NewService(ctx, fmt.Sprintf("%s-api", config.Name), &corev1.ServiceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(fmt.Sprintf("%s-api", config.Name)),
			Namespace: pulumi.String(config.Namespace),
		},
		Spec: &corev1.ServiceSpecArgs{
			Selector: pulumi.StringMap{
				"app": pulumi.String(fmt.Sprintf("%s-api-server", config.Name)),
			},
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Port:       pulumi.Int(config.Port),
					TargetPort: pulumi.Int(config.Port),
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
