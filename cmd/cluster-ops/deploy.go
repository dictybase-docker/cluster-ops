package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/dictybase-docker/cluster-ops/internal/pulumiauto"
	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

// prereqsRunnerFn and rolloutWaiterFn are the runner seams (tests swap them).
var (
	prereqsRunnerFn = prereqsRunner
	rolloutWaiterFn = rolloutWaiter
)

// bindingFlags are the cluster bindings WITHOUT --folder: each service
// carries its own folder (the batch deploy never passes one).
func bindingFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: stackFlag, Aliases: []string{"s"}, Required: true, EnvVars: []string{envPulumiStack}},
		registryDirFlag(),
	}
}

// deployCommand deploys services through the Pulumi Automation API.
func deployCommand() *cli.Command {
	return &cli.Command{
		Name:  "deploy",
		Usage: "Composite service deploys (preflight → stack → preview gate → up → rollout)",
		Subcommands: []*cli.Command{{
			Name:   "backend",
			Usage:  "Deploy a set of backend service stacks (serial, stop on first failure)",
			Action: deployBackendAction,
			Flags: append(bindingFlags(),
				&cli.StringFlag{Name: "services-file", Value: "config/services.yaml"},
				&cli.StringFlag{Name: "services", Usage: "Comma-separated app names (empty = all)"},
				&cli.StringFlag{Name: "image-tag", Aliases: []string{"t"}, Required: true},
				&cli.StringFlag{Name: arangoServiceFlag, Aliases: []string{"a"}, Value: defArangoService},
				&cli.BoolFlag{Name: dryRunFlag},
				&cli.BoolFlag{Name: allowProtectedFlag},
			),
		}, {
			Name:   "service",
			Usage:  "Deploy one backend service stack (bootstrap-service)",
			Action: deployServiceAction,
			Flags: append(stackFlags(),
				&cli.StringFlag{Name: "image-tag", Aliases: []string{"t"}, Required: true},
				&cli.StringFlag{Name: arangoServiceFlag, Aliases: []string{"a"}, Value: defArangoService},
				&cli.BoolFlag{Name: dryRunFlag},
				&cli.BoolFlag{Name: allowProtectedFlag},
			),
		}},
	}
}

