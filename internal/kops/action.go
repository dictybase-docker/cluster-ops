package kops

import (
	"fmt"

	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"

	"github.com/urfave/cli/v2"
)

// buildKopsArgs constructs the kops create cluster argument list from CLI context.
// Pure function — no side effects.
func buildKopsArgs(cltx *cli.Context) []string {
	args := []string{
		"create", "cluster",
		"--name", cltx.String("cluster-name"),
		"--state", cltx.String("state"),
		"--project", cltx.String("project-id"),
		"--zones", cltx.String("zones"),
		"--node-count", fmt.Sprintf("%d", cltx.Int("node-count")),
		"--node-size", cltx.String("node-size"),
		"--node-volume-size", fmt.Sprintf("%d", cltx.Int("node-volume-size")),
		"--control-plane-size", cltx.String("master-size"),
		"--control-plane-volume-size", fmt.Sprintf("%d", cltx.Int("master-volume-size")),
		"--control-plane-count", cltx.String("master-count"),
		"--kubernetes-version", cltx.String("kubernetes-version"),
		"--ssh-public-key", cltx.String("ssh-key"),
		"--cloud", cltx.String("provider"),
		"--networking", cltx.String("networking"),
		"--topology", cltx.String("topology"),
	}

	if cpZones := cltx.String("control-plane-zones"); cpZones != "" {
		args = append(args, "--control-plane-zones", cpZones)
	}
	if adminAccess := cltx.String("admin-access"); adminAccess != "" {
		args = append(args, "--admin-access", adminAccess)
	}

	return args
}

// runCreateClusterIOE executes the full kops create cluster pipeline.
// Uses runKopsIOE from exec.go for the underlying command execution.
func runCreateClusterIOE(args []string) IOE.IOEither[error, string] {
	return F.Pipe1(
		runKopsIOE(args...),
		IOE.MapLeft[string](func(err error) error {
			return fmt.Errorf("kops create cluster failed: %w", err)
		}),
	)
}

// CreateCluster creates a Kubernetes cluster using kops.
func CreateCluster(cltx *cli.Context) error {
	args := buildKopsArgs(cltx)

	return F.Pipe2(
		runCreateClusterIOE(args),
		IOE.ChainFirstIOK[error](IO.Logf[string]("Kops cluster config written. Output: %s")),
		IOE.Fold(func(err error) IO.IO[error] {
			return IO.Of[error](cli.Exit("Failed to create kops cluster: "+err.Error(), 1))
		}, func(_ string) IO.IO[error] { return IO.Of[error](nil) }),
	)()
}
