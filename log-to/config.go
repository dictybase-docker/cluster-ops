package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type LogtoConfig struct {
	Name           string
	Namespace      string
	Image          ImageConfig
	StorageClass   string
	DiskSize       string
	DatabaseSecret string
	Endpoint       string
	APIPort        int
	AdminPort      int
	Ingress        IngressConfig
}

type IngressConfig struct {
	TLSSecret    string
	BackendHosts []string
	Label        LabelConfig
}

type LabelConfig struct {
	Name  string
	Value string
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
