#!/usr/bin/env bash
# Skrip instal Lajur untuk server kantor (PLT-01): Ubuntu 22.04+ atau Debian 12+.
#
#   sudo bash install.sh              instal baru, atau jalankan ulang dengan aman (rahasia lama dipakai)
#   sudo bash install.sh renew-cert   buat ulang sertifikat HTTPS (mis. IP server berubah)
#   sudo bash install.sh status       tampilkan status container dan health
#
# Variabel opsional: LAJUR_IMAGE (default di bawah), LAJUR_DIR (default /opt/lajur),
# LAJUR_HOSTNAME (default hostname server), LAJUR_UPDATE_URL (manifest rilis di update server).
set -euo pipefail

LAJUR_DIR="${LAJUR_DIR:-/opt/lajur}"
LAJUR_IMAGE="${LAJUR_IMAGE:-ghcr.io/lajur/lajur:1.0.0}"
UPDATER_IMAGE="${UPDATER_IMAGE:-${LAJUR_IMAGE%:*}-updater:${LAJUR_IMAGE##*:}}"
HOSTNAME_FQDN="${LAJUR_HOSTNAME:-$(hostname -s 2>/dev/null || hostname)}"
CA_DAYS=3650
CERT_DAYS=825 # batas sertifikat yang masih diterima macOS/iOS untuk CA privat

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '  • %s\n' "$*"; }
warn() { printf '\033[33m  ! %s\033[0m\n' "$*" >&2; }
die() { printf '\033[31mGagal: %s\033[0m\n' "$*" >&2; exit 1; }

need_root() { [ "$(id -u)" -eq 0 ] || die "jalankan dengan sudo."; }

# ------------------------------------------------------------------ prasyarat

check_os() {
	[ -r /etc/os-release ] || die "tidak bisa membaca /etc/os-release."
	# shellcheck disable=SC1091
	. /etc/os-release
	case "${ID:-}" in
	ubuntu)
		[ "${VERSION_ID%%.*}" -ge 22 ] || die "butuh Ubuntu 22.04 atau lebih baru (terdeteksi ${VERSION_ID})."
		;;
	debian)
		[ "${VERSION_ID%%.*}" -ge 12 ] || die "butuh Debian 12 atau lebih baru (terdeteksi ${VERSION_ID})."
		;;
	*) die "OS ${PRETTY_NAME:-tidak dikenal} belum didukung; gunakan Ubuntu 22.04+ atau Debian 12+." ;;
	esac
	info "OS: ${PRETTY_NAME}"
	OS_ID="$ID"
	OS_CODENAME="${VERSION_CODENAME:-}"
}

check_resources() {
	local mem_mb disk_gb
	mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
	disk_gb=$(df -Pk "$(dirname "$LAJUR_DIR")" | awk 'NR==2 {print int($4/1048576)}')
	info "RAM ${mem_mb} MB, ruang disk kosong ${disk_gb} GB"
	[ "$mem_mb" -ge 3500 ] || warn "RAM di bawah 4 GB; Lajur tetap jalan, tetapi laporan besar akan lambat."
	[ "$disk_gb" -ge 20 ] || die "butuh minimal 20 GB ruang disk kosong untuk data dan backup."
	for port in 80 443; do
		if ss -ltnH "sport = :$port" 2>/dev/null | grep -q .; then
			if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^lajur-app'; then
				die "port $port sudah dipakai program lain; hentikan dulu program itu."
			fi
		fi
	done
}

install_docker() {
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
		info "Docker sudah terpasang: $(docker --version)"
		return
	fi
	bold "Memasang Docker dari repositori resmi Docker…"
	apt-get update -qq
	apt-get install -y -qq ca-certificates curl gnupg openssl >/dev/null
	install -m 0755 -d /etc/apt/keyrings
	curl -fsSL "https://download.docker.com/linux/${OS_ID}/gpg" -o /etc/apt/keyrings/docker.asc
	chmod a+r /etc/apt/keyrings/docker.asc
	echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${OS_ID} ${OS_CODENAME} stable" \
		>/etc/apt/sources.list.d/docker.list
	apt-get update -qq
	apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin >/dev/null
	systemctl enable --now docker >/dev/null
	info "Docker terpasang: $(docker --version)"
}

# ------------------------------------------------------------------ sertifikat

lan_ips() {
	hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' || true
}

