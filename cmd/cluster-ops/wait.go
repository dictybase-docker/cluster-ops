package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/gcp"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	"github.com/urfave/cli/v2"
)

// exit codes for wait conditions: 0 met, 1 timeout, 2 terminal, 3 API error.
const (
	waitExitTimeout  = 1
	waitExitTerminal = 2
	waitExitAPI      = 3
)

// exitForWait maps a waitfor.Error to a process exit code.
func exitForWait(err error) int {
	if we, ok := errors.AsType[*waitfor.Error](err); ok {
		switch we.Kind {
		case waitfor.KindTerminal:
			return waitExitTerminal
		case waitfor.KindAPI:
			return waitExitAPI
		default:
			return waitExitTimeout
		}
	}
	return waitExitTimeout
}

func waitTimeoutFlag() cli.Flag {
	return &cli.Float64Flag{
		Name:  "timeout",
		Value: 900,
		Usage: "Wait budget in seconds",
	}
}

func waitIntervalFlag() cli.Flag {
	return &cli.Float64Flag{
		Name:  "interval",
		Value: 5,
		Usage: "Seconds between polls",
	}
}

// waitConfig builds the WaitConfig from flags with a stderr progress logger.
func waitConfig(c *cli.Context) waitfor.WaitConfig {
	timeout := c.Float64("timeout")
	interval := c.Float64("interval")
	if timeout <= 0 {
		timeout = 900
	}
	if interval <= 0 {
		interval = 5
	}
	return waitfor.WaitConfig{
		Timeout:  time.Duration(timeout * float64(time.Second)),
		Interval: time.Duration(interval * float64(time.Second)),
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
	}
}

// ioErr evaluates an IOEither into a plain error.
func ioErr[A any](res IOE.IOEither[error, A]) error {
	return E.Fold(F.Identity[error], func(A) error { return nil })(res())
}

// waitRunner runs a condition with prebuilt clients, context, and config.
type waitRunner func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error

// waitAction is the shared action wrapper: build clients, run the condition,
// map the failure kind to the exit code.
func waitAction(build func(c *cli.Context) waitRunner) cli.ActionFunc {
	return func(c *cli.Context) error {
		return F.Pipe1(
			newClientsIOE(c)(),
			E.Fold(
				func(err error) error { return cli.Exit(fmt.Sprintf("API error: %v", err), waitExitAPI) },
				func(clients kube.Clients) error {
					ctx, stop := signalContext()
					defer stop()
					if err := build(c)(ctx, clients, waitConfig(c)); err != nil {
						return cli.Exit(err.Error(), exitForWait(err))
					}
					return nil
				},
			),
		)
	}
}

const (
	usageNamespace    = "Namespace"
	flagWaitNamespace = "namespace"
	flagSelector      = "selector"
)

func waitNamespaceFlag() cli.Flag {
	return &cli.StringFlag{
		Name:     flagWaitNamespace,
		Aliases:  []string{"n"},
		Required: true,
		Usage:    usageNamespace,
	}
}

