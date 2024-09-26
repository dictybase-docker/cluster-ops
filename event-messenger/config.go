package main

type SecretConfig struct {
  name string
  key string
}

type ConfigMapEntry struct {
  name string
  key string
}

type EventMessengerEmailConfig struct {
	Namespace string
	Image     string
	Replicas  int
  MailgunApiKey SecretConfig
  Domain ConfigMapEntry
  Sender ConfigMapEntry
  SenderName ConfigMapEntry
  Cc ConfigMapEntry
	PublicationApiEndpoint ConfigMapEntry
}
