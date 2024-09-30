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

