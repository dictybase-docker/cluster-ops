package main

type EmailDeployment struct {
	Name    string
	Secrets EmailSecrets
}

type EmailSecrets struct {
	Name   string
	Keys   EmailSecretKeys
	Values EmailSecretValues
}

type EmailSecretKeys struct {
	Cc                     string
	Domain                 string
	MailgunAPIKey          string
	PublicationAPIEndpoint string
	Sender                 string
	SenderName             string
}

type EmailSecretValues struct {
	Cc                     string
	Domain                 string
	MailgunAPIKey          string
	PublicationAPIEndpoint string
	Sender                 string
	SenderName             string
}

type IssueDeployment struct {
	Name    string
	Secrets IssueSecrets
}

type IssueSecrets struct {
	Name   string
	Keys   IssueSecretKeys
	Values IssueSecretValues
}

type IssueSecretKeys struct {
	Owner      string
	Repository string
	Token      string
}

type IssueSecretValues struct {
	Owner      string
	Repository string
	Token      string
}

type EventMessengerConfig struct {
	Namespace       string
	Nats            NatsProperties
	Image           ImageConfig
	LogLevel        string
	IssueDeployment IssueDeployment
	EmailDeployment EmailDeployment
}

type EventMessenger struct {
	Config *EventMessengerConfig
}

type SecretKeyPair struct {
	Name string
	Key  string
}

type ConfigMapPair struct {
	Name string
	Key  string
}

type ImageConfig struct {
	Name       string
	Tag        string
	PullPolicy string
}

type NatsProperties struct {
	Subject string
}
