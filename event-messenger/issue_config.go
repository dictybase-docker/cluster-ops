package main

import (
  "fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type EventMessengerIssueConfig struct {
  Name string
  Namespace string
	Replicas  int
  LogLevel string
  Nats NatsProperties
	Image     ImageConfig
  GithubRepo ConfigMapPair
  GithubOwner ConfigMapPair
  GithubToken SecretKeyPair
}

func ReadEventMessengerIssueConfig(ctx *pulumi.Context) (*EventMessengerIssueConfig, error) {
	conf := config.New(ctx, "event-messenger-issue")
	eventMessengerIssue := &EventMessengerIssueConfig{}
	if err := conf.TryObject("properties", eventMessengerIssue); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger-issue config: %w", err)
	}
	return eventMessengerIssue, nil
}

