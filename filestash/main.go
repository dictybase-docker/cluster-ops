package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	nameLabel   = "app.kubernetes.io/name"
	statePath   = "/app/data/state"
	cachePath   = "/app/data/cache"
	seedPath    = "/seed"
	stateVolume = "state"
	cacheVolume = "cache"
	seedVolume  = "seed"
)

// Filestash installs one Filestash pod with its state PVC.
type Filestash struct {
	Config *FilestashConfig
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}
	return NewFilestash(cfg).Install(ctx)
}

func NewFilestash(cfg *FilestashConfig) *Filestash {
	cfg.ApplyDefaults()
	return &Filestash{Config: cfg}
}

func (f *Filestash) Install(ctx *pulumi.Context) error {
	seed, err := f.CreateSeedConfigMap(ctx)
	if err != nil {
		return err
	}
	pvc, err := f.CreateStatePVC(ctx)
	if err != nil {
		return err
	}
	deployment, err := f.CreateDeployment(ctx, seed, pvc)
	if err != nil {
		return err
	}
	if err := f.CreateService(ctx, deployment); err != nil {
		return err
	}
	return f.CreateIngress(ctx)
}

func (f *Filestash) labels() pulumi.StringMap {
	return pulumi.StringMap{nameLabel: pulumi.String(f.Config.Name)}
}

func (f *Filestash) resourceName(suffix string) string {
	return fmt.Sprintf("%s-%s", f.Config.Name, suffix)
}
