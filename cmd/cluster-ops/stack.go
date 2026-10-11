package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	E "github.com/IBM/fp-go/v2/either"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/pulumiauto"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/urfave/cli/v2"
)

// stackCommand manages Pulumi stacks through the Automation API.
func stackCommand() *cli.Command {
	return &cli.Command{
		Name:  "stack",
		Usage: "Manage Pulumi stacks (Pulumi Automation API)",
		Subcommands: []*cli.Command{
			{
				Name:   "ensure",
				Usage:  "Select a stack, or create it with --create",
				Action: stackEnsureAction,
				Flags: append(stackFlags(),
					&cli.BoolFlag{Name: "create", Usage: "Create the stack when absent"},
					&cli.StringFlag{Name: "copy-config-from", Usage: "Copy config from this stack after create"},
				),
			},
			{
				Name:  "config",
				Usage: "Manage stack configuration",
				Subcommands: []*cli.Command{{
					Name:   "set",
					Usage:  "Set one config key (--secret marks it for the secrets provider)",
					Action: stackConfigSetAction,
					Flags: append(stackFlags(),
						&cli.StringFlag{Name: "key", Required: true},
						&cli.StringFlag{Name: "value", Required: true},
						&cli.BoolFlag{Name: "secret"},
					),
				}},
			},
			{
				Name:   "preview",
				Usage:  "Preview with structured diff and the protected-resource gate",
				Action: stackPreviewAction,
				Flags: append(stackFlags(),
					&cli.BoolFlag{Name: "allow-protected"}),
			},
			{
				Name:   "up",
				Usage:  "Update with secret-safe event logging",
				Action: stackUpAction,
				Flags: append(stackFlags(),
					&cli.BoolFlag{Name: "dry-run"},
					&cli.BoolFlag{Name: "allow-protected"}),
			},
			{
				Name:   "destroy",
				Usage:  "Destroy the stack resources",
				Action: stackDestroyAction,
				Flags: append(stackFlags(),
					&cli.BoolFlag{Name: "dry-run"}),
			},
			{
				Name:   "outputs",
				Usage:  "Read one stack output",
				Action: stackOutputsAction,
				Flags: append(stackFlags(),
					&cli.StringFlag{Name: "key", Required: true}),
			},
		},
	}
}

// stackFlags are the shared target bindings.
func stackFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: folderFlag, Aliases: []string{"f"}, Required: true},
		&cli.StringFlag{Name: stackFlag, Aliases: []string{"s"}, Required: true, EnvVars: []string{envPulumiStack}},
		registryDirFlag(),
	}
}

// deployModeFrom maps the CLI switches onto the flow mode.
func deployModeFrom(dryRun, allowProtected bool) pulumiauto.DeployMode {
	return pulumiauto.DeployMode{DryRun: dryRun, AllowProtected: allowProtected}
}

// targetFrom builds the deploy target: the registry entry binds the backend
// and secrets provider when the stack is a registered cluster; otherwise the
// PULUMI_* environment binds it (local and folder stacks keep working).
func targetFrom(cltx *cli.Context) pulumiauto.DeployTarget {
	stack := cltx.String(stackFlag)
	t := pulumiauto.DeployTarget{
		Folder:     cltx.String(folderFlag),
		Stack:      stack,
		Backend:    os.Getenv("PULUMI_BACKEND_URL"),
		Secrets:    os.Getenv("PULUMI_SECRET_PROVIDER"),
		CredFile:   os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		Passphrase: os.Getenv("PULUMI_CONFIG_PASSPHRASE"),
	}
	entry, err := E.UnwrapError(registry.LoadByStack(cltx.String("registry-dir"), stack))
	if err == nil {
		t.Backend = entry.PulumiState
		t.Secrets = entry.KMSSecretsProvider
	}
	return t
}

