package pulumiauto

import (
	"context"
	"fmt"
	"os"

	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"gopkg.in/yaml.v3"
)

// servicesFile mirrors config/services.yaml (list order is deploy order).
type servicesFile struct {
	Services []struct {
		App    string `yaml:"app"`
		Folder string `yaml:"folder"`
	} `yaml:"services"`
}

// LoadServices reads config/services.yaml in list order. A missing file is a
// named error, never an empty list.
func LoadServices(path string) ([]ServiceSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read services file: %w", err)
	}
	var raw servicesFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse services file %s: %w", path, err)
	}
	specs := make([]ServiceSpec, 0, len(raw.Services))
	for _, s := range raw.Services {
		specs = append(specs, ServiceSpec{App: s.App, Folder: s.Folder})
	}
	return specs, nil
}

type multiWork struct {
	opener   StackOpener
	target   DeployTarget
	services []ServiceSpec
	mode     DeployMode
}

// DeployBackendServices deploys every service in list order (serial). A
// failing service stops the loop: later services never start.
func DeployBackendServices(
	ctx context.Context, opener StackOpener, target DeployTarget,
	services []ServiceSpec, mode DeployMode,
) IOE.IOEither[error, []BootstrapResult] {
	w := multiWork{opener: opener, target: target, services: services, mode: mode}
	return w.deployFrom(ctx, 0, []BootstrapResult{})
}

func (w multiWork) deployFrom(
	ctx context.Context, idx int, acc []BootstrapResult,
) IOE.IOEither[error, []BootstrapResult] {
	if idx >= len(w.services) {
		return IOE.Of[error](acc)
	}
	svc := w.services[idx]
	target := w.target
	target.Folder, target.Stack = svc.Folder, svc.Stack
	return F.Pipe1(
		BootstrapService(ctx, w.opener, target, svc, w.mode),
		IOE.Chain(afterService(w, ctx, idx, acc)),
	)
}

func afterService(
	w multiWork, ctx context.Context, idx int, acc []BootstrapResult,
) func(BootstrapResult) IOE.IOEither[error, []BootstrapResult] {
	return func(r BootstrapResult) IOE.IOEither[error, []BootstrapResult] {
		return w.deployFrom(ctx, idx+1, append(acc, r))
	}
}
