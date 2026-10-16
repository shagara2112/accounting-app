# Lajur

Aplikasi keuangan dan akuntansi on-premise untuk grup usaha dan perusahaan menengah di Indonesia.
Kebutuhan produk ada di [PRD.md](PRD.md), desain teknis di [TDD.md](TDD.md).

Status:

- **Tahap 1 — fondasi platform**: login, sesi, peran per PT, audit trail berantai hash, license guard,
  kerangka frontend.
- **Tahap 2 — buku besar**: bagan akun berkode 1–9 dengan template jasa/konstruksi/retail, jurnal umum
  dan jurnal penyesuaian (seri JP, balik otomatis), void dengan jurnal pembalik, buku besar per akun,
  neraca lajur, dan tutup buku dua tahap (open → adjusting → locked).

- **Tahap 3 — penjualan, pembelian, kas-bank**: kontak (NPWP/NITKU), kode pajak bertanggal berlaku
  (PPN 11% s.d. 2024, PPN 12% × DPP 11/12 sejak 2025), faktur dan tagihan dengan siklus draf → posting →
  void, pelunasan dan pembayaran sebagian atau banyak dokumen sekaligus dengan PPh 23 / PPh final 4(2),
  umur piutang dan utang, serta penerimaan, pengeluaran, dan transfer kas-bank.

- **Tahap 4 — pajak dan Coretax**: profil pajak PT (NPWP, NITKU, PKP), daftar faktur pajak keluaran per
  masa dengan kode transaksi (04 untuk DPP nilai lain), ekspor XML impor massal Coretax, pencatatan nomor
  faktur yang terbit (manual atau impor CSV), pembatalan, rekap PPN per masa dengan kompensasi lebih bayar,
  dan rekap PPh yang dipotong. Faktur yang faktur pajaknya sudah terbit tidak bisa di-void.

  Eksportir XML (`coretax-faktur-keluaran/v1`) mengikuti struktur `TaxInvoiceBulk`; uji dulu dengan satu
  file kecil di Coretax, dan cocokkan kode satuan serta kode barang/jasa dengan referensi DJP.

- **Tahap 5 — proyek dan persediaan**: proyek sebagai dimensi baris jurnal (tagihan, kas, jurnal manual,
  faktur, pemakaian material, dan PPh final yang dipotong pemberi kerja), RAB per akun biaya, laba per
  proyek dan biaya aktual vs RAB; faktur uang muka, termin progres dengan potongan uang muka dan retensi,
  serta pelepasan retensi. Master produk dan jasa; stok satu gudang dengan HPP rata-rata tertimbang
  otomatis saat barang dijual, koreksi HPP saat ada transaksi bertanggal mundur, penyesuaian, pemakaian
  barang, stock opname, kartu stok, dan pencocokan nilai persediaan dengan buku besar.

- **Tahap 6 — laporan keuangan dan dashboard**: laporan posisi keuangan dan laba rugi komparatif,
  arus kas metode tidak langsung, neraca saldo, penjualan per pelanggan dan produk, pembelian per
  pemasok, kartu piutang dan utang per kontak, serta dashboard di Beranda (kas dan bank, piutang dan
  utang jatuh tempo, laba bulan ini vs bulan lalu, tren enam bulan). Semua laporan bisa diekspor ke
  Excel dan CSV (tetap bisa walau lisensi berakhir) dan dicetak ke PDF dari browser.

- **Tahap 7 — multi-entitas dan konsolidasi**: kontak bisa ditautkan ke PT lain dalam grup; faktur ke
  kontak itu otomatis membuat dan memposting tagihan cerminan di PT lawan (dan sebaliknya) di transaksi
  yang sama, dan void di satu sisi ikut mem-void pasangannya. Konsolidasi grup menjumlahkan PT yang
  dipilih ke bagan akun grup (kode sama otomatis, bisa dipetakan ulang bila isinya berbeda), lalu
  mengeliminasi piutang–utang dan penjualan–pembelian antar-PT; selisih yang tidak berpasangan
  ditampilkan untuk dibereskan. Konsolidasi MVP: kepemilikan 100% dan rupiah.

- **Draf transaksi kas**: staf bisa mencatat penerimaan atau pengeluaran tanpa memilih akun lawan.
  Draf belum bernomor dan belum masuk buku besar; akuntan melengkapi akun lalu memposting. Jumlah draf
  yang menunggu tampil di halaman Kas dan di Beranda.

- **Tahap 8A — setup dan administrasi**: setup wizard saat instalasi pertama (PT pertama dengan bagan
  akunnya dan akun Pemilik), 2FA dengan aplikasi autentikator (wajib untuk Pemilik, Manager, dan Admin
  pelanggan), halaman Profil (ganti kata sandi, 2FA), pengelolaan pengguna dan aksesnya per PT, persona
  kustom dari katalog izin, dan penambahan PT ke grup. Jalan darurat di server:
  `lajur reset-2fa <email>` dan `lajur reset-password <email>`.