# make_ca membuat CA lokal sekali saja; PC pengguna memasang lajur-ca.crt sebagai CA tepercaya.
make_ca() {
	local dir="$LAJUR_DIR/ca"
	mkdir -p "$dir" && chmod 700 "$dir"
	if [ -s "$dir/lajur-ca.key" ] && [ -s "$dir/lajur-ca.crt" ]; then
		info "CA lokal sudah ada."
		return
	fi
	openssl genrsa -out "$dir/lajur-ca.key" 4096 2>/dev/null
	chmod 600 "$dir/lajur-ca.key"
	openssl req -x509 -new -sha256 -days "$CA_DAYS" -key "$dir/lajur-ca.key" -out "$dir/lajur-ca.crt" \
		-subj "/O=Lajur/CN=Lajur CA ${HOSTNAME_FQDN}" \
		-extensions v3_ca -config <(printf '[req]\ndistinguished_name=dn\n[dn]\n[v3_ca]\nbasicConstraints=critical,CA:TRUE,pathlen:0\nkeyUsage=critical,keyCertSign,cRLSign\nsubjectKeyIdentifier=hash\n') 2>/dev/null
	info "CA lokal dibuat: $dir/lajur-ca.crt"
}

# make_cert membuat sertifikat server untuk hostname dan semua IP LAN server.
make_cert() {
	local ca="$LAJUR_DIR/ca" tls="$LAJUR_DIR/tls" san="DNS:${HOSTNAME_FQDN},DNS:${HOSTNAME_FQDN}.local,DNS:localhost,IP:127.0.0.1"
	local ip
	for ip in $(lan_ips); do san="$san,IP:$ip"; done
	mkdir -p "$tls"
	openssl genrsa -out "$tls/server.key" 2048 2>/dev/null
	openssl req -new -sha256 -key "$tls/server.key" -subj "/O=Lajur/CN=${HOSTNAME_FQDN}" -out "$tls/server.csr" 2>/dev/null
	openssl x509 -req -sha256 -days "$CERT_DAYS" -in "$tls/server.csr" -CA "$ca/lajur-ca.crt" -CAkey "$ca/lajur-ca.key" \
		-CAcreateserial -out "$tls/server.crt" \
		-extfile <(printf 'basicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=%s\n' "$san") 2>/dev/null
	cp "$ca/lajur-ca.crt" "$tls/ca.crt"
	rm -f "$tls/server.csr"
	# Container berjalan sebagai uid 10001; kunci hanya bisa dibaca container itu.
	chown -R 10001:10001 "$tls"
	chmod 600 "$tls/server.key"
	info "Sertifikat HTTPS untuk ${san//,/, }"
}

# ------------------------------------------------------------------ konfigurasi

rand_b64() { openssl rand -base64 "$1" | tr -d '\n'; }
rand_hex() { openssl rand -hex "$1"; }

write_env() {
	local env="$LAJUR_DIR/.env"
	if [ -s "$env" ]; then
		info "Konfigurasi lama dipakai: $env"
		return 1
	fi
	local pg_pass app_key tz updater_token
	pg_pass=$(rand_hex 24)
	app_key=$(rand_b64 32)
	updater_token=$(rand_hex 32)
	tz=$(timedatectl show -p Timezone --value 2>/dev/null || echo Asia/Jakarta)
	umask 077
	cat >"$env" <<EOF
# Dibuat install.sh $(date -Iseconds). Simpan file ini; tanpa LAJUR_APP_KEY rahasia 2FA dan
# license key yang tersimpan tidak bisa dibuka.
LAJUR_IMAGE=${LAJUR_IMAGE}
LAJUR_UPDATER_IMAGE=${UPDATER_IMAGE}
POSTGRES_PASSWORD=${pg_pass}
LAJUR_DATABASE_URL=postgres://lajur:${pg_pass}@db:5432/lajur?sslmode=disable
LAJUR_APP_KEY=${app_key}
LAJUR_LICENSE_MODE=remote
LAJUR_SECURE_COOKIES=true
LAJUR_MFA_REQUIRED=true
LAJUR_TIMEZONE=${tz:-Asia/Jakarta}
LAJUR_LISTEN=:8443
LAJUR_HTTP_REDIRECT=:8080
LAJUR_TLS_CERT=/etc/lajur/tls/server.crt
LAJUR_TLS_KEY=/etc/lajur/tls/server.key
LAJUR_CA_CERT=/etc/lajur/tls/ca.crt
LAJUR_DATA_DIR=/var/lib/lajur
LAJUR_UPDATER_TOKEN=${updater_token}
LAJUR_UPDATER_URL=http://updater:9000
LAJUR_UPDATE_URL=${LAJUR_UPDATE_URL:-}
EOF
	chmod 600 "$env"
	info "Konfigurasi dibuat: $env (izin 600)"
	return 0
}

