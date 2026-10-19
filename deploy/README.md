# Memasang Lajur di server kantor

Panduan ini untuk pemasang (owner atau teknisi). Lajur berjalan di satu server Linux di jaringan
kantor dan dibuka dari browser di PC lain lewat HTTPS.

## Kebutuhan

- Ubuntu 22.04+ atau Debian 12+, 64-bit, dengan akses root (sudo).
- RAM minimal 4 GB (disarankan 8 GB), disk kosong minimal 20 GB.
- IP LAN tetap (atur reservasi DHCP di router), dan internet untuk lisensi serta pembaruan.
- Port 443 belum dipakai program lain. Bila port 80 sudah dipakai (mis. dasbor CasaOS), unduhan
  `ca.crt` dan pengalihan HTTP otomatis pindah ke port 8080 (atau pilih sendiri dengan
  `LAJUR_HTTP_PORT=<port>`).
- Zona waktu server diatur ke zona kantor (`sudo timedatectl set-timezone Asia/Jakarta`); bila server
  masih UTC, Lajur memakai Asia/Jakarta. Untuk WITA/WIT jalankan skrip dengan
  `LAJUR_TIMEZONE=Asia/Makassar` atau `Asia/Jayapura`.
- Memori PostgreSQL disetel otomatis dari RAM server (bisa diubah di `/opt/lajur/.env`, `PG_*`).
- Client ID dan license key dari License Console (aplikasi **Lajur**), serta alamat License Console.

## Instal

Lajur dikirim sebagai paket offline `lajur-<versi>-linux-amd64.tar.gz` (±50 MB). Salin ke server
(USB atau `scp`), lalu:

```bash
tar xzf lajur-1.0.0-linux-amd64.tar.gz
cd lajur-1.0.0-linux-amd64
sudo bash install.sh
```

Image Lajur dimuat dari paket dan diperiksa dengan `SHA256SUMS`. Server tetap butuh internet untuk
memasang Docker (bila belum ada), mengunduh image PostgreSQL, dan check-in lisensi.

Skrip ini:

1. Memeriksa OS, RAM, disk, dan port, lalu memasang Docker dari repositori resmi Docker bila belum ada.
2. Membuat CA lokal (`/opt/lajur/ca`) dan sertifikat HTTPS untuk hostname dan semua IP LAN server.
3. Membuat `/opt/lajur/.env` (izin 600): kata sandi PostgreSQL acak, kunci aplikasi
   (`LAJUR_APP_KEY`), token updater, dan zona waktu server.
4. Membuat pasangan kunci backup. **Kunci privatnya hanya ditampilkan sekali.** Serahkan ke Pemilik
   dan simpan di dua tempat, misalnya pengelola kata sandi dan cetakan di brankas. Server hanya
   menyimpan kunci publik.
5. Menjalankan `docker compose up -d`, lalu menampilkan alamat Lajur.

Menjalankan ulang skrip aman: konfigurasi, CA, dan sertifikat yang sudah ada tetap dipakai.

## Memperbarui dengan paket versi baru

Salin dan ekstrak paket versi baru, lalu jalankan `sudo bash install.sh` dari foldernya. Skrip
membuat backup dulu (pembaruan dibatalkan bila backup gagal), memuat image baru, mengganti nama
image di `/opt/lajur/.env`, lalu menjalankan ulang Lajur. Data, konfigurasi, dan sertifikat tetap.
Migrasi basis data berjalan otomatis saat Lajur menyala.

## Memasang sertifikat CA di PC pengguna

Sekali per PC, supaya browser tidak memperingatkan. Unduh `http://<ip-server>/ca.crt`, lalu:

- **Windows**: klik dua kali → *Install Certificate* → *Local Machine* →
  *Place all certificates in the following store* → *Trusted Root Certification Authorities*.
  Tutup lalu buka lagi Chrome atau Edge.
- **macOS**: buka di *Keychain Access* → keychain *System* → klik dua kali sertifikat
  *Lajur CA …* → *Trust* → *Always Trust*.
- **Firefox**: *Settings → Privacy & Security → Certificates → View Certificates → Authorities →
  Import*, lalu centang *Trust this CA to identify websites*.

## Setelah instal

1. Buka `https://<ip-server>/`. Setup wizard meminta lisensi, PT pertama, dan akun Pemilik,
   lalu meminta pemasangan 2FA.
2. Di *Sistem → Backup*, atur jam backup harian dan tujuan salinan tambahan (folder NAS yang
   di-mount atau S3). Folder NAS harus di-mount di host di bawah `/opt/lajur/data` supaya terlihat
   dari container, mis. `/opt/lajur/data/nas` → isi `/var/lib/lajur/nas`.

## Perintah berguna

```bash
cd /opt/lajur
docker compose ps                                  # status container
docker compose logs -f app                         # log aplikasi
sudo bash install.sh status                        # status dan health
sudo bash install.sh renew-cert                    # sertifikat baru (IP berubah / habis 825 hari)
docker compose exec app lajur backup               # backup sekarang
docker compose exec app lajur reset-2fa <email>    # Pemilik kehilangan ponsel 2FA
docker compose exec -T app lajur reset-password <email> <<< 'kata-sandi-baru-min-10'
```

## Memulihkan backup

Butuh kunci privat backup dari Pemilik.

```bash
cd /opt/lajur
docker compose stop app
docker compose run --rm app restore /var/lib/lajur/backups/<file>.lajurbk   # tempel kunci saat diminta
docker compose start app
```

Untuk pindah ke server baru: instal Lajur di server baru, salin file backup ke
`/opt/lajur/data/backups/`, pulihkan seperti di atas, lalu minta pemilik aplikasi mereset instalasi
di License Console dan aktifkan ulang lisensi dari *Sistem → Lisensi → Ganti license key*.
`LAJUR_APP_KEY` dari `.env` lama juga perlu disalin; tanpa kunci itu pengguna harus memasang ulang 2FA.
