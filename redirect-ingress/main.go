package main

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type RedirectIngressConfig struct {
	DummyService struct {
		Name string
		Port int
	}
	Hosts struct {
		Destination string
		From        string
	}
	Label struct {
		Name  string
		Value string
	}
	Name      string
	Namespace string
	TLSName   string
}

type RedirectIngress struct {
	Config *RedirectIngressConfig
}

func ReadConfig(ctx *pulumi.Context) (*RedirectIngressConfig, error) {
	conf := config.New(ctx, "")
	redirectConfig := &RedirectIngressConfig{}
	if err := conf.TryObject("properties", redirectConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read redirect-ingress config: %w",
			err,
		)
	}
	return redirectConfig, nil
}

func NewRedirectIngress(config *RedirectIngressConfig) *RedirectIngress {
	return &RedirectIngress{
		Config: config,
	}
}

func (ri *RedirectIngress) Install(ctx *pulumi.Context) error {
	err := ri.createIngress(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (ri *RedirectIngress) createIngress(ctx *pulumi.Context) error {
	_, err := networkingv1.NewIngress(
		ctx,
		ri.Config.Name,
		&networkingv1.IngressArgs{
			Metadata: ri.createMetadata(),
			Spec:     ri.createIngressSpec(),
		},
	)
	if err != nil {
		return fmt.Errorf("error creating Ingress: %w", err)
	}
	return nil
}

func (ri *RedirectIngress) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(ri.Config.Name),
		Namespace: pulumi.String(ri.Config.Namespace),
		Annotations: pulumi.StringMap{
			"nginx.ingress.kubernetes.io/permanent-redirect": pulumi.String(
				fmt.Sprintf(
					"https://%s$request_uri",
					ri.Config.Hosts.Destination,
				),
			),
		},
		Labels: pulumi.StringMap{
			ri.Config.Label.Name: pulumi.String(ri.Config.Label.Value),
		},
	}
}

func (ri *RedirectIngress) createIngressSpec() *networkingv1.IngressSpecArgs {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls:              ri.createTLS(),
		Rules:            ri.createRules(),
	}
}

func (ri *RedirectIngress) createTLS() networkingv1.IngressTLSArray {
	return networkingv1.IngressTLSArray{
		&networkingv1.IngressTLSArgs{
			Hosts:      pulumi.StringArray{pulumi.String(ri.Config.Hosts.From)},
			SecretName: pulumi.String(ri.Config.TLSName),
		},
	}
}

func (ri *RedirectIngress) createRules() networkingv1.IngressRuleArray {
	return networkingv1.IngressRuleArray{
		&networkingv1.IngressRuleArgs{
			Host: pulumi.String(ri.Config.Hosts.From),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: networkingv1.HTTPIngressPathArray{
					ri.createPath(),
				},
			},
		},
	}
}

func (ri *RedirectIngress) createPath() *networkingv1.HTTPIngressPathArgs {
	return &networkingv1.HTTPIngressPathArgs{
		Path:     pulumi.String("/"),
		PathType: pulumi.String("Prefix"),
		Backend:  ri.createBackend(),
	}
}

func (ri *RedirectIngress) createBackend() *networkingv1.IngressBackendArgs {
	return &networkingv1.IngressBackendArgs{
		Service: &networkingv1.IngressServiceBackendArgs{
			Name: pulumi.String(ri.Config.DummyService.Name),
			Port: &networkingv1.ServiceBackendPortArgs{
				Number: pulumi.Int(ri.Config.DummyService.Port),
			},
		},
	}
}

func Run(ctx *pulumi.Context) error {
	redirectConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	redirectIngress := NewRedirectIngress(redirectConfig)

	if err := redirectIngress.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
