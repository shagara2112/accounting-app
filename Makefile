SHELL := /bin/bash
GO ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)

# Variabel di .env (lihat .env.example) ikut diteruskan ke semua perintah.
-include .env
export

VERSION ?= 0.0.0-dev
REGISTRY ?= ghcr.io/lajur

PLATFORM ?= linux/amd64

.PHONY: dev-db dev-db-down sqlc web-install web-build web-dev build build-release image bundle run serve seed test vet

dev-db:          ## Nyalakan PostgreSQL pengembangan di 127.0.0.1:5433
	docker compose -f deploy/docker-compose.dev.yml up -d --wait

dev-db-down:     ## Matikan PostgreSQL pengembangan (data tetap tersimpan)
	docker compose -f deploy/docker-compose.dev.yml down

sqlc:            ## Hasilkan ulang kode akses data dari SQL
	docker run --rm -v "$(CURDIR)":/src -w /src sqlc/sqlc:1.29.0 generate

web-install:
	cd web && npm ci

web-build:       ## Build frontend ke web/dist (disisipkan ke binary)
	cd web && npm run build

web-dev:         ## Server pengembangan frontend di :5173, meneruskan /api ke :8080
	cd web && npm run dev

build: web-build ## Binary pengembangan di bin/lajur
	$(GO) build -o bin/lajur ./cmd/lajur

build-release: web-build ## Binary rilis: tanpa provider lisensi local dan tanpa seed-dev
	CGO_ENABLED=0 $(GO) build -tags release -trimpath -ldflags "-s -w -X lajur/internal/platform/buildinfo.Version=$(VERSION)" -o bin/lajur ./cmd/lajur

image:           ## Image Docker rilis (app dan updater): make image VERSION=1.2.3 REGISTRY=ghcr.io/lajur
	docker build --build-arg VERSION=$(VERSION) -t $(REGISTRY)/lajur:$(VERSION) .
	docker build --build-arg VERSION=$(VERSION) -f Dockerfile.updater -t $(REGISTRY)/lajur-updater:$(VERSION) .

bundle:          ## Paket instalasi offline untuk server klien: make bundle VERSION=1.0.0 (hasil di dist/)
	@[ "$(VERSION)" != "0.0.0-dev" ] || { echo "Isi VERSION, mis. make bundle VERSION=1.0.0"; exit 1; }
	@ls internal/platform/license/keys/release/*.pem >/dev/null 2>&1 || { echo "Kunci publik lisensi produksi belum ada di internal/platform/license/keys/release/"; exit 1; }
	docker buildx build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -t lajur/lajur:$(VERSION) --load .
	docker buildx build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -f Dockerfile.updater -t lajur/lajur-updater:$(VERSION) --load .
	bash deploy/bundle.sh $(VERSION) $(PLATFORM)

run:             ## Jalankan server dari kode sumber (migrasi otomatis) di LAJUR_LISTEN
	$(GO) run ./cmd/lajur serve

serve: build     ## Build frontend dan binary, lalu jalankan bin/lajur dengan variabel dari .env
	./bin/lajur serve

seed:            ## Isi data contoh Grup Arta
	$(GO) run ./cmd/lajur seed-dev

test:            ## Semua tes Go (integrasi butuh make dev-db) dan typecheck frontend
	$(GO) test ./...
	cd web && npm run typecheck

vet:
	$(GO) vet ./...
