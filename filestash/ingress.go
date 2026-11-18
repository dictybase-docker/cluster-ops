package main

import (
	"fmt"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	O "github.com/IBM/fp-go/v2/option"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	nginxPrefix   = "nginx.ingress.kubernetes.io/"
	adminPath     = "/admin"
	transferLimit = "3600"
)

// CreateIngress creates the nginx Ingress mode (clusters with no Gateway
// stack). A second Ingress redirects /admin to /, so the internet cannot
// reach the admin console. Gateway clusters keep ingress.enabled=false.
func (f *Filestash) CreateIngress(ctx *pulumi.Context) error {
	return F.Pipe2(
		f.Config.Ingress,
		O.FromPredicate(isIngressEnabled),
		O.Fold(F.Constant[error](nil), F.Bind1st(f.createIngresses, ctx)),
	)
}

func isIngressEnabled(ing IngressConfig) bool {
	return ing.Enabled
}

func (f *Filestash) createIngresses(ctx *pulumi.Context, _ IngressConfig) error {
	if err := f.newIngress(ctx, "ingress", f.Config.Name, "/", transferAnnotations()); err != nil {
		return err
	}
	return f.newIngress(ctx, "admin-block", f.resourceName("admin-block"), adminPath, pulumi.StringMap{
		nginxPrefix + "permanent-redirect": pulumi.String("/"),
	})
}

// transferAnnotations turn off the nginx body limit and buffering and raise
// the timeouts. A 60-second proxy timeout breaks large uploads.
func transferAnnotations() pulumi.StringMap {
	return pulumi.StringMap{
		nginxPrefix + "proxy-body-size":         pulumi.String("0"),
		nginxPrefix + "proxy-request-buffering": pulumi.String("off"),
		nginxPrefix + "proxy-read-timeout":      pulumi.String(transferLimit),
		nginxPrefix + "proxy-send-timeout":      pulumi.String(transferLimit),
	}
}

func (f *Filestash) newIngress(
	ctx *pulumi.Context,
	suffix, name, path string,
	annotations pulumi.StringMap,
) error {
	cfg := f.Config
	_, err := networkingv1.NewIngress(ctx, f.resourceName(suffix), &networkingv1.IngressArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:        pulumi.String(name),
			Namespace:   pulumi.String(cfg.Namespace),
			Labels:      pulumi.StringMap{cfg.Ingress.Label.Name: pulumi.String(cfg.Ingress.Label.Value)},
			Annotations: annotations,
		},
		Spec: &networkingv1.IngressSpecArgs{
			IngressClassName: pulumi.String(cfg.Ingress.ClassName),
			Tls: networkingv1.IngressTLSArray{
				&networkingv1.IngressTLSArgs{
					Hosts:      pulumi.ToStringArray(cfg.Ingress.Hosts),
					SecretName: pulumi.String(cfg.Ingress.TLSSecret),
				},
			},
			Rules: networkingv1.IngressRuleArray(F.Pipe1(cfg.Ingress.Hosts, A.Map(F.Bind1st(f.ingressRule, path)))),
		},
	})
	if err != nil {
		return fmt.Errorf("error creating filestash ingress %s: %w", name, err)
	}
	return nil
}

func (f *Filestash) ingressRule(path, host string) networkingv1.IngressRuleInput {
	cfg := f.Config
	return &networkingv1.IngressRuleArgs{
		Host: pulumi.String(host),
		Http: &networkingv1.HTTPIngressRuleValueArgs{
			Paths: networkingv1.HTTPIngressPathArray{
				&networkingv1.HTTPIngressPathArgs{
					Path:     pulumi.String(path),
					PathType: pulumi.String("Prefix"),
					Backend: &networkingv1.IngressBackendArgs{
						Service: &networkingv1.IngressServiceBackendArgs{
							Name: pulumi.String(cfg.Name),
							Port: &networkingv1.ServiceBackendPortArgs{Number: pulumi.Int(cfg.Port)},
						},
					},
				},
			},
		},
	}
}
