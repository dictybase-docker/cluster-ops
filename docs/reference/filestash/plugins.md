# Filestash Plugin Details

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Plugins](#plugins)
- [Lock File](#lock-file)
- [just filestash build-plugins](#just-filestash-build-plugins)
- [How the Pod Loads Plugins](#how-the-pod-loads-plugins)

## Plugins

| Plugin | Kind | Delivery |
|---|---|---|
| `theme_apple` | Runtime ZIP (`css`, `patch`) | Bundle image |
| `application_adobe`, `application_biomed`, `application_photography` | Runtime ZIP (`xdg-open`) | Bundle image |
| [`application_docx`](https://www.filestash.app/2026/05/25/selfhosted-word/) | Runtime ZIP (`xdg-open`). DOCX only. | Bundle image |
| `plg_search_stateless`, `plg_image_c` | Compiled into `machines/filestash` | Configuration only |

Each runtime ZIP runs JavaScript in the browser of every public visitor. A `patch` module changes the frontend JavaScript. Filestash has no viewer for `fasta`, `gff`, or `gff3`, and it maps `vcf` to `text/vcard`.

## Lock File

| File | Content |
|---|---|
| `filestash/plugins/plugins.lock.yaml` | For each vendor ZIP: `url`, `sha256`, `size`, `modules`, `notes` |

## just filestash build-plugins

### What It Does

Builds and pushes `dictybase/filestash-plugins:<tag>`, a `busybox` image with all ZIP files under `/plugins/`.

Choose the tag for the new plugin bundle image. The tag is not a Filestash version.
For a second build on the same date, add `-2` to the tag, for example `2026.11.18-2`.

### Behavior

1. Stops if a lock entry has a missing or invalid `sha256`.
2. Downloads each vendor ZIP and checks its `sha256`. A mismatch stops the build.
3. Checks the DOCX manifest and loader. An invalid manifest or a missing loader stops the build.
4. Builds the image for `linux/amd64`.
5. Pushes the image and prints its tag and `sha256:` digest. It also prints the two `set-config` commands for the selected stack.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--tag` | Yes | none | A tag that you choose for the new bundle image. Do not use `latest`. |
| `--image` | No | `dictybase/filestash-plugins` | |
| `--push` | No | `yes` | `no` builds only, and prints no digest. |
| `--lock` | No | `filestash/plugins/plugins.lock.yaml` | For tests. |

## How the Pod Loads Plugins

The `plugins` init container deletes every `*.zip` in `/app/data/state/plugins/` and copies the bundle ZIP files. A ZIP that staff copy by hand does not survive a restart. `verify` checks that `/about` lists each plugin.
