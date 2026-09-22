# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.27.0
ARG NODE_VERSION=24
ARG VERSION=unknown

FROM node:${NODE_VERSION}-bookworm-slim AS ui-builder
WORKDIR /src/ui
COPY ui/package*.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:${GO_VERSION}-bookworm AS go-builder
WORKDIR /src
ARG VERSION
ARG TARGETARCH
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /src/ui/dist ./ui/dist
RUN GOOS=linux GOARCH="${TARGETARCH}" \
	go build -buildvcs=false -trimpath \
	-ldflags "-s -w -X main.version=${VERSION} -X github.com/oschina/mothx/internal/version.Version=${VERSION} -X github.com/oschina/mothx/internal/ua.Version=${VERSION}" \
	-o /out/mothx ./cmd/mothx

FROM ubuntu:24.04 AS runtime-ubuntu
ARG VERSION=unknown
LABEL org.opencontainers.image.source="https://github.com/oschina/mothx" \
	org.opencontainers.image.title="MothX" \
	org.opencontainers.image.description="MothX terminal AI coding assistant" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.base.name="ubuntu:24.04"
RUN apt-get update \
	&& apt-get install -y --no-install-recommends bash ca-certificates curl git openssh-client \
	&& rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
COPY --from=go-builder /out/mothx /usr/local/bin/mothx
USER root
ENTRYPOINT ["mothx"]

FROM debian:bookworm-slim AS runtime-debian
ARG VERSION=unknown
LABEL org.opencontainers.image.source="https://github.com/oschina/mothx" \
	org.opencontainers.image.title="MothX" \
	org.opencontainers.image.description="MothX terminal AI coding assistant" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.base.name="debian:bookworm-slim"
RUN apt-get update \
	&& apt-get install -y --no-install-recommends bash ca-certificates curl git openssh-client \
	&& rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
COPY --from=go-builder /out/mothx /usr/local/bin/mothx
USER root
ENTRYPOINT ["mothx"]

FROM fedora:42 AS runtime-fedora
ARG VERSION=unknown
LABEL org.opencontainers.image.source="https://github.com/oschina/mothx" \
	org.opencontainers.image.title="MothX" \
	org.opencontainers.image.description="MothX terminal AI coding assistant" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.base.name="fedora:42"
RUN dnf -y install bash ca-certificates curl git openssh-clients \
	&& dnf clean all \
	&& rm -rf /var/cache/dnf
WORKDIR /workspace
COPY --from=go-builder /out/mothx /usr/local/bin/mothx
USER root
ENTRYPOINT ["mothx"]

FROM alpine:3.22 AS runtime-alpine
ARG VERSION=unknown
LABEL org.opencontainers.image.source="https://github.com/oschina/mothx" \
	org.opencontainers.image.title="MothX" \
	org.opencontainers.image.description="MothX terminal AI coding assistant" \
	org.opencontainers.image.licenses="MIT" \
	org.opencontainers.image.version="${VERSION}" \
	org.opencontainers.image.base.name="alpine:3.22"
RUN apk add --no-cache bash ca-certificates curl git openssh-client
WORKDIR /workspace
COPY --from=go-builder /out/mothx /usr/local/bin/mothx
USER root
ENTRYPOINT ["mothx"]
