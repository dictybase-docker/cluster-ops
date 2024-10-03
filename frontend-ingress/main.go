package main

import (
	"fmt"

	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Config struct {
	Namespace    string
	Name         string
	Secret       string
	Issuer       string
	Service      string
	Path         string
	BackendHosts []string
}

type FrontendIngress struct {
	Config *Config
}

func (fi *FrontendIngress) CreateIngress(ctx *pulumi.Context) (*networkingv1.Ingress, error) {
	ingress, err := networkingv1.NewIngress(ctx, fi.Config.Name, &networkingv1.IngressArgs{
		Metadata: fi.Metadata(),
		Spec:     fi.Spec(),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create Ingress resource: %w", err)
	}

	return ingress, nil
}

// Metadata returns the Metadata for the Ingress resource.
func (fi *FrontendIngress) Metadata() metav1.ObjectMetaPtrInput {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fi.Config.Name),
		Namespace: pulumi.String(fi.Config.Namespace),
		Annotations: pulumi.StringMap{
			"cert-manager.io/issuer": pulumi.String(fi.Config.Issuer),
		},
	}
}

// Spec returns the Spec for the Ingress resource.
func (fi *FrontendIngress) Spec() networkingv1.IngressSpecPtrInput {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls:              fi.Tls(),
		Rules:            fi.Rules(),
	}
}

// Tls returns the TLS configuration for the Ingress resource.
func (fi *FrontendIngress) Tls() networkingv1.IngressTLSArrayInput {
	return networkingv1.IngressTLSArray{
		&networkingv1.IngressTLSArgs{
			SecretName: pulumi.String(fi.Config.Secret),
			Hosts:      pulumi.ToStringArray(fi.Config.BackendHosts),
		},
	}
}

// Rules returns the Rules for the Ingress resource.
func (fi *FrontendIngress) Rules() networkingv1.IngressRuleArrayInput {
	var rules networkingv1.IngressRuleArray
	for _, h := range fi.Config.BackendHosts {
		rules = append(rules, &networkingv1.IngressRuleArgs{
			Host: pulumi.String(h),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: networkingv1.HTTPIngressPathArray{
					&networkingv1.HTTPIngressPathArgs{
						Path:     pulumi.String(fi.Config.Path),
						PathType: pulumi.String("Prefix"),
						Backend: &networkingv1.IngressBackendArgs{
							Service: &networkingv1.IngressServiceBackendArgs{
								Name: pulumi.String(fi.Config.Service),
								Port: &networkingv1.ServiceBackendPortArgs{
									Number: pulumi.Int(3000),
								},
							},
						},
					},
				},
			},
		})
	}
	return rules
}

func ReadConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "")
	config := &Config{}
	if err := conf.TryObject("properties", config); err != nil {
		return nil, fmt.Errorf("failed to read frontend-ingress config: %w", err)
	}
	return config, nil
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
		config, err := ReadConfig(ctx)
		if err != nil {
			return err
		}

		fe := &FrontendIngress{
			Config: config,
		}

		ingress, err := fe.CreateIngress(ctx)
		if err != nil {
			return err
		}

		ctx.Export("ingressName", ingress.Metadata.Name())
		return nil
	}
