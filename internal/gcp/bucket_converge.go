package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

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
	// Update applies versioning and, when lifecycle is non-nil, the rules.
	Update(ctx context.Context, name string, versioning bool, lifecycle *storage.Lifecycle) error
}

// lifecycleActionDelete is the shared gcloud/storage lifecycle action type.
const lifecycleActionDelete = "Delete"

// BucketConfig is the desired bucket state.
type BucketConfig struct {
	Name, ProjectID, Location string
	Versioning                bool
	// Lifecycle is nil when --lifecycle-file is omitted: existing bucket
	// rules are then LEFT UNTOUCHED (never cleared).
	Lifecycle *storage.Lifecycle
	CredFile  string // "" = application default credentials
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
	return IOE.TryCatchError(w.createOrReprobe)
}

// createOrReprobe absorbs the probe/create race: a typed AlreadyExists from
// Create means another actor won — re-probe and converge the winner's bucket
// (drift included) instead of failing or stopping at "created".
func (w bucketWork) createOrReprobe() (Result, error) {
	err := w.api.Create(context.Background(), w.cfg)
	switch {
	case err == nil:
		return Result{Name: w.name(), Action: ActionCreated}, nil
	case IsAlreadyExists(err):
		attrs, perr := probeAttrs(w.api, w.cfg.Name)
		if perr != nil {
			return Result{}, perr
		}
		if attrs == nil {
			return Result{}, fmt.Errorf("create bucket %s: %w", w.name(), err)
		}
		return w.recoverRaced(attrs)
	default:
		return Result{}, fmt.Errorf("create bucket %s: %w", w.name(), err)
	}
}

func (w bucketWork) recoverRaced(attrs *storage.BucketAttrs) (Result, error) {
	drift := attrs.VersioningEnabled != w.cfg.Versioning ||
		(w.cfg.Lifecycle != nil && !lifecycleEqual(attrs.Lifecycle, *w.cfg.Lifecycle))
	if !drift {
		return Result{Name: w.name(), Action: ActionUnchanged}, nil
	}
	if w.mode.DryRun {
		return Result{Name: w.name(), Action: ActionDryRun}, nil
	}
	if err := w.api.Update(
		context.Background(), w.cfg.Name, w.cfg.Versioning, w.cfg.Lifecycle,
	); err != nil {
		return Result{}, fmt.Errorf("update bucket %s: %w", w.name(), err)
	}
	return Result{Name: w.name(), Action: ActionUpdated}, nil
}

func (w bucketWork) updateIfDrift(attrs *storage.BucketAttrs) IOE.IOEither[error, Result] {
	drift := attrs.VersioningEnabled != w.cfg.Versioning ||
		(w.cfg.Lifecycle != nil && !lifecycleEqual(attrs.Lifecycle, *w.cfg.Lifecycle))
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
		Lifecycle:         lifecycleOrEmpty(cfg.Lifecycle),
	}
	return a.Client.Bucket(cfg.Name).Create(ctx, cfg.ProjectID, attrs)
}

func (a StorageBucketAPI) Update(
	ctx context.Context, name string, versioning bool, lifecycle *storage.Lifecycle,
) error {
	update := storage.BucketAttrsToUpdate{VersioningEnabled: &versioning}
	if lifecycle != nil {
		update.Lifecycle = &storage.Lifecycle{Rules: lifecycle.Rules}
	}
	_, err := a.Client.Bucket(name).Update(ctx, update)
	return err
}

