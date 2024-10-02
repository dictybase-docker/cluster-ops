package main

import (
	"fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type EventMessengerIssueConfig struct {
	LogLevel string
  Namespace string
	Nats     NatsProperties
	Image    ImageConfig
	Issue    IssueDeployment
}

type IssueDeployment struct {
	Name    string
	Secrets IssueSecrets
}

type IssueSecrets struct {
	Name string
	Keys IssueSecretKeys
}

type IssueSecretKeys struct {
	Owner      string
	Repository string
	Token      string
}

func ReadEventMessengerIssueConfig(ctx *pulumi.Context) (*EventMessengerIssueConfig, error) {
	conf := config.New(ctx, "event-messenger")
	eventMessengerIssue := &EventMessengerIssueConfig{}
	if err := conf.TryObject("properties", eventMessengerIssue); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger config: %w", err)
	}
	return eventMessengerIssue, nil
}

