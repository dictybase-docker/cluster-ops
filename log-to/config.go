package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type LogtoConfig struct {
	Name         string
	Namespace    string
	Image        ImageConfig
	StorageClass string
	DiskSize     int
	Database     DatabaseProperties
	Endpoint     string
	APIPort      int
	AdminPort    int
}

type DatabaseProperties struct {
	Name     string
	User     string
	Host     string
	Port     int
	Password string
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
	Name string
	Tag  string
}

func ReadConfig(ctx *pulumi.Context) (*LogtoConfig, error) {
	conf := config.New(ctx, "log-to")
	logtoConfig := &LogtoConfig{}
	if err := conf.TryObject("properties", logtoConfig); err != nil {
		return nil, fmt.Errorf("failed to read log-to config: %w", err)
	}
	return logtoConfig, nil
}
