FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go test ./... && \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/ledger-service ./cmd/ledger-service

FROM debian:bookworm-slim

COPY --from=build /out/ledger-service /usr/local/bin/ledger-service
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ledger-service"]
