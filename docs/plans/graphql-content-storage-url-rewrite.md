# On-the-Fly Content Storage URL Normalization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite hardcoded S3 storage origins inside `Content.content` Lexical JSON on the fly in `graphql-server` so stored asset URLs (`https://<any-host>/<s3-bucket>/<s3-bucket-path>/...`) always resolve to the server's configured `--s3-storage-api` endpoint, while leaving `Content.content` untouched when the URLs already match `--s3-storage-api` or when no asset URLs exist.

**Architecture:** `graphql-server` already stores `--s3-storage-api` (`registry.S3STORAGE`), `--s3-bucket` (`registry.S3Bucket`), and `--s3-bucket-path` (`registry.S3BucketPath`) in `registry.Registry` at server startup (`internal/app/server/server.go`). We pass `StorageURL`, `Bucket`, and `BucketPath` from `Resolver.Content()` (`internal/graphql/resolver/resolver.go`) into `content.ContentResolver` (`internal/graphql/resolver/content/content.go`) and apply a pure `fp-go` v2 normalizer in `(*ContentResolver).Content(ctx, obj)`. When a matched URL already starts with the configured `StorageURL` (or when `StorageURL`, `Bucket`, or `BucketPath` is empty), the normalizer returns the original string unchanged without reallocating or altering the URL.

**Tech Stack:** Go 1.26, `github.com/IBM/fp-go/v2` (`F`, `O`, `P`, `S`), `github.com/dictyBase/fp-go-loom`, `regexp`, `strings`, `github.com/stretchr/testify`.

**Spec:** Derived from `cluster-ops` `serialized_json` inspection (`34` URLs across `13` documents matching `https://<origin>/editor/assets/<YYYY-MM-DD>/<uuid>`).

## Global Constraints

- **All Go code MUST use `fp-go` v2 functional patterns** (`github.com/IBM/fp-go/v2` + `github.com/dictyBase/fp-go-loom`).
- **No imperative nil/default/boolean branching in pure helpers:** Use `O.FromPredicate`, `O.Map`, `O.GetOrElse`, `F.Pipe*`, and `F.Flow*`.
- **No-op identity guarantee:** When a URL inside `obj.Data.Attributes.Content` already has the same origin as `rcs.StorageURL` (for example, `https://slimestorage.dictycr.org/editor/assets/...` when `StorageURL` is `https://slimestorage.dictycr.org`), or when `obj.Data.Attributes.Content` contains no `/<bucket>/<bucketPath>/` URLs, the returned string must be identical to the input string.
- **Scope of replacement:** Only rewrite URLs whose path starts with `/<s3-bucket>/<s3-bucket-path>/` (for example, `/editor/assets/`). External URLs in the same Lexical JSON document (such as `https://dictybase.org/...`, `https://ncbi.nlm.nih.gov/...`, or `https://doi.org/...`) must never be modified.
- **Linter & test gates:** Must pass `golangci-lint fmt`, `golangci-lint run ./...`, `gotestsum --format pkgname-and-test-fails --format-hide-empty-pkg -- ./...`, and `gopls check -severity=hint` with zero warnings or errors.

## Review Focus

- **Identical origin is a strict no-op:** If `StorageURL` is `https://storage.dictybase.dev` (or `https://storage.dictybase.dev/` with a trailing slash) and the content JSON already contains `https://storage.dictybase.dev/editor/assets/2026-09-22/0b9876d2-8d5b-47db-b21b-b08b9a0b8524`, `NormalizeContentStorageURLs` must return the exact string unchanged.
- **Bidirectional portability (`dev` ↔ `prod`):** Content restored from `dcr-experiments` (`https://storage.dictybase.dev/editor/assets/...`) served by `dcr-kube1` (`StorageURL: "https://slimestorage.dictycr.org"`) rewrites to `https://slimestorage.dictycr.org/editor/assets/...`, and content restored from `dcr-kube1` back to `dcr-experiments` rewrites to `https://storage.dictybase.dev/editor/assets/...`.
- **Non-storage URLs in the same JSON stay untouched:** A Lexical JSON document containing both `https://storage.dictybase.dev/editor/assets/2024-10-25/333dc249` and `https://github.com/dictyBase/migration-data` must only rewrite the `/editor/assets/` URL and leave the GitHub URL untouched.
- **Missing or unconfigured `StorageURL` / `Bucket` / `BucketPath`:** In unit tests or environments where `StorageURL`, `Bucket`, or `BucketPath` is empty, `NormalizeContentStorageURLs` must safely return the original `Content` string without panicking (`GetRecord` on `registry.Registry` panics when a key is unset, so `Resolver.Content()` must read registry keys safely or `Registry` must provide safe access).
- **Multiple occurrences in one document:** A document like `frontpage-teach` with 16 embedded image URLs must have all 16 `/editor/assets/...` URLs rewritten in a single pass while preserving every surrounding JSON quote, escape, and character.

