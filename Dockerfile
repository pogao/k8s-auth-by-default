FROM golang:latest AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app/main -ldflags="-s -w" ./cmd/webhook/main.go

# ---
FROM gcr.io/distroless/static-debian11
COPY --from=builder /app/main /
ENTRYPOINT ["/main"]
