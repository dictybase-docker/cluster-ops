package gcp

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/storage"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/option"
)

type CreateBucketParams struct {
	Ctx         context.Context
	Client      *storage.Client
	ProjectID   string
	BucketName  string
	RegionName  string
	MaxVersions int
}

func CreateKopsStateBucket(cliContext *cli.Context) error {
	ctx := context.Background()
	params := getBucketParams(cliContext)

	if err := validateEnvironment(); err != nil {
		return err
	}

	storageClient, err := createStorageClient(ctx)
	if err != nil {
		return err
	}
	defer storageClient.Close()

	bucket := storageClient.Bucket(params.BucketName)
	exists, err := bucketExists(ctx, bucket)
	if err != nil {
		return err
	}

	if !exists {
		if err := setupNewBucket(ctx, params, bucket); err != nil {
			return err
		}
	} else {
		fmt.Printf("Bucket %s already exists. Updating configuration.\n", params.BucketName)
	}

	if err := setLifecycleConfig(ctx, bucket, params.MaxVersions); err != nil {
		return err
	}

	fmt.Println("Bucket setup complete. Ready for use as kops state store.")
	return nil
}

func getBucketParams(cliContext *cli.Context) CreateBucketParams {
	return CreateBucketParams{
		ProjectID:   cliContext.String("project"),
		BucketName:  cliContext.String("bucket"),
		MaxVersions: cliContext.Int("max-versions"),
		RegionName:  cliContext.String("region"),
	}
}

func validateEnvironment() error {
	requiredVars := []string{
		"GOOGLE_APPLICATION_CREDENTIALS",
		"KOPS_CLUSTER_NAME",
		"KOPS_STATE_STORE",
		"KUBECONFIG",
		"SSH_KEY",
		"KUBERNETES_VERSION",
	}
	missingVars := checkRequiredVars(requiredVars)
	if len(missingVars) > 0 {
		return fmt.Errorf(
			"the following required environment variables are not set: %v",
			missingVars,
		)
	}

	return nil
}

func createStorageClient(ctx context.Context) (*storage.Client, error) {
	return storage.NewClient(
		ctx,
		option.WithCredentialsFile(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")),
	)
}

func bucketExists(
	ctx context.Context,
	bucket *storage.BucketHandle,
) (bool, error) {
	_, err := bucket.Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("error checking bucket: %v", err)
	}
	return true, nil
}

func setupNewBucket(
	ctx context.Context,
	params CreateBucketParams,
	bucket *storage.BucketHandle,
) error {
	if err := createBucket(params); err != nil {
		return err
	}

	if err := enableBucketVersioning(ctx, bucket); err != nil {
		return err
	}

	if err := enableSoftDelete(ctx, bucket); err != nil {
		return err
	}

	return nil
}

func createBucket(params CreateBucketParams) error {
	fmt.Printf("Creating bucket: %s\n", params.BucketName)
	bucket := params.Client.Bucket(params.BucketName)
	if err := bucket.Create(params.Ctx, params.ProjectID, &storage.BucketAttrs{
		Location: params.RegionName,
	}); err != nil {
		return fmt.Errorf("failed to create bucket: %v", err)
	}
	return nil
}

func enableBucketVersioning(
	ctx context.Context,
	bucket *storage.BucketHandle,
) error {
	_, err := bucket.Update(ctx, storage.BucketAttrsToUpdate{
		VersioningEnabled: true,
	})
	if err != nil {
		return fmt.Errorf("failed to enable bucket versioning: %v", err)
	}
	return nil
}

func enableSoftDelete(ctx context.Context, bucket *storage.BucketHandle) error {
	_, err := bucket.Update(ctx, storage.BucketAttrsToUpdate{
		RetentionPolicy: &storage.RetentionPolicy{
			RetentionPeriod: 30 * 24 * 60 * 60 * 1000000000, // 30 days in nanoseconds
		},
	})
	if err != nil {
		return fmt.Errorf("failed to enable soft delete: %v", err)
	}
	return nil
}

func setLifecycleConfig(
	ctx context.Context,
	bucket *storage.BucketHandle,
	maxVersions int,
) error {
	lifecycleRules := []storage.LifecycleRule{
		{
			Action: storage.LifecycleAction{
				Type: "Delete",
			},
			Condition: storage.LifecycleCondition{
				NumNewerVersions: int64(maxVersions),
				Liveness:         storage.Archived,
			},
		},
	}

	_, err := bucket.Update(ctx, storage.BucketAttrsToUpdate{
		Lifecycle: &storage.Lifecycle{
			Rules: lifecycleRules,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to set lifecycle config: %v", err)
	}
	return nil
}

func checkRequiredVars(vars []string) []string {
	var missingVars []string
	for _, evar := range vars {
		if _, exists := os.LookupEnv(evar); !exists {
			missingVars = append(missingVars, evar)
		}
	}
	return missingVars
}
