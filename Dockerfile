# Use the official Golang image to create a build artifact.
# This is known as a multi-stage build.
FROM golang:latest AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code
COPY . .

# Build the Go app
# -o /app/main creates the binary in the /app directory
# CGO_ENABLED=0 is important for creating a static binary that can run in a minimal container
# -ldflags="-s -w" strips debugging information, reducing the binary size
RUN CGO_ENABLED=0 go build -o /app/main -ldflags="-s -w" ./cmd/webhook/main.go

# ---
# Start a new, second stage from a minimal base image
# where we will copy the binary from the first stage.
FROM gcr.io/distroless/static-debian11

# Copy the static binary from the builder stage
COPY --from=builder /app/main /

# (Optional) Copy the certificates if you were to build them into the image
# In our case, we will mount them from a Kubernetes secret, which is better.
# COPY --from=builder /app/certs /etc/webhook/certs/

# Command to run the executable
ENTRYPOINT ["/main"]