- **Tahap 8B — lisensi remote**: aktivasi di setup wizard dan check-in tiap jam ke License Console
  (perluasan console pos-app) dengan token RS256 72 jam yang diperiksa offline. Juga mencakup deteksi jam
  mundur, versi minimum, halaman Sistem (detail, cek ulang, ganti key, perpanjangan dengan unggah bukti
  bayar), dan banner lisensi.

- **Tahap 8C — backup dan restore**: backup harian terenkripsi (kunci publik di server, kunci privat
  di tangan Pemilik) berisi basis data dan lampiran, salinan ke folder lain/NAS dan S3-compatible,
  retensi, uji pulih mingguan yang memverifikasi audit trail, halaman Sistem → Backup (jalankan
  sekarang, unduh, pengaturan), banner admin, serta `lajur backup` dan `lajur restore`.

- **Tahap 8D — instalasi**: `deploy/install.sh` untuk server Ubuntu/Debian (Docker, CA lokal dan
  sertifikat HTTPS, `.env` dengan rahasia acak, kunci backup untuk Pemilik), image Docker non-root,
  HTTPS langsung dengan pengalihan dari HTTP dan unduhan `/ca.crt`. Panduan pemasang:
  [`deploy/README.md`](deploy/README.md).

- **Tahap 8E — pembaruan satu klik**: manifest rilis bertanda tangan (image per digest), container
  updater yang membuat backup, menarik image, menjalankan versi baru, memeriksa kesehatan, dan kembali
  ke versi lama bila gagal; tab Sistem → Pembaruan dengan progres langkah demi langkah.

- **Tahap 9A–9B — lampiran dan email**: lampiran PDF/gambar di jurnal, faktur, tagihan, pelunasan, dan
  kas; pengaturan SMTP dengan email uji dan antrean kirim; peringatan backup dan lisensi ke admin;
  PDF faktur berlogo dengan rekening perusahaan, dan kirim faktur ke pelanggan lewat email.

- **Tahap 9C — nota kredit, nota debit, dan retur**: nota atas faktur atau tagihan asal dengan pilihan
  baris, kuantitas, dan harga; barang retur kembali ke stok dengan HPP asal atau keluar ke pemasok;
  kelebihan atas dokumen lunas menjadi uang muka; rekap PPN, umur piutang/utang, dan kartu kontak
  ikut berkurang; nota antar-PT dicerminkan otomatis.

- **Aset tetap**: register aset per kategori, penyusutan bulanan otomatis (garis lurus atau saldo
  menurun) dengan penyusutan susulan, daftar penyusutan fiskal per kelompok harta dengan kolom koreksi,
  pelepasan aset dengan laba/rugi, dan pencocokan register dengan buku besar.

- **Revisi klien (16 Okt 2026)**: departemen per baris di transaksi kas, dan jurnal manual bisa
  diubah selama periodenya belum dikunci (nomor tetap, riwayat versi, audit trail).

- **Faktur masukan dan bukti potong (TAX-07, TAX-08)**: nomor faktur pajak pemasok, masa
  pengkreditan (paling lama tiga masa sesudah masa faktur), PPN tidak dikreditkan yang dijurnal ke
  biaya, dan pencocokan dengan daftar pajak masukan Coretax. Bukti potong PPh 23, 4(2), dan 26 per
  tagihan dengan kode objek pajak dari referensi DJP, ekspor XML BPPU (dalam negeri) dan BPNR (pemasok
  luar negeri), serta pencatatan nomor bukti potong yang terbit. Tagihan yang bukti potongnya terbit
  tidak bisa di-void. Uji dulu satu file kecil di Coretax sebelum mengekspor banyak bukti potong.

- **Tahap 9F — cabang, penomoran, dan jurnal berulang**: cabang dan departemen di faktur, tagihan,
  dan kas yang diwariskan ke jurnal; laba rugi per cabang; format nomor dokumen per seri (awalan,
  siklus, digit, nomor per cabang); jurnal berulang bulanan dengan pengejaran jadwal terlewat; dan
  pengguna yang dibatasi ke cabang tertentu.

- **Tahap 9G — uang muka dan arus kas**: uang muka pelanggan dan pemasok (terima, pakai, kembalikan),
  limit kredit dengan peringatan, proyeksi kebutuhan kas per minggu, dan pelunasan antar-PT yang
  tercermin otomatis.

- **Tahap 9E — impor dan saldo awal**: impor Excel/CSV dengan pratinjau dan galat per baris untuk
  kontak, produk, neraca saldo awal, piutang/utang/stok awal, dan jurnal; rekap penjualan harian
  retail dengan PPN digunggung, dan ekspor rekap PPN digunggung per masa.

- **Tahap 9D — rekonsiliasi bank**: centang mutasi buku terhadap rekening koran, selisih harus nol
  untuk menutup, mutasi yang belum muncul terbawa ke bulan berikutnya, rekap rekonsiliasi (CB-05)
  dan ekspor Excel.

### Rilis

