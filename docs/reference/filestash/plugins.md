# Filestash Plugin Details

Back to: [Filestash Deploy Guide](../../filestash-deploy.md)

## Table of Contents

- [Plugins](#plugins)
- [Lock Files](#lock-files)
- [License and Review Procedure](#license-and-review-procedure)
- [just filestash build-plugins](#just-filestash-build-plugins)
- [just filestash lock-office-deps](#just-filestash-lock-office-deps)
- [How the Pod Loads Plugins](#how-the-pod-loads-plugins)

## Plugins

| Plugin | Kind | Delivery |
|---|---|---|
| `theme_apple` | Runtime ZIP (`css`, `patch`) | Bundle image |
| `application_adobe`, `application_biomed`, `application_photography` | Runtime ZIP (`xdg-open`) | Bundle image |
| `application_office` | Runtime ZIP (`middleware` WASM, `xdg-open`) | Built in the bundle image from the Filestash source |
| `plg_search_stateless`, `plg_image_c` | Compiled into `machines/filestash` | Configuration only |

Each runtime ZIP runs JavaScript in the browser of every public visitor. A `patch` module changes the frontend JavaScript. Filestash has no viewer for `fasta`, `gff`, or `gff3`, and it maps `vcf` to `text/vcard`.

## Lock Files

| File | Content |
|---|---|
| `filestash/plugins/plugins.lock.yaml` | For each vendor ZIP: `url`, `sha256`, `size`, `modules`, `license`, `reviewer`, `notes`. For `office`: the source `ref` (40-character commit). |
| `filestash/plugins/office-deps.lock.yaml` | The LibreOffice WASM files for `office`, with `url` and `sha256`. |

**The vendor ZIPs state no license.** Before the first build, a person gets the license of each ZIP, reviews each `manifest.json` and `patch` file, and fills `license` and `reviewer`. Refer to [License and Review Procedure](#license-and-review-procedure).

## License and Review Procedure

Do these steps before the first build and after each `sha256` change.

1. Download the ZIP from its `url` and check its `sha256`.
2. Read `manifest.json`. Compare the module types with `modules` in the lock file.
3. For a `patch` module, read the diff. It must change only styles, icons, or labels. It must not add a network request or send data to another host.
4. For an `xdg-open` module, search the JavaScript for remote URLs (`http://`, `https://`, `fetch(`, `XMLHttpRequest`). A viewer must load its code from the ZIP only.
5. Record the license. The vendor ZIP files state none. Ask the vendor (`support@filestash.app`) for the license in writing. Read the third-party license files inside the ZIP (for example `jsmol` in `application_biomed`).
6. Set `license`, `reviewer` (your name and the date), and `notes` in the lock file, and commit it.

**An unknown license is not a permission.** A private registry still copies
and runs the files. If the license is unknown or does not permit the use,
remove the plugin from the lock file and stop. Do not build the bundle with it.

For `office`, also review `office-deps.lock.yaml` after `lock-office-deps`. LibreOffice is MPL-2.0.

## just filestash build-plugins

### What It Does

Builds and pushes `dictybase/filestash-plugins:<tag>`, a `busybox` image with all ZIP files under `/plugins/`.

### Behavior

1. Refuses to build while any lock entry has an invalid `sha256` or `ref`, or an empty `license` or `reviewer`.
2. Downloads each vendor ZIP and office file into a temporary build context and checks its `sha256`. A mismatch stops the build.
3. Builds `filestash/plugins/Dockerfile`. Stage 1 fetches the Filestash source at `office.ref` and compiles `application_office.zip`.
4. Pushes the image and prints the digest and the two `set-config` commands for `plugins.image`.

### Flags

| Flag | Required | Default | Notes |
|---|---|---|---|
| `--tag` | Yes | none | Not `latest`. |
| `--image` | No | `dictybase/filestash-plugins` | |
| `--push` | No | `yes` | `no` builds only. |
| `--lock`, `--office-lock` | No | the lock files above | For tests. |

## just filestash lock-office-deps

Downloads each file in `office-deps.lock.yaml` and writes its `sha256` into the file. The upstream URLs are `zetaoffice_latest` URLs, so the checksums change when the vendor publishes a new release. Run it on a host that can reach `cdn.zetaoffice.net`, then review the diff before you commit it.

## How the Pod Loads Plugins

The `plugins` init container deletes every `*.zip` in `/app/data/state/plugins/` and copies the bundle ZIP files. A ZIP that staff copy by hand does not survive a restart. `verify` checks that `/about` lists each plugin.
