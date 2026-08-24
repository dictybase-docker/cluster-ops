package kops

import (
	"fmt"
	"os/exec"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"

	"github.com/blang/semver"
	"github.com/urfave/cli/v2"
)

var kopsV131 = semver.MustParse("1.31.0")

const (
	flagYes      = "--yes"
	flagAdmin    = "--admin"
	cmdCluster   = "cluster"
	cmdUpdate    = "update"
	cmdReconcile = "reconcile"
)

// UpdateCluster applies pending cluster changes using the correct
// kOps command for the installed version.
func UpdateCluster(cltx *cli.Context) error {
	return F.Pipe3(
		detectKopsVersionIOE(),
		IOE.ChainEitherK(parseSemver),
		IOE.Chain(selectKopsCommand),
		func(effect IOE.IOEither[error, string]) error {
			return E.Fold(
				F.Identity[error],
				func(string) error { return nil },
			)(effect())
		},
	)
}

// detectKopsVersionIOE returns the kOps version as a lazy IOEither.
func detectKopsVersionIOE() IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		out, err := exec.Command("kops", "version", "--short").Output()
		if err != nil {
			return "", fmt.Errorf("kops version --short: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	})
}

// parseSemver parses a version string into a semver.Version.
// Pure Either — no IO.
func parseSemver(version string) E.Either[error, semver.Version] {
	v, err := semver.Make(version)
	if err != nil {
		return E.Left[semver.Version](
			fmt.Errorf("kops version %q is not valid semver: %w", version, err),
		)
	}
	return E.Right[error](v)
}

// selectKopsCommand dispatches to the correct kops subcommand based on version.
func selectKopsCommand(v semver.Version) IOE.IOEither[error, string] {
	return runKopsIOE(updateCommandArgs(v, true)...)
}

// selectKopsPlanCommand dispatches dry-run (no --yes) kops command based on version.
func selectKopsPlanCommand(v semver.Version) IOE.IOEither[error, string] {
	return runKopsIOE(updateCommandArgs(v, false)...)
}

// updateCommandArgs returns the kops arguments for applying or previewing changes.
func updateCommandArgs(v semver.Version, apply bool) []string {
	if v.GTE(kopsV131) {
		if apply {
			return []string{cmdReconcile, cmdCluster, flagYes}
		}
		return []string{cmdReconcile, cmdCluster}
	}
	if apply {
		return []string{cmdUpdate, cmdCluster, flagYes, flagAdmin}
	}
	return []string{cmdUpdate, cmdCluster, flagAdmin}
}

// PlanCluster previews pending cluster changes using the correct
// dry-run kOps command for the installed version.
func PlanCluster(cltx *cli.Context) error {
	return F.Pipe3(
		detectKopsVersionIOE(),
		IOE.ChainEitherK(parseSemver),
		IOE.Chain(selectKopsPlanCommand),
		func(effect IOE.IOEither[error, string]) error {
			return E.Fold(
				F.Identity[error],
				func(string) error { return nil },
			)(effect())
		},
	)
}
