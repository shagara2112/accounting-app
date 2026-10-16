# Kunci publik lisensi produksi

Letakkan kunci publik License Console produksi di folder ini sebagai `<kid>.pem`
(format PEM "PUBLIC KEY"), mis. `lajur-20270101.pem`. Build rilis (`make build-release`)
hanya mempercayai kunci di folder ini dan menolak start bila folder ini belum berisi kunci.

Buat pasangan kunci baru dengan:

    lajur license-keygen <folder-keluaran>

Kunci privat (`<kid>.private.pem`) disalin ke Script Properties License Console
(`LAJUR_SIGNING_KEY`, dan `LAJUR_SIGNING_KID` = kid), lalu dihapus dari komputer.
Simpan dua kunci publik sekaligus (aktif dan berikutnya) supaya kunci bisa dirotasi
tanpa memutus pelanggan.
