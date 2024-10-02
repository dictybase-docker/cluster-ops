package main

import (
	"fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type EventMessengerEmailConfig struct {
	LogLevel string
  Namespace string
	Nats     NatsProperties
	Image    ImageConfig
  Email EmailDeployment 
}

type EmailDeployment struct {
	Name     string
	Secrets  EmailSecrets
}

type EmailSecrets struct {
	Name string
	Keys EmailSecretKeys
}

type EmailSecretKeys struct {
	Cc                     string
	Domain                 string
	MailgunApiKey          string
	PublicationApiEndpoint string
	Sender                 string
	SenderName             string
}

func ReadEventMessengerEmailConfig(ctx *pulumi.Context) (*EventMessengerEmailConfig, error) {
	conf := config.New(ctx, "event-messenger")
	eventMessengerEmail := &EventMessengerEmailConfig{}
	if err := conf.TryObject("properties", eventMessengerEmail); err != nil {
		return nil, fmt.Errorf("failed to read event-messenger config: %w", err)
	}
	return eventMessengerEmail, nil
}

