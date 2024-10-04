package main

import (
	"fmt"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/kops"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:   "kops-cluster-creator",
		Usage:  "Create a Kubernetes cluster using kops",
		Action: kops.CreateCluster,
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
		fmt.Fprintf(os.Stderr, "Error running application %v", err)
		os.Exit(1)
	}
}
