package main

import (
  "fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type SecretConfig struct {
  Name string
  Key string
}

type ConfigMapEntry struct {
  Name string
  Key string
}

type ImageProperties struct {
  Repository string
  Tag string
  PullPolicy string
}

type NatsProperties struct {
  Subject string
}

type EventMessengerEmailConfig struct {
	Namespace string
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

func ReadEventMessengerEmailConfig(ctx *pulumi.Context) (*EventMessengerEmailConfig, error) {
	conf := config.New(ctx, "event-messenger-email")
	graphqlConfig := &EventMessengerEmailConfig{}
	if err := conf.TryObject("properties", graphqlConfig); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger-email config: %w", err)
	}
	return graphqlConfig, nil
}

