package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/urfave/cli/v2"
)

func main() {
	// Initialize slog
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	app := &cli.App{
		Name:   "kops-cluster-creator",
		Usage:  "Create a Kubernetes cluster using kops",
		Flags:  []cli.Flag{},
		Action: createCluster,
	}

	// Combine all flag groups
	flagGroups := []func() []cli.Flag{
		defineClusterFlags,
		defineCredentialsFlags,
		defineKubernetesFlags,
		defineMasterFlags,
		defineNodeFlags,
		defineOtherFlags,
	}

	// Add flags from each group to the app
	for _, flagGroup := range flagGroups {
		app.Flags = append(app.Flags, flagGroup()...)
	}

	err := app.Run(os.Args)
	if err != nil {
		slog.Error("Error running application", "error", err)
		os.Exit(1)
	}
}

func createCluster(cltx *cli.Context) error {
	slog.Info("Creating Kubernetes cluster...")
	args := []string{
		"create", "cluster",
		"--name", cltx.String("cluster-name"),
		"--state", cltx.String("state"),
		"--project", cltx.String("project-id"),
		"--zones", cltx.String("zone"),
		"--node-count", fmt.Sprintf("%d", cltx.Int("node-count")),
		"--node-size", cltx.String("node-size"),
		"--node-volume-size", fmt.Sprintf("%d", cltx.Int("node-volume-size")),
		"--control-plane-size", cltx.String("master-size"),
		"--control-plane-volume-size", fmt.Sprintf("%d", cltx.Int("master-volume-size")),
		"--control-plane-count", cltx.String("master-count"),
		"--kubernetes-version", cltx.String("kubernetes-version"),
		"--ssh-public-key", cltx.String("ssh-key"),
		"--cloud", cltx.String("provider"),
		"--networking", "cilium-etcd",
	}
	cmd := exec.Command("kops", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error(
			"Error creating cluster",
			"error",
			err,
			"output",
			string(output),
		)
		return cli.Exit("Failed to create cluster: "+err.Error(), 1)
	}

	slog.Info("Cluster creation initiated.")
	slog.Info("Command output", "output", string(output))
	slog.Info("Please wait for the cluster to be fully provisioned.")
	slog.Info("You can check the status using: kops validate cluster")

	return nil
}