---

### Task 1: Safe S3 Config Lookup in `Resolver.Content()` and `MockRegistry`

**Files:**
- Modify: `internal/registry/registry.go:115-125`
- Modify: `internal/graphql/mocks/registry_mock.go:130-150`
- Modify: `internal/graphql/resolver/resolver.go:117-125`
- Test: `internal/graphql/resolver/content_test.go`

**Interfaces:**
- Consumes: `registry.S3STORAGE`, `registry.S3Bucket`, `registry.S3BucketPath` in `internal/registry/registry.go`.
- Produces:
  - Safe `GetRecord(key string) string` in `internal/registry/registry.go` and `internal/graphql/mocks/registry_mock.go` (returns `""` instead of panicking when `connMap` is nil or `key` is absent, matching `GetAPIEndpoint`).
  - `content.ContentResolver` fields `StorageURL string`, `Bucket string`, `BucketPath string` populated in `(*Resolver).Content()`.

- [ ] **Step 1: Write the failing test in `internal/graphql/resolver/content_test.go`**

Add `TestResolverContentWiresStorageConfig(t *testing.T)` in `internal/graphql/resolver/content_test.go`:
```go
func TestResolverContentWiresStorageConfig(t *testing.T) {
	t.Parallel()
	reg := registry.NewRegistry()
	reg.AddAPIEndpoint(registry.S3STORAGE, "https://slimestorage.dictycr.org")
	reg.AddRecord(registry.S3Bucket, "editor")
	reg.AddRecord(registry.S3BucketPath, "assets")

	r := NewResolver(reg, nil, mocks.TestLogger())
	cr, ok := r.Content().(*content.ContentResolver)
	require.True(t, ok)
	require.Equal(t, "https://slimestorage.dictycr.org", cr.StorageURL)
	require.Equal(t, "editor", cr.Bucket)
	require.Equal(t, "assets", cr.BucketPath)
}

func TestResolverContentEmptyRegistryDoesNotPanic(t *testing.T) {
	t.Parallel()
	reg := &mocks.MockRegistry{}
	r := NewResolver(reg, nil, mocks.TestLogger())
	require.NotPanics(t, func() {
		_ = r.Content()
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `gotestsum --format pkgname-and-test-fails --format-hide-empty-pkg -- ./internal/graphql/resolver/...`
Expected: FAIL (`cr.StorageURL`, `cr.Bucket`, `cr.BucketPath` undefined).

- [ ] **Step 3: Update `GetRecord` in `internal/registry/registry.go` and `internal/graphql/mocks/registry_mock.go`, and wire fields in `internal/graphql/resolver/resolver.go`**

1. In `internal/registry/registry.go`, make `GetRecord(key string) string` return `""` when the key is not found (same non-panicking lookup as `GetAPIEndpoint`):
   ```go
   func (coll *collection) GetRecord(key string) string {
   	val, _ := coll.connMap.Get(key)
   	output, _ := val.(string)
   	return output
   }
   ```
2. In `internal/graphql/mocks/registry_mock.go`, guard `mr.ConnMap == nil` in `GetRecord` and `GetAPIEndpoint` so tests with `&mocks.MockRegistry{}` never panic:
   ```go
   func (mr *MockRegistry) GetRecord(key string) string {
   	if mr.ConnMap == nil {
   		return ""
   	}
   	val, _ := mr.ConnMap.Get(key)
   	output, _ := val.(string)
   	return output
   }

   func (mr MockRegistry) GetAPIEndpoint(key string) string {
   	if mr.ConnMap == nil {
   		return ""
   	}
   	v, _ := mr.ConnMap.Get(key)
   	endpoint, _ := v.(string)
   	return endpoint
   }
   ```
3. In `internal/graphql/resolver/content/content.go`, add `StorageURL`, `Bucket`, and `BucketPath` to `ContentResolver`:
   ```go
   type ContentResolver struct {
   	Client     pb.ContentServiceClient
   	UserClient authentication.LogtoClient
   	Logger     *logrus.Entry
   	StorageURL string
   	Bucket     string
   	BucketPath string
   }
   ```
4. In `internal/graphql/resolver/resolver.go`, pass the three values in `(*Resolver).Content()`:
   ```go
   func (rrs *Resolver) Content() generated.ContentResolver {
   	return &content.ContentResolver{
   		Client:     rrs.GetContentClient(registry.CONTENT),
   		UserClient: rrs.GetAuthClient(registry.AUTH),
   		Logger:     rrs.Logger,
   		StorageURL: rrs.GetAPIEndpoint(registry.S3STORAGE),
   		Bucket:     rrs.GetRecord(registry.S3Bucket),
   		BucketPath: rrs.GetRecord(registry.S3BucketPath),
   	}
   }
   ```

- [ ] **Step 4: Run test to verify it passes**

Run: `gotestsum --format pkgname-and-test-fails --format-hide-empty-pkg -- ./internal/graphql/resolver/...`
Expected: PASS

---

### Task 2: Implement Pure `fp-go` URL Normalizer in `internal/graphql/resolver/content`

**Files:**
- Create: `internal/graphql/resolver/content/storage_url.go`
- Create: `internal/graphql/resolver/content/storage_url_test.go`
- Modify: `internal/graphql/resolver/content/content.go:130-142`

**Interfaces:**
- Consumes: `ContentResolver.{StorageURL, Bucket, BucketPath}` from Task 1.
- Produces:
  - `StorageRewriteConfig` struct (`StorageURL`, `Bucket`, `BucketPath`).
  - `NormalizeContentStorageURLs(cfg StorageRewriteConfig) func(string) string` in `internal/graphql/resolver/content/storage_url.go`.
  - Updated `(*ContentResolver).Content(ctx context.Context, obj *pb.Content) (string, error)` in `internal/graphql/resolver/content/content.go`.

- [ ] **Step 1: Write the failing unit tests in `internal/graphql/resolver/content/storage_url_test.go`**

Create `internal/graphql/resolver/content/storage_url_test.go` covering all 6 cases:
1. **Different origin (`dev` → `prod`)**: `https://storage.dictybase.dev/editor/assets/2024-10-25/333dc249-7b17-4ede-9e99-47aed2cbadd3` inside Lexical JSON rewrites to `https://slimestorage.dictycr.org/editor/assets/2024-10-25/333dc249-7b17-4ede-9e99-47aed2cbadd3`.
2. **Reverse direction (`prod` → `dev`)**: `https://slimestorage.dictycr.org/editor/assets/2026-09-22/0b9876d2-8d5b-47db-b21b-b08b9a0b8524` with `StorageURL: "https://storage.dictybase.dev"` rewrites to `https://storage.dictybase.dev/editor/assets/2026-09-22/0b9876d2-8d5b-47db-b21b-b08b9a0b8524`.
3. **Identical origin (no-op)**: When `StorageURL` is `https://storage.dictybase.dev` (or `https://storage.dictybase.dev/` with trailing slash) and the content already has `https://storage.dictybase.dev/editor/assets/...`, the returned string equals the input string.
4. **Mixed URLs in one Lexical JSON document**: Rewrites multiple `/editor/assets/...` URLs while leaving `https://dictybase.org/papers/123` and `https://storage.dictybase.dev/other-bucket/file.png` untouched.
5. **Empty / unconfigured `StorageRewriteConfig`**: When `StorageURL`, `Bucket`, or `BucketPath` is `""`, returns the original string unchanged.
6. **`(*ContentResolver).Content(ctx, obj)` integration**: Calls `rcs.Content(ctx, pbContent)` and verifies the resolved GraphQL field has normalized URLs.

