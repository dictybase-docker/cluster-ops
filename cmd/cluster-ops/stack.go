package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

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
						&cli.StringFlag{Name: "value", Usage: "Config value to store; read from stdin when omitted"},
						&cli.BoolFlag{Name: "secret"},
					),
				}},
			},
			{
				Name:   "preview",
				Usage:  "Preview with structured diff and the protected-resource gate",
				Action: stackPreviewAction,
				Flags: append(stackFlags(),
					&cli.BoolFlag{Name: allowProtectedFlag}),
			},
			{
				Name:   "up",
				Usage:  "Update with secret-safe event logging",
				Action: stackUpAction,
				Flags: append(stackFlags(),
					&cli.BoolFlag{Name: dryRunFlag},
					&cli.BoolFlag{Name: allowProtectedFlag}),
			},
			{
				Name:   destroyCmd,
				Usage:  "Destroy the stack resources",
				Action: stackDestroyAction,
				Flags: append(stackFlags(),
					&cli.BoolFlag{Name: dryRunFlag}),
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
	return pulumiauto.DeployMode{
		DryRun: dryRun, AllowProtected: allowProtected,
		ProtectDeployIn: splitCSV(os.Getenv("PROTECT_DEPLOY_NS")),
	}
}

// targetEnv is the ambient PULUMI_* environment (the fallback bindings for
// unregistered local/folder stacks).
type targetEnv struct {
	Folder, Stack        string
	Backend, Secrets     string
	CredFile, Passphrase string
}

// newOpener is the production opener seam (tests swap it).
var newOpener = pulumiauto.AutoOpener

// pulumiCredential is the Pulumi identity: the deployer key from
// PULUMI_GCP_CREDENTIALS. The ambient GOOGLE_APPLICATION_CREDENTIALS is the
// kops-creator key in a cluster shell and must never be used here.
func pulumiCredential() string {
	return os.Getenv("PULUMI_GCP_CREDENTIALS")
}

// targetFrom builds the deploy target from the registry entry, falling back
// to the environment ONLY on a typed registry miss. The Pulumi credential is
// PULUMI_GCP_CREDENTIALS (the deployer key) — never the ambient
// GOOGLE_APPLICATION_CREDENTIALS, which cluster-env sets to the kops key.
func targetFrom(cltx *cli.Context) (pulumiauto.DeployTarget, error) {
	return targetFromEnv(context.Background(),
		registry.LoadByStack(cltx.String("registry-dir"), cltx.String(stackFlag)),
		targetEnv{
			Folder:     cltx.String(folderFlag),
			Stack:      cltx.String(stackFlag),
			Backend:    os.Getenv("PULUMI_BACKEND_URL"),
			Secrets:    os.Getenv("PULUMI_SECRET_PROVIDER"),
			CredFile:   pulumiCredential(),
			Passphrase: os.Getenv("PULUMI_CONFIG_PASSPHRASE"),
		})
}

// targetFromEnv resolves the bindings: registry entry wins; a typed miss may
// fall back to the environment but only with complete bindings; any other
// registry error fails closed.
func targetFromEnv(
	_ context.Context, entry E.Either[error, registry.Entry], env targetEnv,
) (pulumiauto.DeployTarget, error) {
	t := pulumiauto.DeployTarget{
		Folder:     env.Folder,
		Stack:      env.Stack,
		CredFile:   env.CredFile,
		Passphrase: env.Passphrase,
	}
	resolved, err := E.UnwrapError(entry)
	switch {
	case err == nil:
		t.Backend = resolved.PulumiState
		t.Secrets = resolved.KMSSecretsProvider
	case errors.Is(err, registry.ErrEntryNotFound):
		t.Backend, t.Secrets = env.Backend, env.Secrets
	default:
		return pulumiauto.DeployTarget{}, err
	}
	if t.Backend == "" || t.Secrets == "" {
		return pulumiauto.DeployTarget{}, fmt.Errorf(
			"stack %s: no registry entry and PULUMI_BACKEND_URL/PULUMI_SECRET_PROVIDER are not both set",
			env.Stack)
	}
	return t, nil
}

