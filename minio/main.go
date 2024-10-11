package main

import (
	"fmt"

	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ChartConfig struct {
	Name       string
	Repository string
	Version    string
}

type ImageConfig struct {
	Tag string
}

type SecretConfig struct {
	Name     string `json:"name"`
	PassKey  string `json:"passKey"`
	Password string `json:"password"`
	UserKey  string `json:"userKey"`
	UserName string `json:"userName"`
}

type StorageConfig struct {
	Class string
	Size  string
}

type APIIngressConfig struct {
	Enabled          bool
	Hostname         string
	IngressClassName string
	Label            struct {
		Name  string
		Value string
	}
	Service struct {
		Name string
		Port int
	}
	TLS struct {
		Hosts      []string
		SecretName string
	}
}

type MinioConfig struct {
	Chart      ChartConfig
	Image      ImageConfig
	Namespace  string
	Secret     SecretConfig
	Storage    StorageConfig
	WebUI      bool `json:"webui"`
	APIIngress APIIngressConfig
}

type Minio struct {
	Config *MinioConfig
}

func ReadConfig(ctx *pulumi.Context) (*MinioConfig, error) {
	conf := config.New(ctx, "")
	minioConfig := &MinioConfig{}
	if err := conf.TryObject("properties", minioConfig); err != nil {
		return nil, fmt.Errorf("failed to read minio config: %w", err)
	}
	return minioConfig, nil
}

func NewMinio(config *MinioConfig) *Minio {
	return &Minio{
		Config: config,
	}
}

func (mno *Minio) createSecret(ctx *pulumi.Context) (*corev1.Secret, error) {
	return corev1.NewSecret(
		ctx,
		mno.Config.Secret.Name,
		&corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(mno.Config.Secret.Name),
				Namespace: pulumi.String(mno.Config.Namespace),
			},
			StringData: pulumi.StringMap{
				mno.Config.Secret.UserKey: pulumi.String(
					mno.Config.Secret.UserName,
				),
				mno.Config.Secret.PassKey: pulumi.String(
					mno.Config.Secret.Password,
				),
			},
		},
	)
}

func (mno *Minio) getHelmValues() pulumi.Map {
	return pulumi.Map{
		"image": pulumi.Map{
			"tag": pulumi.String(mno.Config.Image.Tag),
		},
		"auth": pulumi.Map{
			"existingSecret":        pulumi.String(mno.Config.Secret.Name),
			"rootUserSecretKey":     pulumi.String(mno.Config.Secret.UserKey),
			"rootPasswordSecretKey": pulumi.String(mno.Config.Secret.PassKey),
		},
		"persistence": pulumi.Map{
			"storageClass": pulumi.String(mno.Config.Storage.Class),
			"size":         pulumi.String(mno.Config.Storage.Size),
		},
		"disableWebUI": pulumi.Bool(!mno.Config.WebUI),
	}
}

func (mno *Minio) installHelmChart(
	ctx *pulumi.Context,
	secret *corev1.Secret,
) error {
	_, err := helm.NewRelease(ctx, "minio", &helm.ReleaseArgs{
		Name:      pulumi.String(mno.Config.Chart.Name),
		Chart:     pulumi.String(mno.Config.Chart.Name),
		Version:   pulumi.String(mno.Config.Chart.Version),
		Namespace: pulumi.String(mno.Config.Namespace),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(mno.Config.Chart.Repository),
		},
		Values: mno.getHelmValues(),
	}, pulumi.DependsOn([]pulumi.Resource{secret}))
	if err != nil {
		return fmt.Errorf("failed to install Helm chart: %w", err)
	}
	return nil
}

func (mno *Minio) createIngress(ctx *pulumi.Context) error {
	if !mno.Config.APIIngress.Enabled {
		return nil
	}

	ingressName := "minio-api-ingress"
	_, err := networkingv1.NewIngress(ctx, ingressName, &networkingv1.IngressArgs{
		Metadata: mno.createIngressMetadata(ingressName),
		Spec:     mno.createIngressSpec(),
	})

	return err
}

func (mno *Minio) createIngressMetadata(name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(mno.Config.Namespace),
		Labels: pulumi.StringMap{
			mno.Config.APIIngress.Label.Name: pulumi.String(mno.Config.APIIngress.Label.Value),
		},
	}
}

func (mno *Minio) createIngressSpec() *networkingv1.IngressSpecArgs {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String(mno.Config.APIIngress.IngressClassName),
		Rules:            mno.createIngressRules(),
		Tls:              mno.createIngressTLS(),
	}
}

func (mno *Minio) createIngressRules() networkingv1.IngressRuleArray {
	return networkingv1.IngressRuleArray{
		&networkingv1.IngressRuleArgs{
			Host: pulumi.String(mno.Config.APIIngress.Hostname),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: mno.createIngressPaths(),
			},
		},
	}
}

func (mno *Minio) createIngressPaths() networkingv1.HTTPIngressPathArray {
	return networkingv1.HTTPIngressPathArray{
		&networkingv1.HTTPIngressPathArgs{
			Path:     pulumi.String("/"),
			PathType: pulumi.String("Prefix"),
			Backend:  mno.createIngressBackend(),
		},
	}
}

func (mno *Minio) createIngressBackend() *networkingv1.IngressBackendArgs {
	return &networkingv1.IngressBackendArgs{
		Service: &networkingv1.IngressServiceBackendArgs{
			Name: pulumi.String(mno.Config.APIIngress.Service.Name),
			Port: &networkingv1.ServiceBackendPortArgs{
				Number: pulumi.Int(mno.Config.APIIngress.Service.Port),
			},
		},
	}
}

func (mno *Minio) createIngressTLS() networkingv1.IngressTLSArray {
	return networkingv1.IngressTLSArray{
		&networkingv1.IngressTLSArgs{
			Hosts:      pulumi.ToStringArray(mno.Config.APIIngress.TLS.Hosts),
			SecretName: pulumi.String(mno.Config.APIIngress.TLS.SecretName),
		},
	}
}

func (mno *Minio) Install(ctx *pulumi.Context) error {
	secret, err := mno.createSecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Minio secret: %w", err)
	}

	if err := mno.installHelmChart(ctx, secret); err != nil {
		return err
	}

	if err := mno.createIngress(ctx); err != nil {
		return fmt.Errorf("failed to create Minio API ingress: %w", err)
	}

	return nil
}

func Run(ctx *pulumi.Context) error {
	minioConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	minio := NewMinio(minioConfig)

	if err := minio.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
