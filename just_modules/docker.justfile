name := "database-backup"
namespace := "dictybase"
github_user := "cybersiddhu"
platform := "linux/amd64"
platform_multi := "linux/amd64,linux/arm64"
image := namespace + "/" + name
ghcr_image := "ghcr.io/" + image

# Internal helper to construct build arguments
[private]
build-args go_ver arango_ver restic_ver:
    @echo "--build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }}"

# Build the backup docker image for the target platform
[group('docker')]
build-backup tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    docker buildx build --platform {{ platform }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ image }}:{{ tag }} .

# Build for GitHub Container Registry
[group('docker')]
build-backup-ghcr tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    docker buildx build --platform {{ platform }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ ghcr_image }}:{{ tag }} .

# Push to GitHub Container Registry
[group('docker')]
push-backup-ghcr tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    echo $GITHUB_REGISTRY_TOKEN | docker login ghcr.io -u {{ github_user }} --password-stdin
    docker buildx build --platform {{ platform }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ ghcr_image }}:{{ tag }} --push .

# Build and push multi-arch image (amd64 + arm64) to Docker Hub
[group('docker')]
push-backup-multi tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    docker buildx build --platform {{ platform_multi }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ image }}:{{ tag }} --push .

# Push multi-arch image to GitHub Container Registry
[group('docker')]
push-backup-ghcr-multi tag="latest" go_ver="1.25" arango_ver="3.11.6" restic_ver="0.17.0":
    echo $GITHUB_REGISTRY_TOKEN | docker login ghcr.io -u {{ github_user }} --password-stdin
    docker buildx build --platform {{ platform_multi }} \
        --build-arg GO_VERSION={{ go_ver }} --build-arg ARANGO_VERSION={{ arango_ver }} --build-arg RESTIC_VERSION={{ restic_ver }} \
        -f build/package/Dockerfile -t {{ ghcr_image }}:{{ tag }} --push .
