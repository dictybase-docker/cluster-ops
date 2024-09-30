package main

type SecretKeyPair struct {
  Name string
  Key string
}

type ConfigMapPair struct {
  Name string
  Key string
}

type ImageProperties struct {
  Repository string
  Tag string
  PullPolicy string
}
