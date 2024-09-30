package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type EventMessengerEmail struct {
	Config *EventMessengerEmailConfig
}

type EventMessengerIssue struct {
	Config *EventMessengerIssueConfig
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

	if err := eventMessengerEmail.Install(ctx); err != nil {
		return err
	}

	return nil
}

func (eme *EventMessengerEmail) Install(ctx *pulumi.Context) error {
	_, err := eme.CreateDeployment(ctx)
	if err != nil {
		return err
	}

	return nil
}

func NewEventMessengerEmail(config *EventMessengerEmailConfig) *EventMessengerEmail {
	return &EventMessengerEmail{
		Config: config,
	}
}


