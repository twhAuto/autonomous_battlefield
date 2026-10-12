# Builds the API, engine, AI gateway, and agent binaries and packages their runtime configuration
# and authored game content in a non-root image.

FROM golang:1.26.9-alpine AS build

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

FROM alpine:3.23

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
COPY --chown=battlefield:battlefield scenarios /app/scenarios
COPY --chown=battlefield:battlefield rulesets /app/rulesets
COPY --chown=battlefield:battlefield content /app/content

USER battlefield

EXPOSE 8080 8081 8083 8084

ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/config.yaml"]
