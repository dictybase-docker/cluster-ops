package main

import (
	"fmt"

	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/helm/v3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ChartConfig struct {
	Name       string
	Repository string
	Version    string
}

type ImageConfig struct {
	Tag string
}

type RedisOperatorConfig struct {
	Chart          ChartConfig
	Image          ImageConfig
	Namespace      string
	WatchNamespace string `json:"watchNamespace"`
}

type RedisOperator struct {
	Config *RedisOperatorConfig
}

func ReadConfig(ctx *pulumi.Context) (*RedisOperatorConfig, error) {
	conf := config.New(ctx, "redis-operator")
	redisConfig := &RedisOperatorConfig{}
	if err := conf.TryObject("properties", redisConfig); err != nil {
		return nil, fmt.Errorf("failed to read redis-operator config: %w", err)
	}
	return redisConfig, nil
}

func NewRedisOperator(config *RedisOperatorConfig) *RedisOperator {
	return &RedisOperator{
		Config: config,
	}
}

func (rds *RedisOperator) Install(ctx *pulumi.Context) error {
	// Install the Helm chart
	_, err := helm.NewChart(ctx, "redis-operator", helm.ChartArgs{
		Chart:     pulumi.String(rds.Config.Chart.Name),
		Version:   pulumi.String(rds.Config.Chart.Version),
		Namespace: pulumi.String(rds.Config.Namespace),
		FetchArgs: helm.FetchArgs{
			Repo: pulumi.String(rds.Config.Chart.Repository),
		},
		Values: pulumi.Map{
			"image": pulumi.Map{
				"tag": pulumi.String(rds.Config.Image.Tag),
			},
			"redisOperator": pulumi.Map{
				"watchNamespace": pulumi.String(
					rds.Config.WatchNamespace,
				),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to install Helm chart: %w", err)
	}

	return nil
}

func Run(ctx *pulumi.Context) error {
	redisConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	redisOperator := NewRedisOperator(redisConfig)

	if err := redisOperator.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
