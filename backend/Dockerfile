FROM golang:1.26.5-alpine AS build

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
      ./cmd/engine

FROM alpine:3.23

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 battlefield \
    && adduser -S -D -H -u 10001 -G battlefield battlefield

WORKDIR /app

COPY --from=build --chown=battlefield:battlefield /out/server /app/server
COPY --from=build --chown=battlefield:battlefield /out/engine /app/engine
COPY --chown=battlefield:battlefield config.yaml /app/config.yaml
COPY --chown=battlefield:battlefield scenarios /app/scenarios

USER battlefield

EXPOSE 8080 8081

ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/config.yaml"]
