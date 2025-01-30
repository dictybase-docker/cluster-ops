package main

import (
	"github.com/dictybase-docker/cluster-ops/internal/backend"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(backend.Run)
}
