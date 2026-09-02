# Default versions
ARG GO_VERSION=1.26
ARG ARANGO_VERSION=3.11.6
ARG RESTIC_VERSION=0.17.0

# Build stage
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

# These arguments are automatically populated by docker buildx
ARG TARGETOS
ARG TARGETARCH

RUN apk update && apk add --no-cache upx

# Set the working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY cmd/ cmd/
COPY internal/ internal/

# Build the application for the requested target architecture
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \ 
        -a -installsuffix cgo \
        -o /bin/app cmd/backup/main.go

RUN upx -q -9 /bin/app

# Final runtime stage
FROM arangodb:${ARANGO_VERSION}

# Redeclare ARGs for the second stage
ARG TARGETARCH
ARG RESTIC_VERSION

# Install dependencies and dynamically fetch restic based on architecture
RUN apk update && \
    apk add --no-cache postgresql14 curl bzip2 redis && \
    RESTIC_ARCH=${TARGETARCH:-amd64} && \
    curl -L "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${RESTIC_ARCH}.bz2" -o "/tmp/restic.bz2" && \
    bunzip2 /tmp/restic.bz2 && \
    mv /tmp/restic /usr/local/bin/restic && \
    chmod +x /usr/local/bin/restic && \
    rm -f /tmp/restic.bz2

COPY --from=builder /bin/app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
