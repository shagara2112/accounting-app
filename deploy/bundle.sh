#!/usr/bin/env bash
# Menyusun paket instalasi offline dari image yang sudah dibangun `make bundle`:
#   dist/lajur-<versi>-<os>-<arch>/        install.sh, docker-compose.yml, README.md, bundle.env,
#                                          images.tar.gz (image app + updater), SHA256SUMS
#   dist/lajur-<versi>-<os>-<arch>.tar.gz  folder di atas dalam satu berkas untuk disalin ke server
set -euo pipefail

VERSION="$1"
PLATFORM="${2:-linux/amd64}"
APP="lajur/lajur:${VERSION}"
UPDATER="lajur/lajur-updater:${VERSION}"
NAME="lajur-${VERSION}-${PLATFORM//\//-}"
OUT="dist/${NAME}"

for img in "$APP" "$UPDATER"; do
	arch=$(docker image inspect -f '{{.Os}}/{{.Architecture}}' "$img")
	[ "$arch" = "$PLATFORM" ] || { echo "Image $img untuk $arch, bukan $PLATFORM" >&2; exit 1; }
done

rm -rf "$OUT" "$OUT.tar.gz"
mkdir -p "$OUT"
docker save "$APP" "$UPDATER" | gzip -6 >"$OUT/images.tar.gz"
cp deploy/install.sh deploy/docker-compose.yml deploy/README.md "$OUT/"
cat >"$OUT/bundle.env" <<ENV
# Nama image di paket ini; dibaca install.sh. LAJUR_IMAGE dari lingkungan tetap didahulukan.
LAJUR_IMAGE="\${LAJUR_IMAGE:-${APP}}"
UPDATER_IMAGE="\${UPDATER_IMAGE:-${UPDATER}}"
LAJUR_VERSION=${VERSION}
ENV
(cd "$OUT" && shasum -a 256 images.tar.gz install.sh docker-compose.yml README.md bundle.env >SHA256SUMS)
# tar macOS (bsdtar) menyelipkan berkas ._* dan atribut tambahan; matikan supaya paket bersih di Linux.
if tar --version 2>/dev/null | grep -q bsdtar; then
	COPYFILE_DISABLE=1 tar --no-mac-metadata --no-xattrs -C dist -czf "$OUT.tar.gz" "$NAME"
else
	tar -C dist -czf "$OUT.tar.gz" "$NAME"
fi
echo "Paket: $OUT.tar.gz ($(du -h "$OUT.tar.gz" | cut -f1))"