// lifecycleOrEmpty maps a nil (absent) lifecycle config to empty rules for
// CREATE only — an existing bucket's rules are never cleared this way.
func lifecycleOrEmpty(l *storage.Lifecycle) storage.Lifecycle {
	if l == nil {
		return storage.Lifecycle{}
	}
	return *l
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

// gcloudLifecycle mirrors gcloud's --lifecycle-file YAML shape. The decoder
// is STRICT (KnownFields): anything unmappable fails closed rather than
// silently broadening a Delete rule onto live objects.
type gcloudLifecycle struct {
	Rule []gcloudRule `yaml:"rule"`
}

type gcloudRule struct {
	Action struct {
		Type         string `yaml:"type"`
		StorageClass string `yaml:"storageClass"`
	} `yaml:"action"`
	Condition gcloudCondition `yaml:"condition"`
}

type gcloudCondition struct {
	Age                     *int64   `yaml:"age"`
	IsLive                  *bool    `yaml:"isLive"`
	NumNewerVersions        *int     `yaml:"numNewerVersions"`
	MatchesStorageClass     []string `yaml:"matchesStorageClass"`
	MatchesPrefix           []string `yaml:"matchesPrefix"`
	MatchesSuffix           []string `yaml:"matchesSuffix"`
	Date                    *string  `yaml:"date"`
	DaysSinceCustomTime     *int64   `yaml:"daysSinceCustomTime"`
	CustomTimeBefore        *string  `yaml:"customTimeBefore"`
	DaysSinceNoncurrentTime *int64   `yaml:"daysSinceNoncurrentTime"`
	NoncurrentTimeBefore    *string  `yaml:"noncurrentTimeBefore"`
}

// ParseGcloudLifecycle converts gcloud's YAML lifecycle format into a
// storage.Lifecycle. Unknown fields are REJECTED (fail closed).
func ParseGcloudLifecycle(r io.Reader) (storage.Lifecycle, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var raw gcloudLifecycle
	if err := dec.Decode(&raw); err != nil {
		return storage.Lifecycle{}, fmt.Errorf("parse lifecycle file: %w", err)
	}
	rules := make([]storage.LifecycleRule, 0, len(raw.Rule))
	for _, rule := range raw.Rule {
		cond, err := rule.Condition.toStorage()
		if err != nil {
			return storage.Lifecycle{}, err
		}
		rules = append(rules, storage.LifecycleRule{
			Action: storage.LifecycleAction{
				Type:         rule.Action.Type,
				StorageClass: rule.Action.StorageClass,
			},
			Condition: cond,
		})
	}
	return storage.Lifecycle{Rules: rules}, nil
}

func (c gcloudCondition) toStorage() (storage.LifecycleCondition, error) {
	out := storage.LifecycleCondition{
		MatchesStorageClasses: c.MatchesStorageClass,
		MatchesPrefix:         c.MatchesPrefix,
		MatchesSuffix:         c.MatchesSuffix,
	}
	if c.IsLive != nil {
		out.Liveness = storage.Archived
		if *c.IsLive {
			out.Liveness = storage.Live
		}
	}
	if c.NumNewerVersions != nil {
		out.NumNewerVersions = int64(*c.NumNewerVersions)
	}
	if c.Age != nil {
		out.AgeInDays = *c.Age
	}
	if c.DaysSinceCustomTime != nil {
		out.DaysSinceCustomTime = *c.DaysSinceCustomTime
	}
	if c.DaysSinceNoncurrentTime != nil {
		out.DaysSinceNoncurrentTime = *c.DaysSinceNoncurrentTime
	}
	var err error
	if out.CreatedBefore, err = parseLifecycleDate(c.Date); err != nil {
		return storage.LifecycleCondition{}, err
	}
	if out.CustomTimeBefore, err = parseLifecycleDate(c.CustomTimeBefore); err != nil {
		return storage.LifecycleCondition{}, err
	}
	if out.NoncurrentTimeBefore, err = parseLifecycleDate(c.NoncurrentTimeBefore); err != nil {
		return storage.LifecycleCondition{}, err
	}
	return out, nil
}

// parseLifecycleDate maps a gcloud date ("2006-01-02") to midnight UTC.
func parseLifecycleDate(s *string) (time.Time, error) {
	if s == nil {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse lifecycle date %q: %w", *s, err)
	}
	return t, nil
}
