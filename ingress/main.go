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
			return fmt.Errorf("failed to read config: %w", err)
		}

		ingresses := &Ingresses{Config: config}

		// Create GraphQL Ingress
		if _, err := createIngress(ctx, "graphql", ingresses.Config.Namespace, ingresses.Config.GraphqlIngress); err != nil {
			return err
		}

		// Create Frontend Ingress
		if _, err := createIngress(ctx, "frontend", ingresses.Config.Namespace, ingresses.Config.FrontendIngress); err != nil {
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

func createIngress(ctx *pulumi.Context, name string, namespace string, config IngressConfig) (*networkingv1.Ingress, error) {
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

	return networkingv1.NewIngress(ctx, fmt.Sprintf("%s-ingress", name), &networkingv1.IngressArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String(fmt.Sprintf("%s-ingress", name)),
      Namespace: pulumi.String(namespace),
			Annotations: pulumi.StringMap{
				"kubernetes.io/ingress.class":    pulumi.String("nginx"),
				"cert-manager.io/cluster-issuer": pulumi.String(config.Issuer),
			},
		},
		Spec: &networkingv1.IngressSpecArgs{
			Tls: networkingv1.IngressTLSArray{
				&networkingv1.IngressTLSArgs{
					Hosts:      pulumi.ToStringArray(config.Hosts),
					SecretName: pulumi.String(fmt.Sprintf("%s-tls-secret", name)),
				},
			},
			Rules: rules,
		},
	})
}
