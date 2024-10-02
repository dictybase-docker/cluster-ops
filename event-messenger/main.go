package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type EventMessengerIssue struct {
	Config *EventMessengerIssueConfig
}

type EventMessengerEmail struct {
	Config *EventMessengerEmailConfig
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	emeConfig, err := ReadEventMessengerEmailConfig(ctx)
	if err != nil {
		return err
	}

	eventMessengerEmail := NewEventMessengerEmail(emeConfig)

	if _, err := eventMessengerEmail.CreateDeployment(ctx); err != nil {
		return err
	}
	emiConfig, err := ReadEventMessengerIssueConfig(ctx)
	if err != nil {
		return err
	}

	eventMessengerIssue := NewEventMessengerIssue(emiConfig)

	if _, err := eventMessengerIssue.CreateDeployment(ctx); err != nil {
		return err
	}

	return nil
}

func ReadEventMessengerIssueConfig(
	ctx *pulumi.Context,
) (*EventMessengerIssueConfig, error) {
	conf := config.New(ctx, "event-messenger")
	eventMessengerIssue := &EventMessengerIssueConfig{}
	if err := conf.TryObject("properties", eventMessengerIssue); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger config: %w", err)
	}
	return eventMessengerIssue, nil
}

func ReadEventMessengerEmailConfig(
	ctx *pulumi.Context,
) (*EventMessengerEmailConfig, error) {
	conf := config.New(ctx, "event-messenger")
	eventMessengerEmail := &EventMessengerEmailConfig{}
	if err := conf.TryObject("properties", eventMessengerEmail); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger config: %w", err)
	}
	return eventMessengerEmail, nil
}

func NewEventMessengerEmail(
	config *EventMessengerEmailConfig,
) *EventMessengerEmail {
	return &EventMessengerEmail{
		Config: config,
	}
}

func NewEventMessengerIssue(
	config *EventMessengerIssueConfig,
) *EventMessengerIssue {
	return &EventMessengerIssue{
		Config: config,
	}
}