// openTarget opens (or creates) the stack behind the CLI target.
func openTarget(cltx *cli.Context, create bool) (pulumiauto.StackAPI, pulumiauto.DeployTarget, error) {
	t, err := targetFrom(cltx)
	if err != nil {
		return nil, t, err
	}
	s, err := pulumiauto.OpenStack(context.Background(), newOpener(), t, create)
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
	rawKey := cltx.String("key")
	key := pulumiauto.NormalizeConfigKey(rawKey)
	value := cltx.String("value")
	if !cltx.IsSet("value") {
		stdin := readSecretFromStdin(os.Stdin)
		secret, err := E.UnwrapError(stdin())
		if err != nil {
			return err
		}
		value = secret
	}
	setConfig := pulumiauto.SetConfig(context.Background(), s, key, value, cltx.Bool("secret"))
	if _, err := E.UnwrapError(setConfig()); err != nil {
		return err
	}
	fmt.Printf("set %s\n", key)
	return nil
}

// readSecretFromStdin captures the secret piped in by the set-secret recipe's
// hidden prompt, keeping it out of the process arguments.
func readSecretFromStdin(r io.Reader) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		data, err := io.ReadAll(r)
		if err != nil {
			return "", fmt.Errorf("read secret from stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\n\r"), nil
	})
}

func stackPreviewAction(cltx *cli.Context) error {
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	mode := deployModeFrom(cltx.Bool(dryRunFlag), cltx.Bool(allowProtectedFlag))
	gate, err := E.UnwrapError(pulumiauto.PreviewGate(context.Background(), s, pulumiauto.PolicyFrom(mode))())
	if err != nil {
		return err
	}
	pulumiauto.CleanupPlan(gate.PlanPath)
	printSummary(s.Name(), gate.Summary)
	for _, b := range gate.Blocked {
		fmt.Fprintf(os.Stderr, "BLOCKED %s %s (%s)\n", b.URN, b.Type, b.Op)
	}
	if len(gate.Blocked) > 0 && !cltx.Bool(allowProtectedFlag) {
		return cli.Exit(
			"preview blocked by protected changes — re-run with --allow-protected to accept", 1)
	}
	return nil
}

func stackUpAction(cltx *cli.Context) error {
	mode := deployModeFrom(cltx.Bool(dryRunFlag), cltx.Bool(allowProtectedFlag))
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	secrets, err := E.UnwrapError(pulumiauto.SecretValues(context.Background(), s)())
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	run := upRunner(context.Background(), s, mode.UpOptions(logger, secrets))
	outcome, err := E.UnwrapError(pulumiauto.RunIfAllowed(context.Background(), s, mode, run)())
	if err != nil {
		return err
	}
	printSummary(s.Name(), outcome.Summary)
	return nil
}

func stackDestroyAction(cltx *cli.Context) error {
	mode := deployModeFrom(cltx.Bool(dryRunFlag), false)
	s, _, err := openTarget(cltx, false)
	if err != nil {
		return err
	}
	if cltx.String("confirm") != confirmYes {
		return fmt.Errorf("%s: confirmation required — re-run with --confirm yes", s.Name())
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

// upRunner is the Update step the gate runs with its saved plan.
func upRunner(
	ctx context.Context, s pulumiauto.StackAPI, opts pulumiauto.UpOptions,
) func(string) IOE.IOEither[error, auto.UpResult] {
	return func(planPath string) IOE.IOEither[error, auto.UpResult] {
		opts.PlanPath = planPath
		return pulumiauto.Up(ctx, s, opts)
	}
}

// printSummary renders the one-line change summary.
func printSummary(name string, sum pulumiauto.PreviewSummary) {
	fmt.Printf("stack %s: %d/%d/%d/%d\n", name, sum.Creates, sum.Updates, sum.Deletes, sum.Replaces)
}
