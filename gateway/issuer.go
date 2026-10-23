package main

import (
	"fmt"

	certmanagerv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/certmanager/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (g *GatewayStack) createIssuer(ctx *pulumi.Context) (*certmanagerv1.Issuer, error) {
	cfg := g.Config
	issuer, err := certmanagerv1.NewIssuer(
		ctx,
		cfg.Issuer.Name,
		&certmanagerv1.IssuerArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(cfg.Issuer.Name),
				Namespace: pulumi.String(cfg.Namespace),
			},
			Spec: g.createIssuerSpec(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create cert-manager Issuer %s: %w", cfg.Issuer.Name, err)
	}
	return issuer, nil
}

func (g *GatewayStack) createIssuerSpec() *certmanagerv1.IssuerSpecArgs {
	cfg := g.Config
	return &certmanagerv1.IssuerSpecArgs{
		Acme: &certmanagerv1.IssuerSpecAcmeArgs{
			Email:  pulumi.String(cfg.Issuer.Email),
			Server: pulumi.String(cfg.Issuer.Server),
			PrivateKeySecretRef: &certmanagerv1.IssuerSpecAcmePrivateKeySecretRefArgs{
				Name: pulumi.String(cfg.Issuer.SecretName),
			},
			Solvers: certmanagerv1.IssuerSpecAcmeSolversArray{
				&certmanagerv1.IssuerSpecAcmeSolversArgs{
					Http01: &certmanagerv1.IssuerSpecAcmeSolversHttp01Args{
						GatewayHTTPRoute: &certmanagerv1.IssuerSpecAcmeSolversHttp01GatewayHttprouteArgs{
							ParentRefs: certmanagerv1.IssuerSpecAcmeSolversHttp01GatewayHttprouteParentRefsArray{
								&certmanagerv1.IssuerSpecAcmeSolversHttp01GatewayHttprouteParentRefsArgs{
									Kind:      pulumi.String("Gateway"),
									Name:      pulumi.String(cfg.Name),
									Namespace: pulumi.String(cfg.Namespace),
								},
							},
						},
					},
				},
			},
		},
	}
}
