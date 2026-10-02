# Builds the Vite frontend and serves its static output with unprivileged Nginx and the
# runtime API proxy template.

FROM node:24-alpine AS build

WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY . .

ARG VITE_API_URL=""
ENV VITE_API_URL=${VITE_API_URL}

RUN npm run build

FROM ghcr.io/nginx/nginx-unprivileged:1.30.4-alpine3.24-slim@sha256:bcf91d2c73ab64fa1c4ac7fbac5ac523057c8af7d553ab9251c7aef38c260979

USER root

RUN apk upgrade --no-cache \
    && mkdir -p /etc/nginx/templates \
    && chmod 0755 /etc/nginx/templates

ENV API_UPSTREAM=battlefield-api:8080
ENV NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1

# The private Jenkins workspace uses umask 0077; runtime permissions must not
# depend on the checkout owner or mode. These files contain no secrets.
COPY --chown=101:101 --chmod=0644 nginx-main.conf /etc/nginx/nginx.conf
COPY --chown=101:101 --chmod=0644 nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build --chown=101:101 /src/dist /usr/share/nginx/html

USER 101:101

EXPOSE 8080
