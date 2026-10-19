# syntax=docker/dockerfile:1
# Image aplikasi Lajur: API, frontend (go:embed), worker backup, dan lisensi dalam satu binary.
# Dibangun dari tag rilis:  make image VERSION=1.2.3  atau  make bundle VERSION=1.2.3 (paket offline).
# Build rilis hanya mempercayai kunci lisensi di internal/platform/license/keys/release/.
# Tahap web dan Go berjalan di arsitektur mesin build lalu di-cross-compile ke arsitektur target
# (mis. build di Mac arm64 untuk server linux/amd64), sehingga tidak perlu emulasi CPU.

FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=0.0.0-dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags release -trimpath \
      -ldflags "-s -w -X lajur/internal/platform/buildinfo.Version=${VERSION}" \
      -o /out/lajur ./cmd/lajur

FROM alpine:3.22
# postgresql16-client untuk pg_dump/pg_restore (backup), tzdata untuk jadwal backup per zona waktu.
RUN apk add --no-cache ca-certificates tzdata postgresql16-client \
 && adduser -D -H -u 10001 -s /sbin/nologin lajur \
 && mkdir -p /var/lib/lajur /etc/lajur/tls \
 && chown 10001:10001 /var/lib/lajur
COPY --from=build /out/lajur /usr/local/bin/lajur
USER 10001
ENV LAJUR_DATA_DIR=/var/lib/lajur
EXPOSE 8443 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s \
  CMD wget -qO- --no-check-certificate https://127.0.0.1:8443/healthz >/dev/null || exit 1
ENTRYPOINT ["lajur"]
CMD ["serve"]
