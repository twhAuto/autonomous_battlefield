# Builds the API, engine, AI gateway, and agent binaries and packages them with their runtime
# configuration in a non-root image. Game content and migrations are embedded in the binaries.

# Base image: golang:1.26.9-alpine
FROM golang@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/server \
      ./cmd/server \
    && CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/engine \
      ./cmd/engine \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ai-gateway ./cmd/aigateway

# Base image: alpine:3.23
FROM alpine@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0

RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 battlefield \
    && adduser -S -D -H -u 10001 -G battlefield battlefield

WORKDIR /app

COPY --from=build --chown=battlefield:battlefield /out/server /app/server
COPY --from=build --chown=battlefield:battlefield /out/engine /app/engine
COPY --from=build --chown=battlefield:battlefield /out/agent /app/agent
COPY --from=build --chown=battlefield:battlefield /out/ai-gateway /app/ai-gateway
# Pre-rename path for the same binary; the gateway Deployment still runs it so that
# image-only rollbacks to older images keep working. Remove with that command.
COPY --from=build --chown=battlefield:battlefield /out/ai-gateway /app/mcp
COPY --chown=battlefield:battlefield config.yaml /app/config.yaml

USER battlefield

EXPOSE 8080 8081 8083 8084

ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/config.yaml"]
