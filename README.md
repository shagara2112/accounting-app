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

Proyek, persediaan, laporan keuangan, dan konsolidasi dibangun di tahap berikutnya.

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
                   # 3 bulan terakhir, serta kontak, faktur, tagihan, pelunasan, dan kas bulan ini
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
internal/modules/tax/coretax/ faktur pajak keluaran, ekspor XML Coretax, rekap PPN dan PPh
internal/modules/trade/ perhitungan baris faktur/tagihan dan umur piutang/utang (bersama sales dan purchase)
internal/modules/sales/ faktur penjualan, pelunasan, umur piutang
internal/modules/purchase/ tagihan pemasok, pembayaran, umur utang
internal/modules/cash/ rekening kas/bank dan transaksi kas manual
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
