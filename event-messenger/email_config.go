package main

import (
  "fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type EventMessengerEmailConfig struct {
  Name string
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
	eventMessengerEmail := &EventMessengerEmailConfig{}
	if err := conf.TryObject("properties", eventMessengerEmail); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger-email config: %w", err)
	}
	return eventMessengerEmail, nil
}

