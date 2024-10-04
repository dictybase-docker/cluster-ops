package main

import (
	"fmt"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/kops"
	"github.com/urfave/cli/v2"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	app := &cli.App{
		Name:   "kops-cluster-creator",
		Usage:  "Create a Kubernetes cluster using kops",
		Flags:  []cli.Flag{},
		Action: func(c *cli.Context) error {
			return kops.CreateCluster(c, logger)
		},
	}

	// Combine all flag groups
	flagGroups := []func() []cli.Flag{
		kops.DefineClusterFlags,
		kops.DefineCredentialsFlags,
		kops.DefineKubernetesFlags,
		kops.DefineMasterFlags,
		kops.DefineNodeFlags,
		kops.DefineOtherFlags,
	}

	// Add flags from each group to the app
	for _, flagGroup := range flagGroups {
		app.Flags = append(app.Flags, flagGroup()...)
	}

	if err := app.Run(os.Args); err != nil {
		logger.Error("Error running application", "error", err)
		os.Exit(1)
	}
}