// openTarget opens (or creates) the stack behind the CLI target.
func openTarget(cltx *cli.Context, create bool) (pulumiauto.StackAPI, pulumiauto.DeployTarget, error) {
	t := targetFrom(cltx)
	s, err := pulumiauto.OpenStack(context.Background(), pulumiauto.AutoOpener(), t, create)
	return s, t, err
}

func stackEnsureAction(cltx *cli.Context) error {
	s, t, err := openTarget(cltx, cltx.Bool("create"))
	if err != nil {
		return err
	}
	if from := cltx.String("copy-config-from"); from != "" {
		if err := copyConfig(t, s, from); err != nil {
			return err
		}
	}
	fmt.Printf("stack %s ready in %s\n", s.Name(), t.Folder)
	return nil
}

// copyConfig copies every config value from another stack of the same target
// (the --copy-config-from flow; secret flags are preserved).
func copyConfig(t pulumiauto.DeployTarget, to pulumiauto.StackAPI, from string) error {
	src, err := pulumiauto.OpenStack(context.Background(), pulumiauto.AutoOpener(),
		pulumiauto.DeployTarget{Folder: t.Folder, Stack: from,
			Backend: t.Backend, Secrets: t.Secrets, CredFile: t.CredFile, Passphrase: t.Passphrase}, false)
	if err != nil {
		return err
	}
	_, err = E.UnwrapError(pulumiauto.CopyConfig(context.Background(), src, to)())
	return err
}

func stackConfigSetAction(cltx *cli.Context) error {
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	key, value := cltx.String("key"), cltx.String("value")
	setConfig := pulumiauto.SetConfig(context.Background(), s, key, value, cltx.Bool("secret"))
	if _, err := E.UnwrapError(setConfig()); err != nil {
		return err
	}
	fmt.Printf("set %s\n", key)
	return nil
}

func stackPreviewAction(cltx *cli.Context) error {
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	gate, err := E.UnwrapError(pulumiauto.PreviewGate(context.Background(), s, nil)())
	if err != nil {
		return err
	}
	printSummary(s.Name(), gate.Summary)
	for _, b := range gate.Blocked {
		fmt.Fprintf(os.Stderr, "BLOCKED %s %s (%s)\n", b.URN, b.Type, b.Op)
	}
	if len(gate.Blocked) > 0 && !cltx.Bool("allow-protected") {
		return cli.Exit(
			"preview blocked by protected changes — re-run with --allow-protected to accept", 1)
	}
	return nil
}

func stackUpAction(cltx *cli.Context) error {
	mode := deployModeFrom(cltx.Bool("dry-run"), cltx.Bool("allow-protected"))
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	secrets, err := E.UnwrapError(pulumiauto.SecretValues(context.Background(), s)())
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	run := func() IOE.IOEither[error, auto.UpResult] {
		return pulumiauto.Up(context.Background(), s, mode.UpOptions(logger, secrets))
	}
	res, err := E.UnwrapError(pulumiauto.RunIfAllowed(context.Background(), s, mode, run)())
	if err != nil {
		return err
	}
	printSummary(s.Name(), pulumiauto.SummaryFromResourceChanges(res.Summary.ResourceChanges))
	return nil
}

func stackDestroyAction(cltx *cli.Context) error {
	mode := deployModeFrom(cltx.Bool("dry-run"), false)
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	if mode.DryRun {
		fmt.Printf("DRY RUN %s destroy\n", s.Name())
		return nil
	}
	if err := s.Destroy(context.Background()); err != nil {
		return err
	}
	fmt.Printf("stack %s destroyed\n", s.Name())
	return nil
}

func stackOutputsAction(cltx *cli.Context) error {
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	v, err := E.UnwrapError(pulumiauto.StackOutput(context.Background(), s, cltx.String("key"))())
	if err != nil {
		return err
	}
	fmt.Println(v)
	return nil
}

// printSummary renders the one-line change summary.
func printSummary(name string, sum pulumiauto.PreviewSummary) {
	fmt.Printf("stack %s: %d/%d/%d/%d\n", name, sum.Creates, sum.Updates, sum.Deletes, sum.Replaces)
}
