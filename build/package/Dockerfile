# Build stage
FROM golang:1.22.7-alpine AS builder

RUN apk update && apk add upx

# Set the working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
ADD cmd cmd
ADD internal internal

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build \ 
	-a -installsuffix cgo \
	-o /bin/app cmd/backup/main.go

RUN upx -q -9 /bin/app

FROM dictybase/restic-redis-arangopg:main-3b271f9
COPY --from=builder /bin/app /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/app"]