- [ ] **Step 2: Run tests to verify they fail**

Run: `gotestsum --format pkgname-and-test-fails --format-hide-empty-pkg -- ./internal/graphql/resolver/content/...`
Expected: FAIL (`NormalizeContentStorageURLs` and `StorageRewriteConfig` undefined).

- [ ] **Step 3: Implement `internal/graphql/resolver/content/storage_url.go` and wire `(*ContentResolver).Content` using `fp-go` v2**

In `internal/graphql/resolver/content/storage_url.go`, build the normalizer using `github.com/IBM/fp-go/v2` (`F`, `O`, `P`, `S`) with zero imperative `if` branching:

```go
package content

import (
	"fmt"
	"regexp"
	"strings"

	F "github.com/IBM/fp-go/v2/function"
	O "github.com/IBM/fp-go/v2/option"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
)

// StorageRewriteConfig holds the target S3 public origin and bucket/path prefix.
type StorageRewriteConfig struct {
	StorageURL string
	Bucket     string
	BucketPath string
}

type compiledRewriter struct {
	targetPrefix string
	pattern      *regexp.Regexp
}

var isConfigComplete = F.Pipe2(
	P.And(
		F.Pipe1(S.IsNonEmpty, P.ContraMap(func(c StorageRewriteConfig) string { return trimSlash(c.StorageURL) })),
	)(
		F.Pipe1(S.IsNonEmpty, P.ContraMap(func(c StorageRewriteConfig) string { return trimSlashes(c.Bucket) })),
	),
	P.And(
		F.Pipe1(S.IsNonEmpty, P.ContraMap(func(c StorageRewriteConfig) string { return trimSlashes(c.BucketPath) })),
	),
)

func trimSlash(s string) string {
	return strings.TrimRight(s, "/")
}

func trimSlashes(s string) string {
	return strings.Trim(s, "/")
}

func buildCompiledRewriter(cfg StorageRewriteConfig) compiledRewriter {
	origin := trimSlash(cfg.StorageURL)
	segmentPrefix := fmt.Sprintf("/%s/%s/", trimSlashes(cfg.Bucket), trimSlashes(cfg.BucketPath))
	escapedSegment := regexp.QuoteMeta(segmentPrefix)
	return compiledRewriter{
		targetPrefix: origin + segmentPrefix,
		pattern:      regexp.MustCompile(`https?://[^"'\s)\\]+` + escapedSegment),
	}
}

