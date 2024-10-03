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
		ApiVersion: pulumi.String("networking.k8s.io/v1"),
		Kind:       pulumi.String("Ingress"),
		Metadata: fi.metadata(), 
    Spec: fi.spec(),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create Ingress resource: %w", err)
	}

	return ingress, nil
}

// metadata returns the metadata for the Ingress resource.
func (fi *FrontendIngress) metadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
			Name:      pulumi.String(fi.Config.Name),
			Namespace: pulumi.String(fi.Config.Namespace),
      Annotations: pulumi.StringMap{
        "cert-manager.io/issuer": pulumi.String(fi.Config.Issuer),
      },
    }
}

// spec returns the spec for the Ingress resource.
func (fi *FrontendIngress) spec() *networkingv1.IngressSpecArgs {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls:              fi.tls(),
		Rules:            fi.rules(),
	}
}

// tls returns the TLS configuration for the Ingress resource.
func (fi *FrontendIngress) tls() *networkingv1.IngressTLSArray {
	return &networkingv1.IngressTLSArray{
		networkingv1.IngressTLSArgs{
			SecretName: pulumi.String(fi.Config.Secret),
			Hosts:      pulumi.ToStringArray(fi.Config.BackendHosts),
		},
	}
}

// rules returns the rules for the Ingress resource.
func (fi *FrontendIngress) rules() []map[string]interface{} {
	var result []map[string]interface{}
	for _, h := range fi.Config.BackendHosts {
		result = append(result, map[string]interface{}{
			"host": h,
			"http": fi.backendPaths(),
		})
	}
	return result
}

// backendPaths returns the backend paths for the Ingress resource.
func (fi *FrontendIngress) backendPaths() map[string]interface{} {
	return map[string]interface{}{
		"paths": []map[string]interface{}{
			{
				"pathType": "Prefix",
				"path":     fi.Config.Path,
				"backend": map[string]interface{}{
					"service": map[string]interface{}{
						"name": fi.Config.Service,
						"port": map[string]interface{}{
							"number": 3000,
						},
					},
				},
			},
		},
	}
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
	pulumi.Run(func(ctx *pulumi.Context) error {
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
	})
}
