package gcp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	serviceusage "cloud.google.com/go/serviceusage/apiv1"
	"cloud.google.com/go/serviceusage/apiv1/serviceusagepb"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/urfave/cli/v2"
)

type disableInput struct {
	ProjectID                string
	APIFilePath              string
	DisableDependentServices bool
}

type disableWork struct {
	ProjectID                string
	Services                 []string
	DisableDependentServices bool
}

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
	outcome := F.Pipe3(
		disableInput{
			ProjectID:                cliContext.String("project"),
			APIFilePath:              cliContext.String("api-file-path"),
			DisableDependentServices: cliContext.Bool("disable-dependent-services"),
		},
		requireAPIFile,
		IOE.Chain(loadDisableServices),
		IOE.Chain(disableListedServices),
	)
	result := outcome()
	return E.ToError(result)
}

func requireAPIFile(in disableInput) IOE.IOEither[error, disableInput] {
	return F.Pipe2(
		IOE.TryCatchError(func() (os.FileInfo, error) {
			return os.Stat(in.APIFilePath)
		}),
		IOE.MapLeft[os.FileInfo](func(err error) error {
			return fmt.Errorf("API file %s does not exist: %w", in.APIFilePath, err)
		}),
		IOE.Map[error](F.Constant1[os.FileInfo](in)),
	)
}

func loadDisableServices(in disableInput) IOE.IOEither[error, disableWork] {
	return F.Pipe2(
		IOE.TryCatchError(func() ([]string, error) {
			return readAPIsFromFile(in.APIFilePath)
		}),
		IOE.MapLeft[[]string](func(err error) error {
			return fmt.Errorf("failed to read APIs file: %w", err)
		}),
		IOE.Map[error](func(services []string) disableWork {
			return disableWork{
				ProjectID:                in.ProjectID,
				Services:                 services,
				DisableDependentServices: in.DisableDependentServices,
			}
		}),
	)
}

func disableListedServices(work disableWork) IOE.IOEither[error, struct{}] {
	use := disableWithClient(work)
	acquire := newUsageClient()
	return F.Pipe1(
		use,
		IOE.WithResource[struct{}](
			acquire,
			closeUsageClient,
		),
	)
}

func newUsageClient() IOE.IOEither[error, *serviceusage.Client] {
	return F.Pipe1(
		IOE.TryCatchError(func() (*serviceusage.Client, error) {
			ctx := context.Background()
			return serviceusage.NewClient(ctx)
		}),
		IOE.MapLeft[*serviceusage.Client](func(err error) error {
			return fmt.Errorf("failed to create Service Usage service: %w", err)
		}),
	)
}

func closeUsageClient(c *serviceusage.Client) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, c.Close()
	})
}

func disableWithClient(work disableWork) func(*serviceusage.Client) IOE.IOEither[error, struct{}] {
	return func(c *serviceusage.Client) IOE.IOEither[error, struct{}] {
		disableFn := disableOneService(c, work)
		return F.Pipe2(
			work.Services,
			IOE.TraverseArraySeq(disableFn),
			IOE.Map[error](discardDisableResults),
		)
	}
}

func discardDisableResults([]struct{}) struct{} {
	return struct{}{}
}

func disableOneService(
	c *serviceusage.Client,
	work disableWork,
) func(string) IOE.IOEither[error, struct{}] {
	return func(service string) IOE.IOEither[error, struct{}] {
		return disableNamedService(c, work, service)
	}
}

func disableRequest(
	projectID, service string,
	disableDependent bool,
) *serviceusagepb.DisableServiceRequest {
	return &serviceusagepb.DisableServiceRequest{
		Name:                     fmt.Sprintf("projects/%s/services/%s", projectID, service),
		DisableDependentServices: disableDependent,
	}
}

func disableNamedService(
	c *serviceusage.Client,
	work disableWork,
	service string,
) IOE.IOEither[error, struct{}] {
	ctx := context.Background()
	waitOp := waitDisableOperation(ctx)
	req := disableRequest(work.ProjectID, service, work.DisableDependentServices)
	return F.Pipe2(
		IOE.TryCatchError(func() (*serviceusage.DisableServiceOperation, error) {
			return c.DisableService(ctx, req)
		}),
		IOE.MapLeft[*serviceusage.DisableServiceOperation](func(err error) error {
			return fmt.Errorf("failed to disable APIs: %w", err)
		}),
		IOE.Chain(waitOp),
	)
}

func waitDisableOperation(
	ctx context.Context,
) func(*serviceusage.DisableServiceOperation) IOE.IOEither[error, struct{}] {
	return func(op *serviceusage.DisableServiceOperation) IOE.IOEither[error, struct{}] {
		return F.Pipe2(
			IOE.TryCatchError(func() (*serviceusagepb.DisableServiceResponse, error) {
				return op.Wait(ctx)
			}),
			IOE.MapLeft[*serviceusagepb.DisableServiceResponse](func(err error) error {
				return fmt.Errorf("failed to wait for API disabling: %w", err)
			}),
			IOE.Map[error](func(*serviceusagepb.DisableServiceResponse) struct{} {
				return struct{}{}
			}),
		)
	}
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
		line := scanner.Text()
		api := strings.TrimSpace(line)
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
