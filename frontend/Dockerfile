# Builds the Vite frontend and serves its static output with unprivileged Nginx and the
# runtime API proxy template.

# Base image: node:24-alpine
FROM node@sha256:44fce696f38ba8c987c4db89954051000bbd05ffd8d8659ba15ed4a8602e937f AS build

WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY . .

ARG VITE_API_URL=""
ENV VITE_API_URL=${VITE_API_URL}

RUN npm run build

# Base image: ghcr.io/nginx/nginx-unprivileged:1.30.6-alpine3.24-slim
FROM ghcr.io/nginx/nginx-unprivileged@sha256:ea8a305bb7bff1e622c8223a097bf5c7664d36b05371717e23dc69972e1924e6

USER root

RUN apk upgrade --no-cache \
    && mkdir -p /etc/nginx/templates \
    && chmod 0755 /etc/nginx/templates

ENV API_UPSTREAM=battlefield-api:8080
ENV NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1

# The Content-Security-Policy allows browser connections to the origin the bundle calls.
# An empty value keeps them on this origin, behind the /v1 proxy.
ARG VITE_API_URL=""
ENV CSP_API_ORIGIN=${VITE_API_URL}

# The private Jenkins workspace uses umask 0077; runtime permissions must not
# depend on the checkout owner or mode. These files contain no secrets.
COPY --chown=101:101 --chmod=0644 nginx-main.conf /etc/nginx/nginx.conf
COPY --chown=101:101 --chmod=0644 nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build --chown=101:101 /src/dist /usr/share/nginx/html

USER 101:101

EXPOSE 8080
