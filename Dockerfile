# Keep the builder version at least as new as the Go version in go.mod.
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY . .
# Build a static binary for the distroless image.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# Run the binary as a non-root user in a minimal runtime image.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/server /server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
