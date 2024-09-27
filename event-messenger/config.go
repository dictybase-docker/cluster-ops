package main

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
