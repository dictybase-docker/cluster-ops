package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	emeConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}
	eventMessenger := NewEventMessenger(emeConfig)

	// Create the email secret
	emailSecret, err := eventMessenger.CreateEmailSecret(ctx)
	if err != nil {
		return err
	}
	// Pass the email secret to CreateEmailDeployment
	if _, err := eventMessenger.CreateEmailDeployment(ctx, emailSecret); err != nil {
		return err
	}

	if _, err := eventMessenger.CreateIssueDeployment(ctx); err != nil {
		return err
	}

	return nil
}

func ReadConfig(
	ctx *pulumi.Context,
) (*EventMessengerConfig, error) {
	conf := config.New(ctx, "")
	eventMessengerConfig := &EventMessengerConfig{}
	if err := conf.TryObject("properties", eventMessengerConfig); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger config: %w", err)
	}
	return eventMessengerConfig, nil
}

func NewEventMessenger(
	config *EventMessengerConfig,
) *EventMessenger {
	return &EventMessenger{
		Config: config,
	}
}
