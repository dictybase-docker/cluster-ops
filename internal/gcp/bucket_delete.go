package gcp

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/storage"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"google.golang.org/api/iterator"
)

// BucketDeleteAPI enumerates and removes (injected; production
// StorageDeleteAPI wraps the storage client, versions included).
type BucketDeleteAPI interface {
	CountObjects(ctx context.Context, name string) (int64, error) // objects + noncurrent versions
	DeleteObjects(ctx context.Context, name string) error         // recursive purge incl. versions
	DeleteBucket(ctx context.Context, name string) error
}

type deleteWork struct {
	api  BucketDeleteAPI
	name string
	mode MutationMode
}

// DeleteBucketState validates first (count + Confirmed), then purges objects
// and deletes the bucket once. DryRun reports intent and calls no mutation
// seam; missing Confirmed errors before any mutation.
func DeleteBucketState(api BucketDeleteAPI, name string, mode MutationMode) IOE.IOEither[error, Result] {
	w := deleteWork{api: api, name: name, mode: mode}
	return F.Pipe1(w.preflightDelete(), IOE.Chain(w.purgeAndDelete))
}

func (w deleteWork) bucketName() string { return "gs://" + w.name }

func (w deleteWork) preflightDelete() IOE.IOEither[error, int64] {
	return F.Pipe1(
		IOE.TryCatchError(w.rawCount),
		IOE.MapLeft[int64](w.wrapCountErr),
	)
}

func (w deleteWork) rawCount() (int64, error) {
	return w.api.CountObjects(context.Background(), w.name)
}

func (w deleteWork) wrapCountErr(err error) error {
	return fmt.Errorf("count objects in %s: %w", w.bucketName(), err)
}

// purgeAndDelete is the mutation half: every gate (dry-run, confirmation)
// runs before the first mutating seam is reachable.
func (w deleteWork) purgeAndDelete(count int64) IOE.IOEither[error, Result] {
	if w.mode.DryRun {
		return IOE.Of[error](Result{Name: w.bucketName(), Action: ActionDryRun})
	}
	if !w.mode.Confirmed {
		return IOE.Left[Result](
			fmt.Errorf("%s: confirmation required — re-run with --confirm yes", w.bucketName()))
	}
	if count > 0 {
		return F.Pipe1(w.purgeObjects(), IOE.Chain(w.afterPurge))
	}
	return w.afterPurge(struct{}{})
}

func (w deleteWork) purgeObjects() IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, w.api.DeleteObjects(context.Background(), w.name)
	})
}

func (w deleteWork) afterPurge(struct{}) IOE.IOEither[error, Result] {
	return F.Pipe1(
		IOE.TryCatchError(func() (struct{}, error) {
			return struct{}{}, w.api.DeleteBucket(context.Background(), w.name)
		}),
		IOE.Map[error](w.deletedResult),
	)
}

func (w deleteWork) deletedResult(struct{}) Result {
	return Result{Name: w.bucketName(), Action: ActionUpdated}
}

// ---- production adapter ----

// StorageDeleteAPI adapts *storage.Client to BucketDeleteAPI. A versioned
// bucket is purged object-by-object (all generations) before the bucket
// delete, so noncurrent versions cannot block or resurrect the operation.
type StorageDeleteAPI struct {
	Client *storage.Client
}

func (a StorageDeleteAPI) CountObjects(ctx context.Context, name string) (int64, error) {
	it := a.Client.Bucket(name).Objects(ctx, &storage.Query{Versions: true})
	var n int64
	for {
		if _, err := it.Next(); err != nil {
			if errors.Is(err, iterator.Done) {
				return n, nil
			}
			return 0, err
		}
		n++
	}
}

func (a StorageDeleteAPI) DeleteObjects(ctx context.Context, name string) error {
	bucket := a.Client.Bucket(name)
	it := bucket.Objects(ctx, &storage.Query{Versions: true})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return nil
		}
		if err != nil {
			return err
		}
		obj := bucket.Object(attrs.Name)
		if attrs.Generation != 0 {
			obj = obj.Generation(attrs.Generation)
		}
		if err := obj.Delete(ctx); err != nil && !IsNotFound(err) {
			return err
		}
	}
}

func (a StorageDeleteAPI) DeleteBucket(ctx context.Context, name string) error {
	return a.Client.Bucket(name).Delete(ctx)
}
