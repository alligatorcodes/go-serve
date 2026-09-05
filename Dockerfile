# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/go-serve ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/go-serve /app/go-serve
COPY --from=build /src/config/example.toml /app/config/example.toml

EXPOSE 8080 9901
USER nonroot:nonroot
ENTRYPOINT ["/app/go-serve"]
CMD ["-config", "/app/config/example.toml"]
