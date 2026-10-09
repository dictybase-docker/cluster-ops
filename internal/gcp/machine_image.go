package gcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// WaitMachineImage polls a Compute Engine machine image until it reports
// READY; FAILED is terminal, a missing image keeps waiting (creation is
// async). Uses application default credentials (gcloud auth in recipes).
func WaitMachineImage(ctx context.Context, project, name string, cfg waitfor.WaitConfig) error {
	svc, err := compute.NewService(ctx, option.WithScopes(compute.ComputeScope))
	if err != nil {
		return &waitfor.Error{Kind: waitfor.KindAPI, Msg: fmt.Sprintf("compute client: %v", err)}
	}
	project, err = resolveProject(ctx, project)
	if err != nil {
		return err
	}
	start := time.Now()
	for {
		status, err := describeMachineImage(ctx, svc, project, name)
		switch {
		case err != nil:
			return err
		case status == "READY":
			return nil
		case status == "FAILED":
			return &waitfor.Error{Kind: waitfor.KindTerminal,
				Msg: fmt.Sprintf("machine image %s creation FAILED", name)}
		default:
			logWait(cfg, "Machine image %s: %s (%s elapsed)...", name, status, time.Since(start))
		}
		if err := waitTick(ctx, cfg, start, name); err != nil {
			return err
		}
	}
}

//nolint:cyclop // linear status classification
func describeMachineImage(
	ctx context.Context, svc *compute.Service, project, name string,
) (string, error) {
	img, err := svc.MachineImages.Get(project, name).Context(ctx).Do()
	if err != nil {
		// A 404 before the image registers is normal right after --async.
		if isNotFound(err) {
			return "CREATING", nil
		}
		return "", &waitfor.Error{Kind: waitfor.KindAPI,
			Msg: fmt.Sprintf("describe machine image %s: %v", name, err)}
	}
	return img.Status, nil
}

func resolveProject(ctx context.Context, project string) (string, error) {
	if project != "" {
		return project, nil
	}
	p, err := metadata.ProjectIDWithContext(ctx)
	if err != nil {
		return "", &waitfor.Error{Kind: waitfor.KindAPI, Msg: fmt.Sprintf("resolve project: %v", err)}
	}
	return p, nil
}

// waitTick sleeps one interval, capped at the time left in cfg.Timeout, and
// reports a timeout once the budget is spent. The budget comes from cfg, not
// ctx: the CLI's context only carries signals.
func waitTick(ctx context.Context, cfg waitfor.WaitConfig, start time.Time, name string) error {
	timedOut := &waitfor.Error{
		Kind: waitfor.KindTimeout,
		Msg:  fmt.Sprintf("timed out waiting for machine image %s after %s", name, time.Since(start).Round(time.Second)),
	}
	sleep := intervalOr(cfg)
	if cfg.Timeout > 0 {
		remaining := cfg.Timeout - time.Since(start)
		if remaining <= 0 {
			return timedOut
		}
		sleep = min(sleep, remaining)
	}
	if ctx.Err() != nil {
		return timedOut
	}
	select {
	case <-time.After(sleep):
		return nil
	case <-ctx.Done():
		return timedOut
	}
}

func intervalOr(cfg waitfor.WaitConfig) time.Duration {
	if cfg.Interval > 0 {
		return cfg.Interval
	}
	return 10 * time.Second
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(msg, "notFound") ||
		strings.Contains(msg, "was not found")
}

func logWait(cfg waitfor.WaitConfig, format string, args ...any) {
	if cfg.Logger != nil {
		cfg.Logger.Info(fmt.Sprintf(format, args...))
	}
}
