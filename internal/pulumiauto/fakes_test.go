package pulumiauto

import (
	"context"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
)

// Shared step names for the recorded sequence logs.
const (
	stepPreflight = "preflight"
	stepOpen      = "open"
	stepPreview   = "preview"
	stepUp        = "up"
)

// fakeStack records every call and replays canned results/events.
type fakeStack struct {
	name       string
	previewRes auto.PreviewResult
	upRes      auto.UpResult
	previewEv  []events.EngineEvent // pushed on the channel before return
	upEv       []events.EngineEvent
	configs    map[string]auto.ConfigValue
	outputs    auto.OutputMap
	log        *[]string // shared sequence log
}

func (f *fakeStack) record(step string) {
	if f.log != nil {
		*f.log = append(*f.log, step)
	}
}

func (f *fakeStack) Name() string { return f.name }

func (f *fakeStack) Preview(_ context.Context, ch chan<- events.EngineEvent) (auto.PreviewResult, error) {
	f.record(stepPreview)
	for _, e := range f.previewEv {
		ch <- e
	}
	return f.previewRes, nil
}

func (f *fakeStack) Up(_ context.Context, ch chan<- events.EngineEvent) (auto.UpResult, error) {
	f.record(stepUp)
	for _, e := range f.upEv {
		ch <- e
	}
	return f.upRes, nil
}

func (f *fakeStack) Destroy(context.Context) error {
	f.record("destroy")
	return nil
}

func (f *fakeStack) SetConfig(_ context.Context, key string, val auto.ConfigValue) error {
	f.record("set-config:" + key)
	if f.configs == nil {
		f.configs = map[string]auto.ConfigValue{}
	}
	f.configs[key] = val
	return nil
}

func (f *fakeStack) GetAllConfig(context.Context) (auto.ConfigMap, error) {
	return f.configs, nil
}

func (f *fakeStack) Outputs(context.Context) (auto.OutputMap, error) {
	return f.outputs, nil
}
