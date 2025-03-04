package gcp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	serviceusage "cloud.google.com/go/serviceusage/apiv1"
	"cloud.google.com/go/serviceusage/apiv1/serviceusagepb"
	"github.com/urfave/cli/v2"
)

func EnableAPIs(cliContext *cli.Context) error {
	projectId := cliContext.String("project")
	apiFilePath := cliContext.String("api-file-path")
	ctx := context.Background()

	// Check if the API file exists
	if _, err := os.Stat(apiFilePath); os.IsNotExist(err) {
		return fmt.Errorf("API file %s does not exist", apiFilePath)
	}

	c, err := serviceusage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Service Usage service: %v", err)
	}
	defer c.Close()

	services, err := readAPIsFromFile(apiFilePath)
	if err != nil {
		return fmt.Errorf("failed to read APIs file: %v", err)
	}

	// A single request can enable a maximum of 20 services at a time. If more
	// than 20 services are specified, the request will fail, and no state changes
	// will occur.
	serviceChunks := chunkServices(services, 20)

	for _, chunk := range serviceChunks {
		req := &serviceusagepb.BatchEnableServicesRequest{
			Parent:     fmt.Sprintf("projects/%s", projectId),
			ServiceIds: chunk,
		}

		op, err := c.BatchEnableServices(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to enable APIs: %v", err)
		}

		if _, err := op.Wait(ctx); err != nil {
			return fmt.Errorf("failed to wait for API enabling: %v", err)
		}
	}

	return nil
}

func DisableAPIs(cliContext *cli.Context) error {
	projectId := cliContext.String("project")
	apiFilePath := cliContext.String("api-file-path")
	ctx := context.Background()

	// Check if the API file exists
	if _, err := os.Stat(apiFilePath); os.IsNotExist(err) {
		return fmt.Errorf("API file %s does not exist", apiFilePath)
	}

	c, err := serviceusage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Service Usage service: %v", err)
	}
	defer c.Close()

	services, err := readAPIsFromFile(apiFilePath)
	if err != nil {
		return fmt.Errorf("failed to read APIs file: %v", err)
	}

	for _, service := range services {
		req := &serviceusagepb.DisableServiceRequest{
			Name: fmt.Sprintf("projects/%s/services/%s", projectId, service),
		}

		op, err := c.DisableService(ctx, req)

		if err != nil {
			return fmt.Errorf("failed to enable APIs: %v", err)
		}

		if _, err := op.Wait(ctx); err != nil {
			return fmt.Errorf("failed to wait for API enabling: %v", err)
		}
	}

	return nil
}

func readAPIsFromFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var apis []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// Trim whitespace.
		api := strings.TrimSpace(scanner.Text())
		// Ignore empty lines.
		if api != "" {
			apis = append(apis, api)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return apis, nil
}

func chunkServices(services []string, chunkSize int) [][]string {
	var chunks [][]string
	for i := 0; i < len(services); i += chunkSize {
		end := min(i+chunkSize, len(services))
		chunks = append(chunks, services[i:end])
	}
	return chunks
}
