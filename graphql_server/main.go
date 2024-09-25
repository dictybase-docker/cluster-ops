package main

import (
	"fmt"
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
	namespace     string
	name          string
	image         string
	tag           string
	logLevel      string
	port          int
	secretName    string
	configMapName string
	s3Bucket      string
	s3BucketPath  string
  allowedOrigins []string
}

func main() {
	pulumi.Run(execute)
}

func loadConfig(ctx *pulumi.Context) Config {
	conf := config.New(ctx, "")

	namespace := conf.Require("namespace")
	name := conf.Require("name")
	tag := conf.Require("tag")

	image := conf.Get("image")
	if image == "" {
		image = "dictybase/graphql-server"
	}

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

	return Config{
		namespace:     namespace,
		name:          name,
		image:         image,
		tag:           tag,
		logLevel:      logLevel,
		port:          port,
		secretName:    secretName,
		configMapName: configMapName,
		s3Bucket:      s3Bucket,
		s3BucketPath:  s3BucketPath,
		allowedOrigins: allowedOrigins,
	}
}

func execute(ctx *pulumi.Context) error {
	// Load configuration
	config := loadConfig(ctx)

	// Create deployment
  _, err := appsv1.NewDeployment(ctx, fmt.Sprintf("%s-api-server", config.name), &appsv1.DeploymentArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(fmt.Sprintf("%s-api-server", config.name)),
			Namespace: pulumi.String(config.namespace),
		},
		Spec: &appsv1.DeploymentSpecArgs{
			Selector: &metav1.LabelSelectorArgs{
				MatchLabels: pulumi.StringMap{
					"app": pulumi.String(fmt.Sprintf("%s-api-server", config.name)),
				},
			},
			Template: &corev1.PodTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Labels: pulumi.StringMap{
						"app": pulumi.String(fmt.Sprintf("%s-api-server", config.name)),
					},
				},
				Spec: &corev1.PodSpecArgs{
					Containers: containerArray(&ContainerConfig{
            name: config.name,
            image: config.image,
            tag: config.tag,
            logLevel: config.logLevel,
            configMapName: config.logLevel,
            secretName: config.secretName,
            port: config.port,
            allowedOrigins: config.allowedOrigins,
          }),
				},
			},
		},
	})
	if err != nil {
		return err
	}

	// Create service
	_, err = corev1.NewService(ctx, fmt.Sprintf("%s-api", config.name), &corev1.ServiceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(fmt.Sprintf("%s-api", config.name)),
			Namespace: pulumi.String(config.namespace),
		},
		Spec: &corev1.ServiceSpecArgs{
			Selector: pulumi.StringMap{
				"app": pulumi.String(fmt.Sprintf("%s-api-server", config.name)),
			},
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Port:       pulumi.Int(config.port),
					TargetPort: pulumi.Int(config.port),
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