```bash
make image VERSION=1.2.3 REGISTRY=ghcr.io/lajur          # image app dan updater
docker push ghcr.io/lajur/lajur:1.2.3 && docker push ghcr.io/lajur/lajur-updater:1.2.3
lajur sign-manifest --key rilis.private.pem --kid lajur-rilis-2027 --version 1.2.3 \
  --image ghcr.io/lajur/lajur@sha256:<digest> --notes-file catatan.md > stable.jws
```

Build rilis membutuhkan kunci publik produksi di `internal/platform/license/keys/release/` dan
`internal/platform/update/keys/release/`.

### Mencoba lisensi remote di laptop

1. Jalankan License Console tiruan dari repo pos-app: `npm run dev`, lalu buka
   <http://localhost:5173/owner> dan buat *Client baru* dengan aplikasi **Lajur**.
2. Jalankan Lajur dengan `LAJUR_LICENSE_MODE=remote` dan `LAJUR_LICENSE_ALLOW_HTTP=true` pada basis
   data kosong. Di setup wizard, isi alamat console `http://localhost:5173/owner/exec`, Client ID, dan
   license key.

Console tiruan menandatangani token dengan kunci pengembangan (`deploy/dev/lajur-dev.private.pem`).
Hanya build pengembangan yang mempercayai kunci itu.

## Prasyarat

- Go 1.27+
- Node.js 24+
- Docker (PostgreSQL untuk pengembangan, dan sqlc)

## Menjalankan di laptop

```bash
cp .env.example .env
make dev-db        # PostgreSQL di 127.0.0.1:5433
make web-install
make seed          # data contoh Grup Arta: 3 PT, 6 akun (satu per persona), bagan akun, jurnal
                   # 3 bulan terakhir, kontak, faktur, tagihan, pelunasan, kas, produk dan stok,
                   # serta proyek dengan RAB, uang muka, dan termin bulan ini
make serve         # build frontend + binary, lalu jalankan dengan variabel dari .env
```

Buka http://localhost:8080 dan masuk dengan salah satu akun berikut. Kata sandinya adalah
nilai `LAJUR_DEV_SEED_PASSWORD` di `.env`.

| Email | Persona |
| --- | --- |
| pemilik@arta.test | Pemilik |
| manager@arta.test | Manager |
| akuntansi@arta.test | Staf akuntansi |
| pajak@arta.test | Staf pajak |
| auditor@arta.test | Auditor |
| admin@arta.test | Admin pelanggan |

Untuk mengembangkan frontend dengan hot reload, jalankan `make run` di satu terminal dan
`make web-dev` di terminal lain, lalu buka http://localhost:5173.

## Tes

```bash
make test
```

Tes integrasi memakai `LAJUR_TEST_DATABASE_URL` dan membuat basis data sementara sendiri.
Bila variabel itu kosong, tes integrasi dilewati.

## Struktur

```text
cmd/lajur/             perintah: serve, migrate, audit-verify, seed-dev (hanya build pengembangan)
internal/platform/     db, migrasi, http, sesi dan login, peran (rbac), audit trail, lisensi,
                       money (nominal tanpa floating point), civil (tanggal tanpa jam)
internal/ledger/       mesin buku besar: posting, void, bagan akun, periode, neraca lajur
internal/modules/core/ PT, hak akses per PT, dan kontak
internal/modules/tax/  kode pajak bertanggal berlaku; perhitungan PPN dan PPh
internal/modules/tax/coretax/ faktur pajak keluaran dan masukan, bukti potong (BPPU/BPNR), ekspor XML
                       Coretax, rekap PPN dan PPh
internal/modules/trade/ perhitungan baris faktur/tagihan dan umur piutang/utang (bersama sales dan purchase)
internal/modules/sales/ faktur penjualan, pelunasan, umur piutang
internal/modules/purchase/ tagihan pemasok, pembayaran, umur utang
internal/modules/cash/ rekening kas/bank dan transaksi kas manual
internal/modules/inventory/ produk dan jasa, kartu stok, HPP rata-rata, penyesuaian dan stock opname
internal/modules/project/ proyek, RAB, laba per proyek, faktur uang muka/termin/retensi
internal/modules/report/ laporan keuangan, arus kas, neraca saldo, ringkasan, kartu kontak, dashboard
internal/modules/consol/ dokumen cerminan antar-PT, bagan akun grup, pemetaan, laporan konsolidasi
internal/platform/export/ penulis CSV dan XLSX untuk ekspor laporan
internal/server/       perangkaian route dan penyaji frontend
migrations/            SQL goose, tertanam di binary
web/                   React + Vite; hasil build disisipkan ke binary
deploy/                docker compose untuk pengembangan
```

Setiap route wajib menyatakan izin dan mode lisensinya; aplikasi menolak start bila ada yang
lupa, dan `TestEveryRouteDeclaresLicenseAndPermission` menjaganya di CI.

Aturan buku besar yang dijaga basis data, bukan hanya aplikasi: jurnal harus seimbang saat commit,
baris jurnal tidak bisa diubah atau dihapus, baris hanya boleh ke akun detail, dan saldo agregat
diisi trigger dari baris jurnal. POST jurnal, penyesuaian, dan void wajib membawa header
`Idempotency-Key`, sehingga klik ganda tidak membuat jurnal kedua.
