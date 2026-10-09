package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"google.golang.org/api/option"
	"gopkg.in/yaml.v3"
)

// BucketAPI is the injected GCS surface (production: storageBucketAPI over
// *storage.Client) so converge behavior is testable without GCP.
type BucketAPI interface {
	Get(ctx context.Context, name string) (*storage.BucketAttrs, error) // typed NotFound via googleapi
	Create(ctx context.Context, cfg BucketConfig) error
	Update(ctx context.Context, name string, versioning bool, lifecycle storage.Lifecycle) error
}

// lifecycleActionDelete is the shared gcloud/storage lifecycle action type.
const lifecycleActionDelete = "Delete"

// BucketConfig is the desired bucket state.
type BucketConfig struct {
	Name, ProjectID, Location string
	Versioning                bool
	Lifecycle                 storage.Lifecycle
	CredFile                  string // "" = application default credentials
}

type bucketWork struct {
	api  BucketAPI
	cfg  BucketConfig
	mode MutationMode
}

// ConvergeBucket: describe → create | update-if-drift | unchanged. DryRun
// reports the intended action and calls no mutation seam.
func ConvergeBucket(api BucketAPI, cfg BucketConfig, mode MutationMode) IOE.IOEither[error, Result] {
	w := bucketWork{api: api, cfg: cfg, mode: mode}
	return F.Pipe1(w.probeBucket(), IOE.Chain(w.convergeFromProbe))
}

func (w bucketWork) name() string { return "gs://" + w.cfg.Name }

func (w bucketWork) probeBucket() IOE.IOEither[error, *storage.BucketAttrs] {
	return IOE.TryCatchError(func() (*storage.BucketAttrs, error) {
		return probeAttrs(w.api, w.cfg.Name)
	})
}

// probeAttrs describes the bucket, absorbing a typed NotFound into a nil
// attrs — "missing" is a probe outcome, not an error.
func probeAttrs(api BucketAPI, name string) (*storage.BucketAttrs, error) {
	attrs, err := api.Get(context.Background(), name)
	if err != nil && !IsNotFound(err) {
		return nil, fmt.Errorf("describe bucket gs://%s: %w", name, err)
	}
	return attrs, nil
}

func (w bucketWork) convergeFromProbe(attrs *storage.BucketAttrs) IOE.IOEither[error, Result] {
	if attrs == nil {
		return w.createBucket()
	}
	return w.updateIfDrift(attrs)
}

func (w bucketWork) createBucket() IOE.IOEither[error, Result] {
	if w.mode.DryRun {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionDryRun})
	}
	return F.Pipe1(
		IOE.TryCatchError(func() (struct{}, error) {
			return struct{}{}, w.api.Create(context.Background(), w.cfg)
		}),
		IOE.Map[error](w.createdResult),
	)
}

func (w bucketWork) createdResult(struct{}) Result {
	return Result{Name: w.name(), Action: ActionCreated}
}

func (w bucketWork) updateIfDrift(attrs *storage.BucketAttrs) IOE.IOEither[error, Result] {
	drift := attrs.VersioningEnabled != w.cfg.Versioning ||
		!lifecycleEqual(attrs.Lifecycle, w.cfg.Lifecycle)
	if !drift {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionUnchanged})
	}
	if w.mode.DryRun {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionDryRun})
	}
	return F.Pipe1(
		IOE.TryCatchError(func() (struct{}, error) {
			return struct{}{}, w.api.Update(
				context.Background(), w.cfg.Name, w.cfg.Versioning, w.cfg.Lifecycle)
		}),
		IOE.Map[error](w.updatedResult),
	)
}

func (w bucketWork) updatedResult(struct{}) Result {
	return Result{Name: w.name(), Action: ActionUpdated}
}

// lifecycleEqual compares lifecycle rules deterministically via canonical
// JSON bytes (storage.LifecycleRule is not safely ==/slices.Equal-able).
func lifecycleEqual(a, b storage.Lifecycle) bool {
	aj, aerr := json.Marshal(a)
	bj, berr := json.Marshal(b)
	return aerr == nil && berr == nil && bytes.Equal(aj, bj)
}

// ---- production adapter ----

// StorageBucketAPI adapts *storage.Client to BucketAPI.
type StorageBucketAPI struct {
	Client *storage.Client
}

func (a StorageBucketAPI) Get(ctx context.Context, name string) (*storage.BucketAttrs, error) {
	return a.Client.Bucket(name).Attrs(ctx)
}

func (a StorageBucketAPI) Create(ctx context.Context, cfg BucketConfig) error {
	attrs := &storage.BucketAttrs{
		Location:          cfg.Location,
		VersioningEnabled: cfg.Versioning,
		Lifecycle:         cfg.Lifecycle,
	}
	return a.Client.Bucket(cfg.Name).Create(ctx, cfg.ProjectID, attrs)
}

func (a StorageBucketAPI) Update(
	ctx context.Context, name string, versioning bool, lifecycle storage.Lifecycle,
) error {
	_, err := a.Client.Bucket(name).Update(ctx, storage.BucketAttrsToUpdate{
		VersioningEnabled: &versioning,
		Lifecycle:         &storage.Lifecycle{Rules: lifecycle.Rules},
	})
	return err
}

// NewStorageClientFrom builds a storage client from an explicit credential
// file ("" = application default credentials).
func NewStorageClientFrom(credFile string) IOE.IOEither[error, *storage.Client] {
	return IOE.TryCatchError(func() (*storage.Client, error) {
		if credFile == "" {
			return storage.NewClient(context.Background())
		}
		return storage.NewClient(
			context.Background(),
			option.WithAuthCredentialsFile(option.ServiceAccount, credFile),
		)
	})
}

// ---- gcloud lifecycle-file parsing (recipe parity) ----

// gcloudLifecycle mirrors gcloud's --lifecycle-file YAML shape.
type gcloudLifecycle struct {
	Rule []gcloudRule `yaml:"rule"`
}

type gcloudRule struct {
	Action struct {
		Type string `yaml:"type"`
	} `yaml:"action"`
	Condition struct {
		Age                 int      `yaml:"age"`
		MatchesStorageClass []string `yaml:"matchesStorageClass"`
	} `yaml:"condition"`
}

// ParseGcloudLifecycle converts gcloud's YAML lifecycle format into a
// storage.Lifecycle (the only lifecycle file format the recipes use).
func ParseGcloudLifecycle(r io.Reader) (storage.Lifecycle, error) {
	var raw gcloudLifecycle
	if err := yaml.NewDecoder(r).Decode(&raw); err != nil {
		return storage.Lifecycle{}, fmt.Errorf("parse lifecycle file: %w", err)
	}
	rules := make([]storage.LifecycleRule, 0, len(raw.Rule))
	for _, rule := range raw.Rule {
		rules = append(rules, storage.LifecycleRule{
			Action: storage.LifecycleAction{Type: rule.Action.Type},
			Condition: storage.LifecycleCondition{
				AgeInDays:             int64(rule.Condition.Age),
				MatchesStorageClasses: rule.Condition.MatchesStorageClass,
			},
		})
	}
	return storage.Lifecycle{Rules: rules}, nil
}