// deployServiceAction runs the BootstrapService composite.
func deployServiceAction(cltx *cli.Context) error {
	target, err := targetFrom(cltx)
	if err != nil {
		return err
	}
	mode := deployModeFrom(cltx.Bool(dryRunFlag), cltx.Bool(allowProtectedFlag))
	svc := pulumiauto.ServiceSpec{
		Folder:    target.Folder,
		Stack:     target.Stack,
		ImageTag:  cltx.String("image-tag"),
		Preflight: prereqsRunnerFn(cltx, target.Folder, target.Stack),
		Rollout:   rolloutWaiterFn(cltx, target.Folder, target.Stack),
		Logger:    slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	res, err := E.UnwrapError(pulumiauto.BootstrapService(
		context.Background(), newOpener(), target, svc, mode)())
	if err != nil {
		return err
	}
	printSummary(target.Stack, res.Summary)
	fmt.Println("rollout:", res.Rollout)
	return nil
}

// deployBackendAction deploys the selected services through the Go loop.
func deployBackendAction(cltx *cli.Context) error {
	target, err := targetFrom(cltx)
	if err != nil {
		return err
	}
	mode := deployModeFrom(cltx.Bool(dryRunFlag), cltx.Bool(allowProtectedFlag))
	specs, err := pulumiauto.LoadServices(cltx.String("services-file"))
	if err != nil {
		return err
	}
	specs = filterServices(specs, cltx.String("services"))
	if len(specs) == 0 {
		return fmt.Errorf("no services selected by --services %q", cltx.String("services"))
	}
	for i := range specs {
		specs[i].Stack = target.Stack
		specs[i].ImageTag = cltx.String("image-tag")
		specs[i].Preflight = prereqsRunnerFn(cltx, specs[i].Folder, target.Stack)
		specs[i].Rollout = rolloutWaiterFn(cltx, specs[i].Folder, target.Stack)
		specs[i].Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	res, err := E.UnwrapError(pulumiauto.DeployBackendServices(
		context.Background(), newOpener(), target, specs, mode)())
	if err != nil {
		return err
	}
	for i, r := range res {
		printSummary(specs[i].Folder, r.Summary)
	}
	return nil
}

// filterServices keeps only the named apps (csv; empty = all).
func filterServices(specs []pulumiauto.ServiceSpec, csv string) []pulumiauto.ServiceSpec {
	if strings.TrimSpace(csv) == "" {
		return specs
	}
	wanted := map[string]bool{}
	for a := range strings.SplitSeq(csv, ",") {
		if t := strings.TrimSpace(a); t != "" {
			wanted[t] = true
		}
	}
	kept := []pulumiauto.ServiceSpec{}
	for _, s := range specs {
		if wanted[s.App] {
			kept = append(kept, s)
		}
	}
	return kept
}

// prereqsRunner adapts the backend prereq checks to the composite seam; each
// service's own folder/stack drives its gate.
func prereqsRunner(cltx *cli.Context, folder, stack string) pulumiauto.PreflightRunner {
	return func() []preflight.Result {
		in, err := E.UnwrapError(loadPrereqsInput(cltx, folder, stack))
		if err != nil {
			return []preflight.Result{{
				Name: "backend prereqs", Detail: err.Error(), Status: preflight.StatusFail,
			}}
		}
		return runGate(backend.PrereqsChecks(backend.PrereqsConfig{
			Clients:       in.clients,
			Namespace:     in.entry.Namespace,
			SecretName:    in.props.ArangodbSecret.Name,
			UserKey:       in.props.ArangodbSecret.Userkey,
			PassKey:       in.props.ArangodbSecret.Passkey,
			ArangoService: cltx.String(arangoServiceFlag),
			AppName:       in.props.AppName,
			Port:          in.props.Port,
		}))
	}
}

// rolloutWaiter waits for the service Deployment (the recipe derives
// "<appName>-api-server" in the stack's namespace).
func rolloutWaiter(cltx *cli.Context, folder, stack string) pulumiauto.RolloutWaiter {
	return func(ctx context.Context) (string, error) {
		app, namespace, err := readServiceProps(folder, stack)
		if err != nil {
			return "", err
		}
		clients, err := newClientsValue(cltx)
		if err != nil {
			return "", err
		}
		deploy := app + "-api-server"
		cond := waitfor.DeploymentRolloutCondition{Namespace: namespace, Name: deploy}
		cfg := waitfor.WaitConfig{Timeout: 5 * time.Minute}
		if err := ioErr(waitfor.DeploymentRollout(ctx, clients, cfg, cond)); err != nil {
			return "", err
		}
		return deploy + " ready in " + namespace, nil
	}
}

type serviceProps struct {
	AppName   string `yaml:"appName"`
	Namespace string `yaml:"namespace"`
}

// readServiceProps reads appName/namespace from the stack's committed config
// (the recipe's yq reads on Pulumi.<stack>.yaml).
func readServiceProps(folder, stack string) (app, namespace string, err error) {
	project := filepath.Base(strings.TrimSuffix(folder, "/"))
	data, err := os.ReadFile(filepath.Join(folder, "Pulumi."+stack+".yaml"))
	if err != nil {
		return "", "", fmt.Errorf("read stack config: %w", err)
	}
	var raw struct {
		Config map[string]serviceProps `yaml:"config"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", "", fmt.Errorf("parse stack config: %w", err)
	}
	props, ok := raw.Config[project+":properties"]
	if !ok {
		return "", "", fmt.Errorf("stack config has no %s:properties section", project)
	}
	app, namespace = props.AppName, props.Namespace
	if app == "" || namespace == "" {
		return "", "", fmt.Errorf("stack config %s:properties lacks appName/namespace", project)
	}
	return app, namespace, nil
}