func replaceMatchIfDifferent(rw compiledRewriter) func(string) string {
	hasTargetPrefix := F.Pipe1(
		S.IsNonEmpty,
		P.ContraMap(func(match string) string {
			return strings.TrimPrefix(match, rw.targetPrefix)
		}),
	)
	_ = hasTargetPrefix
	isSamePrefix := func(match string) bool {
		return match == rw.targetPrefix
	}
	rewriteMatch := F.Flow2(
		O.FromPredicate( isSamePrefix ),
		O.GetOrElse(F.Constant(rw.targetPrefix)),
	)
	return func(raw string) string {
		return rw.pattern.ReplaceAllStringFunc(raw, rewriteMatch)
	}
}

// NormalizeContentStorageURLs returns a pure string transformer that rewrites
// any `http(s)://<host>/<bucket>/<bucketPath>/` prefix in a Lexical JSON string
// to `<StorageURL>/<bucket>/<bucketPath>/`. When the URL already uses
// `StorageURL`, or when `cfg` has any empty field, the input string is returned
// unchanged.
func NormalizeContentStorageURLs(cfg StorageRewriteConfig) func(string) string {
	return F.Pipe3(
		cfg,
		O.FromPredicate(isConfigComplete),
		O.Map(F.Flow2(buildCompiledRewriter, replaceMatchIfDifferent)),
		O.GetOrElse(F.Constant(F.Identity[string])),
	)
}
```
*(Remove the unused `hasTargetPrefix` lines when writing the file.)*

And in `internal/graphql/resolver/content/content.go`, update `Content`:
```go
func (rcs *ContentResolver) Content(
	ctx context.Context,
	obj *pb.Content,
) (string, error) {
	normalize := NormalizeContentStorageURLs(StorageRewriteConfig{
		StorageURL: rcs.StorageURL,
		Bucket:     rcs.Bucket,
		BucketPath: rcs.BucketPath,
	})
	return F.Pipe1(obj.Data.Attributes.Content, normalize), nil
}
```

- [ ] **Step 4: Run tests, nested-call check, linter, and gopls gate**

Run:
```bash
golangci-lint fmt
golangci-lint run ./...
gotestsum --format pkgname-and-test-fails --format-hide-empty-pkg -- ./...
gopls check -severity=hint internal/graphql/resolver/content/storage_url.go internal/graphql/resolver/content/storage_url_test.go internal/graphql/resolver/content/content.go internal/graphql/resolver/resolver.go internal/registry/registry.go
```
Expected: PASS with 0 errors and 0 hints.

- [ ] **Step 5: Commit**

```bash
git add internal/registry/registry.go internal/graphql/mocks/registry_mock.go internal/graphql/resolver/resolver.go internal/graphql/resolver/content_test.go internal/graphql/resolver/content/content.go internal/graphql/resolver/content/storage_url.go internal/graphql/resolver/content/storage_url_test.go
git commit -m "feat(content): normalize hardcoded storage URLs in content JSON on the fly"
```