# backup_key membuat pasangan kunci backup. Kunci privat hanya ditampilkan di layar sekali.
backup_key() {
	local env="$LAJUR_DIR/.env" out
	if grep -q '^LAJUR_BACKUP_PUBLIC_KEY=lajur-backup-pub-1:' "$env"; then
		return
	fi
	out=$(docker run --rm --entrypoint lajur "$LAJUR_IMAGE" backup-keygen)
	BACKUP_PRIVATE=$(printf '%s\n' "$out" | grep -o 'lajur-backup-key-1:[A-Za-z0-9_-]*')
	printf '%s\n' "$out" | grep '^LAJUR_BACKUP_PUBLIC_KEY=' >>"$env"
}

install_compose() {
	local src
	src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/docker-compose.yml"
	[ -f "$src" ] || die "docker-compose.yml tidak ditemukan di samping install.sh."
	cp "$src" "$LAJUR_DIR/docker-compose.yml"
	mkdir -p "$LAJUR_DIR/data"
	chown 10001:10001 "$LAJUR_DIR/data"
}

wait_healthy() {
	local i
	for i in $(seq 1 60); do
		if curl -fsk https://127.0.0.1/healthz >/dev/null 2>&1; then
			return 0
		fi
		sleep 2
	done
	return 1
}

summary() {
	local ip
	bold ""
	bold "Lajur siap."
	echo "  Buka dari PC di jaringan kantor:"
	echo "    https://${HOSTNAME_FQDN}/"
	for ip in $(lan_ips); do echo "    https://${ip}/"; done
	echo
	echo "  Sebelumnya, pasang sertifikat CA di setiap PC pengguna (sekali saja):"
	echo "    unduh http://$(lan_ips | head -1)/ca.crt atau salin ${LAJUR_DIR}/ca/lajur-ca.crt"
	echo "    Windows: klik dua kali → Install Certificate → Local Machine → Trusted Root Certification Authorities"
	echo "    macOS  : buka di Keychain Access → System → klik dua kali → Trust → Always Trust"
	echo
	echo "  Setup wizard di browser meminta Client ID, license key, dan alamat License Console."
	if [ -n "${BACKUP_PRIVATE:-}" ]; then
		echo
		bold "  KUNCI PRIVAT BACKUP — serahkan ke Pemilik, simpan di dua tempat (pengelola kata sandi dan"
		bold "  cetakan di brankas). Kunci ini TIDAK disimpan di server; tanpa kunci ini backup tidak bisa dipulihkan."
		echo
		echo "    ${BACKUP_PRIVATE}"
		echo
		read -r -p "  Ketik SUDAH setelah kunci disimpan: " ans </dev/tty
		[ "$ans" = "SUDAH" ] || warn "Kunci belum dikonfirmasi tersimpan; skrip tidak bisa menampilkannya lagi."
	fi
}

# ------------------------------------------------------------------ perintah

cmd_install() {
	need_root
	bold "Instal Lajur di ${LAJUR_DIR}"
	check_os
	install_docker
	check_resources
	mkdir -p "$LAJUR_DIR" && chmod 755 "$LAJUR_DIR"
	install_compose
	write_env || true
	make_ca
	[ -s "$LAJUR_DIR/tls/server.crt" ] || make_cert
	bold "Mengunduh image Lajur…"
	(cd "$LAJUR_DIR" && docker compose pull -q)
	backup_key
	bold "Menjalankan Lajur…"
	(cd "$LAJUR_DIR" && docker compose up -d)
	wait_healthy || die "Lajur belum menjawab di https://127.0.0.1/healthz; periksa: cd $LAJUR_DIR && docker compose logs app"
	summary
}

cmd_renew_cert() {
	need_root
	make_cert
	(cd "$LAJUR_DIR" && docker compose restart app)
	info "Sertifikat baru dipakai. CA tidak berubah, jadi PC pengguna tidak perlu dipasang ulang."
}

cmd_status() {
	(cd "$LAJUR_DIR" && docker compose ps)
	curl -fsk https://127.0.0.1/healthz && echo
}

main() {
	case "${1:-install}" in
	install) cmd_install ;;
	renew-cert) cmd_renew_cert ;;
	status) cmd_status ;;
	*) die "perintah tidak dikenal: $1 (install, renew-cert, status)" ;;
	esac
}

# Bisa di-source untuk menguji fungsinya tanpa menjalankan instalasi.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
	main "$@"
fi
