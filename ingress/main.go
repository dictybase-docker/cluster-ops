// Package main provides functionality to create and manage Kubernetes Ingress resources using Pulumi.
package main

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Ingresses holds the configuration for multiple Ingress resources.
type Ingresses struct {
	Config *Config
}

// Config represents the overall configuration for the Ingress resources.
type Config struct {
	Namespace       string
	GraphqlIngress  IngressConfig
	FrontendIngress IngressConfig
}

// IngressConfig holds the configuration for a single Ingress resource.
type IngressConfig struct {
	Issuer    string
	TlsSecret string
	Hosts     []string
	Services  []struct {
		Name string
		Port int
		Path string
	}
}

// main is the entry point of the program.
func main() {
	pulumi.Run(Run)
}

// Run is the main function that creates the Ingress resources.
func Run(ctx *pulumi.Context) error {
	config, err := ReadConfig(ctx)

	if err != nil {
		return err
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

// ReadConfig reads the configuration from Pulumi config and returns a Config struct.
func ReadConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "ingress")
	var ingressConfig Config
	if err := conf.TryObject("properties", &ingressConfig); err != nil {
		return nil, fmt.Errorf("failed to read ingress config: %w", err)
	}
	return &ingressConfig, nil
}

// createIngress creates a new Ingress resource with the given configuration.
func createIngress(
	ctx *pulumi.Context,
	name string,
	namespace string,
	config IngressConfig,
) (*networkingv1.Ingress, error) {
	ingress, err := networkingv1.NewIngress(
		ctx,
		fmt.Sprintf("%s-ingress", name),
		&networkingv1.IngressArgs{
			Metadata: createIngressMetadata(name, namespace, config.Issuer),
			Spec:     createIngressSpec(config),
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create %s ingress: %w", name, err)
	}
	return ingress, nil
}

// createIngressMetadata creates the metadata for an Ingress resource.
func createIngressMetadata(
	name string,
	namespace string,
	issuer string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fmt.Sprintf("%s-ingress", name)),
		Namespace: pulumi.String(namespace),
		Annotations: pulumi.StringMap{
			"cert-manager.io/cluster-issuer": pulumi.String(issuer),
		},
	}
}

// createIngressSpec creates the specification for an Ingress resource.
func createIngressSpec(config IngressConfig) *networkingv1.IngressSpecArgs {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls: networkingv1.IngressTLSArray{
			&networkingv1.IngressTLSArgs{
				Hosts:      pulumi.ToStringArray(config.Hosts),
				SecretName: pulumi.String(config.TlsSecret),
			},
		},
		Rules: createIngressRuleArray(config),
	}
}

// createIngressRuleArray creates an array of IngressRule objects based on the provided IngressConfig.
func createIngressRuleArray(
	config IngressConfig,
) networkingv1.IngressRuleArray {
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
