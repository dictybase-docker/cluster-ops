package main

import (
	"fmt"

	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type KcertConfig struct {
	Chart struct {
		Name       string
		Repository string
		Values     struct {
			AcmeDirURL        string `json:"acmeDirUrl"`
			AcmeEmail         string `json:"acmeEmail"`
			AcmeTermsAccepted bool   `json:"acmeTermsAccepted"`
			Debug             bool   `json:"debug"`
		}
		Version string
	}
	Namespace string
}

type Kcert struct {
	Config *KcertConfig
}

func ReadConfig(ctx *pulumi.Context) (*KcertConfig, error) {
	conf := config.New(ctx, "")
	kcertConf := &KcertConfig{}
	if err := conf.TryObject("properties", kcertConf); err != nil {
		return nil, fmt.Errorf("failed to read kcert config: %w", err)
	}
	return kcertConf, nil
}

func NewKcert(config *KcertConfig) *Kcert {
	return &Kcert{
		Config: config,
	}
}

func (kcert *Kcert) Install(ctx *pulumi.Context) error {
	_, err := helm.NewRelease(ctx, "kcert", &helm.ReleaseArgs{
		Chart:   pulumi.String(kcert.Config.Chart.Name),
		Version: pulumi.String(kcert.Config.Chart.Version),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(kcert.Config.Chart.Repository),
		},
		Namespace: pulumi.String(kcert.Config.Namespace),
		Values:    kcert.createHelmValues(),
	})
	if err != nil {
		return fmt.Errorf("error creating Helm Release: %w", err)
	}
	return nil
}

func (kcert *Kcert) createHelmValues() pulumi.Map {
	return pulumi.Map{
		"acmeDirUrl": pulumi.String(
			kcert.Config.Chart.Values.AcmeDirURL,
		),
		"acmeTermsAccepted": pulumi.Bool(
			kcert.Config.Chart.Values.AcmeTermsAccepted,
		),
		"debug":     pulumi.Bool(kcert.Config.Chart.Values.Debug),
		"acmeEmail": pulumi.String(kcert.Config.Chart.Values.AcmeEmail),
	}
}

func Run(ctx *pulumi.Context) error {
	kcertConf, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	kcert := NewKcert(kcertConf)

	if err := kcert.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
