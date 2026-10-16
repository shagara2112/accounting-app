# Kunci publik manifest rilis (produksi)

Kunci publik untuk memverifikasi manifest pembaruan, sebagai `<kid>.pem`. Kunci ini sengaja
terpisah dari kunci lisensi: kebocoran kunci lisensi tidak boleh membuat orang bisa
menyuruh server pelanggan menjalankan image lain. Buat dengan `lajur license-keygen <folder>
lajur-rilis-YYYYMMDD`; kunci privatnya hanya dipakai di mesin rilis untuk `lajur sign-manifest`.
