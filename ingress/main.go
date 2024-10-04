package main

import (
	"fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
)

type Ingresses struct {
	Config *Config
}

type Config struct {
	Namespace       string
	GraphqlIngress  IngressConfig
	FrontendIngress IngressConfig
}

type IngressConfig struct {
	Issuer   string
  TlsSecret string 
	Hosts    []string
	Services []struct {
		Name string
		Port int
		Path string
	}
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
  config, err := ReadConfig(ctx)

  if err != nil {
    return err
  }

  ingresses := &Ingresses{Config: config}

  // Create GraphQL Ingress
  if _, err := CreateIngress(ctx, "graphql", ingresses.Config.Namespace, ingresses.Config.GraphqlIngress); err != nil {
    return err
  }

  // Create Frontend Ingress
  if _, err := CreateIngress(ctx, "frontend", ingresses.Config.Namespace, ingresses.Config.FrontendIngress); err != nil {
    return err
  }

  return nil
}
  
func ReadConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "ingress")
	var ingressConfig Config
	if err := conf.TryObject("properties", &ingressConfig); err != nil {
		return nil, fmt.Errorf("failed to read ingress config: %w", err)
	}
	return &ingressConfig, nil
}

func CreateIngressRuleArray(config IngressConfig) networkingv1.IngressRuleArray {
	var rules networkingv1.IngressRuleArray

	for _, host := range config.Hosts {
		var paths networkingv1.HTTPIngressPathArray
		for _, service := range config.Services {
			paths = append(paths, &networkingv1.HTTPIngressPathArgs{
				Path:     pulumi.String(service.Path),
				PathType: pulumi.String("Prefix"),
				Backend: &networkingv1.IngressBackendArgs{
					Service: &networkingv1.IngressServiceBackendArgs{
						Name: pulumi.String(service.Name),
						Port: &networkingv1.ServiceBackendPortArgs{
							Number: pulumi.Int(service.Port),
						},
					},
				},
			})
		}

		rules = append(rules, &networkingv1.IngressRuleArgs{
			Host: pulumi.String(host),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: paths,
			},
		})
	}

	return rules
}

func CreateIngressMetadata(name string, namespace string, issuer string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fmt.Sprintf("%s-ingress", name)),
		Namespace: pulumi.String(namespace),
		Annotations: pulumi.StringMap{
			"cert-manager.io/cluster-issuer": pulumi.String(issuer),
		},
	}
}

func CreateIngressSpec(config IngressConfig) *networkingv1.IngressSpecArgs {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls: networkingv1.IngressTLSArray{
			&networkingv1.IngressTLSArgs{
				Hosts:      pulumi.ToStringArray(config.Hosts),
				SecretName: pulumi.String(config.TlsSecret),
			},
		},
		Rules: CreateIngressRuleArray(config),
	}
}

func CreateIngress(ctx *pulumi.Context, name string, namespace string, config IngressConfig) (*networkingv1.Ingress, error) {
	ingress, err := networkingv1.NewIngress(ctx, name, &networkingv1.IngressArgs{
		Metadata: CreateIngressMetadata(name, namespace, config.Issuer),
		Spec:     CreateIngressSpec(config),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create %s ingress: %w", name, err)
	}
	return ingress, nil
}
