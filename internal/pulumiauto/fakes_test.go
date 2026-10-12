package pulumiauto

import (
	"context"
	"strings"

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

// stepName strips the ":<planPath>" suffix the record lines carry (plan
// paths are the only suffixes with a slash).
func stepName(recorded string) string {
	name, rest, found := strings.Cut(recorded, ":")
	if found && strings.Contains(rest, "/") {
		return name
	}
	return recorded
}

// stepNames normalizes a recorded sequence for order assertions.
func stepNames(log []string) []string {
	names := make([]string, 0, len(log))
	for _, entry := range log {
		names = append(names, stepName(entry))
	}
	return names
}

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

func (f *fakeStack) Preview(
	_ context.Context, ch chan<- events.EngineEvent, planPath string,
) (auto.PreviewResult, error) {
	f.record(stepPreview + ":" + planPath)
	for _, e := range f.previewEv {
		ch <- e
	}
	return f.previewRes, nil
}

func (f *fakeStack) Up(_ context.Context, ch chan<- events.EngineEvent, planPath string) (auto.UpResult, error) {
	f.record(stepUp + ":" + planPath)
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
