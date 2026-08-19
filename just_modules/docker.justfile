name := "database-backup"
namespace := "dictybase"
github_user := "cybersiddhu"
platform := "linux/amd64"
platform_multi := "linux/amd64,linux/arm64"
image := namespace + "/" + name
ghcr_image := "ghcr.io/" + image

# Internal helper to construct build arguments.
# Usage: just docker build-args --go-ver <v> --arango-ver <v> --restic-ver <v>
[arg("go_ver", long="go-ver", short="g", help="Go version")]
[arg("arango_ver", long="arango-ver", short="a", help="ArangoDB version")]
[arg("restic_ver", long="restic-ver", short="r", help="Restic version")]
[private]
build-args go_ver arango_ver restic_ver:
    @echo "--build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }}"

# Build the backup docker image for the target platform.
# Usage: just docker build-backup [--tag <t>] [--go-ver <v>] [--arango-ver <v>] [--restic-ver <v>]
[arg("tag", long="tag", short="t", help="Image tag")]
[arg("go_ver", long="go-ver", short="g", help="Go version")]
[arg("arango_ver", long="arango-ver", short="a", help="ArangoDB version")]
[arg("restic_ver", long="restic-ver", short="r", help="Restic version")]
[group('docker')]
[no-cd]
build-backup tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    docker buildx build --platform {{ platform }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ image }}:{{ tag }} .

# Build backup image for GitHub Container Registry.
# Usage: just docker build-backup-ghcr [--tag <t>] [--go-ver <v>] [--arango-ver <v>] [--restic-ver <v>]
[arg("tag", long="tag", short="t", help="Image tag")]
[arg("go_ver", long="go-ver", short="g", help="Go version")]
[arg("arango_ver", long="arango-ver", short="a", help="ArangoDB version")]
[arg("restic_ver", long="restic-ver", short="r", help="Restic version")]
[group('docker')]
[no-cd]
build-backup-ghcr tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    docker buildx build --platform {{ platform }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ ghcr_image }}:{{ tag }} .

# Push backup image to GitHub Container Registry.
# Usage: just docker push-backup-ghcr [--tag <t>] [--go-ver <v>] [--arango-ver <v>] [--restic-ver <v>]
[arg("tag", long="tag", short="t", help="Image tag")]
[arg("go_ver", long="go-ver", short="g", help="Go version")]
[arg("arango_ver", long="arango-ver", short="a", help="ArangoDB version")]
[arg("restic_ver", long="restic-ver", short="r", help="Restic version")]
[group('docker')]
[no-cd]
push-backup-ghcr tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    echo $GITHUB_REGISTRY_TOKEN | docker login ghcr.io -u {{ github_user }} --password-stdin
    docker buildx build --platform {{ platform }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ ghcr_image }}:{{ tag }} --push .

# Build and push multi-arch image (amd64 + arm64) to Docker Hub.
# Usage: just docker push-backup-multi [--tag <t>] [--go-ver <v>] [--arango-ver <v>] [--restic-ver <v>]
[arg("tag", long="tag", short="t", help="Image tag")]
[arg("go_ver", long="go-ver", short="g", help="Go version")]
[arg("arango_ver", long="arango-ver", short="a", help="ArangoDB version")]
[arg("restic_ver", long="restic-ver", short="r", help="Restic version")]
[group('docker')]
[no-cd]
push-backup-multi tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    docker buildx build --platform {{ platform_multi }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ image }}:{{ tag }} --push .

# Push multi-arch backup image to GitHub Container Registry.
# Usage: just docker push-backup-ghcr-multi [--tag <t>] [--go-ver <v>] [--arango-ver <v>] [--restic-ver <v>]
[arg("tag", long="tag", short="t", help="Image tag")]
[arg("go_ver", long="go-ver", short="g", help="Go version")]
[arg("arango_ver", long="arango-ver", short="a", help="ArangoDB version")]
[arg("restic_ver", long="restic-ver", short="r", help="Restic version")]
[group('docker')]
[no-cd]
push-backup-ghcr-multi tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    echo $GITHUB_REGISTRY_TOKEN | docker login ghcr.io -u {{ github_user }} --password-stdin
    docker buildx build --platform {{ platform_multi }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ ghcr_image }}:{{ tag }} --push .
