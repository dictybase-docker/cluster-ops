package main

import (
	"fmt"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/recipeslint"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:      "lint-recipes",
		Usage:     "Mechanical checks for just recipes (bug-prevention gates)",
		ArgsUsage: "[target-dir]",
		Action: func(cltx *cli.Context) error {
			root := cltx.Args().Get(0)
			if root == "" {
				root = "."
			}
			return recipeslint.Run(recipeslint.Config{Root: root})
		},
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
