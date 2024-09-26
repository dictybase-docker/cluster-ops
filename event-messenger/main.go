package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type EventMessengerEmail struct {
	Config *EventMessengerEmailConfig
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		return nil
	})
}