//nolint:funlen // eight subcommands in one table; splitting hurts review
func waitCommand() *cli.Command {
	return &cli.Command{
		Name:  "wait",
		Usage: "Wait for a Kubernetes condition; exit 0 met, 1 timeout, 2 terminal failure, 3 API error",
		Subcommands: []*cli.Command{
			{
				Name:  "pods-ready",
				Usage: "Wait until N pods matching a selector are Running with every container ready",
				Flags: []cli.Flag{
					waitNamespaceFlag(),
					&cli.StringFlag{Name: flagSelector, Aliases: []string{"l"}, Required: true, Usage: "Label selector"},
					&cli.IntFlag{Name: "count", Aliases: []string{"c"}, Value: 1, Usage: "Ready pods to wait for"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					cond := waitfor.PodReadyCondition{
						Namespace: c.String(flagWaitNamespace),
						Selector:  c.String("selector"),
						Count:     c.Int("count"),
					}
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.PodsReady(ctx, clients, cfg, cond))
					}
				}),
			},
			{
				Name:  "job-complete",
				Usage: "Wait until a Job succeeds (seen-then-deleted counts as done; failed Jobs tail logs and exit 2)",
				Flags: []cli.Flag{
					waitNamespaceFlag(),
					&cli.StringFlag{Name: "job", Aliases: []string{"j"}, Required: true, Usage: "Job name"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					cond := waitfor.JobCondition{Namespace: c.String(flagWaitNamespace), Name: c.String("job")}
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.JobComplete(ctx, clients, cfg, cond))
					}
				}),
			},
			{
				Name:  "resources-gone",
				Usage: "Wait until no resources of kind remain (missing namespace or API type counts as gone)",
				Flags: []cli.Flag{
					waitNamespaceFlag(),
					&cli.StringFlag{Name: "kind", Required: true, Usage: "Resource kind (pods, svc, arangodeployment, …)"},
					&cli.StringFlag{Name: flagSelector, Aliases: []string{"l"}, Usage: "Label selector"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					ns, kind, selector := c.String(flagWaitNamespace), c.String("kind"), c.String("selector")
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.ResourcesGone(ctx, clients, cfg, ns, kind, selector))
					}
				}),
			},
			{
				Name:  "deployment-rollout",
				Usage: "Wait until a Deployment's observedGeneration is current and all replicas are available",
				Flags: []cli.Flag{
					waitNamespaceFlag(),
					&cli.StringFlag{Name: nameFlag, Required: true, Usage: "Deployment name"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					cond := waitfor.DeploymentRolloutCondition{Namespace: c.String(flagWaitNamespace), Name: c.String("name")}
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.DeploymentRollout(ctx, clients, cfg, cond))
					}
				}),
			},
			{
				Name:  "custom-resource",
				Usage: "Wait until a custom resource's status field reaches a value",
				Flags: []cli.Flag{
					waitNamespaceFlag(),
					&cli.StringFlag{Name: nameFlag, Required: true, Usage: "Resource name"},
					&cli.StringFlag{Name: "group", Required: true, Usage: "API group (e.g. database.arangodb.com)"},
					&cli.StringFlag{Name: "version", Required: true, Usage: "API version (e.g. v1)"},
					&cli.StringFlag{Name: "kind", Required: true, Usage: "Kind (e.g. ArangoDeployment)"},
					&cli.StringFlag{Name: "status-path", Required: true, Usage: "Dot path under .status (e.g. state)"},
					&cli.StringFlag{Name: "expected-value", Required: true, Usage: "Success value (e.g. Running)"},
					&cli.StringSliceFlag{Name: "failed-value", Usage: "Same-path value that fails fast (repeatable)"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					cond := waitfor.CustomResourceCondition{
						Namespace:     c.String(flagWaitNamespace),
						Name:          c.String("name"),
						Group:         c.String("group"),
						Version:       c.String("version"),
						Kind:          c.String("kind"),
						StatusPath:    c.String("status-path"),
						ExpectedValue: c.String("expected-value"),
						FailedValues:  c.StringSlice("failed-value"),
					}
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.CustomResourceStatus(ctx, clients, cfg, cond))
					}
				}),
			},
			{
				Name:  "crd-established",
				Usage: "Wait until a CRD reports Established=True",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "crd", Required: true, Usage: "CRD name (e.g. arangodeployments.database.arangodb.com)"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					crdName := c.String("crd")
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.CRDEstablished(ctx, clients.APIExtensions, cfg, crdName))
					}
				}),
			},
			{
				Name:  "storage-classes",
				Usage: "Wait until named StorageClasses exist with the expected provisioner",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "classes", Required: true, Usage: "Comma-separated StorageClass names"},
					&cli.StringFlag{Name: "provisioner", Required: true, Usage: "Expected provisioner"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					cond := waitfor.StorageClassCondition{
						Classes:     strings.Split(c.String("classes"), ","),
						Provisioner: c.String("provisioner"),
					}
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.StorageClassesReady(ctx, clients, cfg, cond))
					}
				}),
			},
			{
				Name:  "machine-image",
				Usage: "Wait until a GCP machine image reports READY (FAILED is terminal)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "project", Usage: "GCP project (defaults to ADC metadata)"},
					&cli.StringFlag{Name: nameFlag, Required: true, Usage: "Machine image name"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: func(c *cli.Context) error {
					project, name := c.String("project"), c.String("name")
					ctx, stop := signalContext()
					defer stop()
					if err := gcp.WaitMachineImage(ctx, project, name, waitConfig(c)); err != nil {
						return cli.Exit(err.Error(), exitForWait(err))
					}
					return nil
				},
			},
			{
				Name:  "nodes-ready",
				Usage: "Wait until N nodes matching a selector report Ready=True",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: flagSelector, Aliases: []string{"l"}, Required: true, Usage: "Node label selector"},
					&cli.IntFlag{Name: "count", Aliases: []string{"c"}, Value: 1, Usage: "Ready nodes to wait for"},
					waitTimeoutFlag(), waitIntervalFlag(),
				},
				Action: waitAction(func(c *cli.Context) waitRunner {
					cond := waitfor.NodeCondition{Selector: c.String("selector"), Count: c.Int("count")}
					return func(ctx context.Context, clients kube.Clients, cfg waitfor.WaitConfig) error {
						return ioErr(waitfor.NodesReady(ctx, clients, cfg, cond))
					}
				}),
			},
		},
	}
}
