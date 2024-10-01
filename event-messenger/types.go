package main

type SecretKeyPair struct {
  Name string
  Key string
}

type ConfigMapPair struct {
  Name string
  Key string
}

type ImageConfig struct {
  Repository string
  Tag string
  PullPolicy string
}

type NatsProperties struct {
  Subject string
}
