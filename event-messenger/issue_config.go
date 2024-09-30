package main

import (
  "fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type EventMessengerIssueConfig struct {
	Replicas  int
  LogLevel string
  Nats NatsProperties
	Image     ImageProperties
  MailgunApiKey SecretConfig
  Domain ConfigMapEntry
  Sender ConfigMapEntry
  SenderName ConfigMapEntry
  Cc ConfigMapEntry
	PublicationApiEndpoint ConfigMapEntry
}

func ReadEventMessengerIssueConfig(ctx *pulumi.Context) (*EventMessengerEmailConfig, error) {
	conf := config.New(ctx, "event-messenger-email")
	graphqlConfig := &EventMessengerIssueConfig{}
	if err := conf.TryObject("properties", graphqlConfig); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger-email config: %w", err)
	}
	return graphqlConfig, nil
}

