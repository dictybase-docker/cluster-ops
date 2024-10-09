package main

import (
	"fmt"

	certmanagerv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/certmanager/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type CertIssuerConfig struct {
	Namespace  string `pulumi:"namespace"`
	Email      string `pulumi:"email"`
	Server     string `pulumi:"server"`
	SecretName string `pulumi:"secretName"`
}

type CertIssuer struct {
	Config *CertIssuerConfig
}

func ReadConfig(ctx *pulumi.Context) (*CertIssuerConfig, error) {
	conf := config.New(ctx, "")
	certIssuerConfig := &CertIssuerConfig{}
	if err := conf.TryObject("properties", certIssuerConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read cert-issuer config: %w",
			err,
		)
	}
	return certIssuerConfig, nil
}

func NewCertIssuer(config *CertIssuerConfig) *CertIssuer {
	return &CertIssuer{
		Config: config,
	}
}

func (ci *CertIssuer) Install(ctx *pulumi.Context) error {
	_, err := certmanagerv1.NewIssuer(
		ctx,
		"letsencrypt-issuer",
		&certmanagerv1.IssuerArgs{
			Metadata: ci.createMetadata(),
			Spec:     ci.createIssuerSpec(),
		},
	)
	if err != nil {
		return fmt.Errorf("error creating Issuer: %w", err)
	}
	return nil
}

func (ci *CertIssuer) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String("letsencrypt-issuer"),
		Namespace: pulumi.String(ci.Config.Namespace),
	}
}

func (ci *CertIssuer) createIssuerSpec() *certmanagerv1.IssuerSpecArgs {
	return &certmanagerv1.IssuerSpecArgs{
		Acme: ci.createAcmeArgs(),
	}
}

func (ci *CertIssuer) createAcmeArgs() *certmanagerv1.IssuerSpecAcmeArgs {
	return &certmanagerv1.IssuerSpecAcmeArgs{
		Solvers:             ci.createSolvers(),
		Server:              pulumi.String(ci.Config.Server),
		Email:               pulumi.String(ci.Config.Email),
		PrivateKeySecretRef: ci.createPrivateKeySecretRef(),
	}
}

func (ci *CertIssuer) createSolvers() certmanagerv1.IssuerSpecAcmeSolversArray {
	return certmanagerv1.IssuerSpecAcmeSolversArray{
		&certmanagerv1.IssuerSpecAcmeSolversArgs{
			Http01: ci.createHTTP01Solver(),
		},
	}
}

func (ci *CertIssuer) createHTTP01Solver() *certmanagerv1.IssuerSpecAcmeSolversHttp01Args {
	return &certmanagerv1.IssuerSpecAcmeSolversHttp01Args{
		Ingress: &certmanagerv1.IssuerSpecAcmeSolversHttp01IngressArgs{
			Class: pulumi.String("nginx"),
		},
	}
}

func (ci *CertIssuer) createPrivateKeySecretRef() *certmanagerv1.IssuerSpecAcmePrivateKeySecretRefArgs {
	return &certmanagerv1.IssuerSpecAcmePrivateKeySecretRefArgs{
		Name: pulumi.String(ci.Config.SecretName),
	}
}

func Run(ctx *pulumi.Context) error {
	certIssuerConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	certIssuer := NewCertIssuer(certIssuerConfig)

	if err := certIssuer.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
