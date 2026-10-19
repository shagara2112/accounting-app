import type { Me } from './api'
import { isRestricted } from './dimensions'
import { can } from './session'

// Isi menu Panduan: langkah memakai Lajur per menu, ditambah pertanyaan umum. Topik yang menunya
// tidak bisa dibuka pengguna (izin atau pembatasan cabang) disembunyikan, sama seperti menu utama.
// Bila tampilan atau alur sebuah halaman berubah, perbarui topiknya di sini juga.

export type GuideSection = {
  title: string
  text?: string
  /** Langkah berurutan. */
  steps?: string[]
  /** Butir tanpa urutan. */
  points?: string[]
  note?: string
}

export type GuideTopic = {
  id: string
  group: string
  title: string
  summary: string
  /** Halaman yang dijelaskan topik ini. */
  page?: { to: string; label: string }
  /** Halaman yang tombol Panduannya membuka topik ini, mis. '/penjualan' atau '/penjualan?tab=pelunasan'. */
  paths?: string[]
  visible?: (me: Me) => boolean
  /** Kata lain yang dipakai pengguna saat mencari, mis. "inventaris" untuk persediaan. */
  keywords?: string[]
  sections: GuideSection[]
}

export type Faq = { q: string; a: string; topic?: string }

const allow =
  (perm: string, wholeCompany = false) =>
  (me: Me) =>
    can(me, perm) && !(wholeCompany && isRestricted(me))

export const guideGroups = ['Mulai', 'Transaksi', 'Pajak', 'Operasional', 'Laporan', 'Akuntansi', 'Admin'] as const

export const topics: GuideTopic[] = [
  // Mulai ------------------------------------------------------------------------------
  {
    id: 'mulai',
    group: 'Mulai',
    title: 'Memulai',
    summary: 'Masuk ke Lajur, memilih PT, mengenali menu, dan status dokumen.',
    page: { to: '/', label: 'Beranda' },
    paths: ['/'],
    keywords: ['login', 'masuk', 'ganti pt', 'peran', 'akses', 'draf', 'void', 'posting'],
    sections: [
      {
        title: 'Masuk ke Lajur',
        steps: [
          'Buka alamat Lajur di browser (Chrome, Edge, atau Firefox) dari komputer di jaringan kantor. Alamatnya diberikan oleh admin, mis. https://lajur.kantor atau alamat IP server.',
          'Isi email dan kata sandi, lalu tekan Masuk.',
          'Bila 2FA aktif, masukkan kode 6 digit dari aplikasi autentikator di ponsel Anda.',
        ],
        note: 'Bila browser memperingatkan sertifikat, minta admin memasang sertifikat kantor (berkas ca.crt) di komputer Anda. Jangan melewati peringatan itu di komputer umum.',
      },
      {
        title: 'Memilih PT',
        points: [
          'Nama PT aktif tampil di kanan atas. Semua data yang Anda lihat dan catat milik PT aktif.',
          'Bila Anda punya akses ke beberapa PT, pilih PT lain di kotak "Ganti PT". Tidak perlu masuk ulang.',
          'Peran Anda bisa berbeda di tiap PT, sehingga menu yang tampil juga bisa berbeda.',
        ],
      },
      {
        title: 'Menu dan izin',
        points: [
          'Menu di kiri hanya menampilkan halaman yang boleh Anda buka. Kartu "Yang bisa Anda lakukan di PT ini" di Beranda merangkum izin Anda.',
          'Peran bawaan: Pemilik, Manager, Staf akuntansi, Staf pajak, Auditor, dan Admin pelanggan. Admin bisa membuat persona sendiri.',
          'Bila menu yang Anda butuhkan tidak ada, minta pengelola pengguna menambah izin Anda di menu Pengguna dan akses.',
        ],
      },
      {
        title: 'Status dokumen',
        points: [
          'Draf: belum bernomor dan belum masuk buku besar. Masih bisa diubah atau dihapus.',
          'Diposting: sudah bernomor dan jurnalnya masuk buku besar. Tidak bisa diubah.',
          'Void: dibatalkan dengan jurnal pembalik bertanggal void. Dokumen dan nomornya tetap tercatat untuk jejak audit.',
        ],
        note: 'Tombol Panduan di kanan atas setiap halaman membuka panduan untuk halaman itu.',
      },
    ],
  },
  {
    id: 'profil',
    group: 'Mulai',
    title: 'Kata sandi dan 2FA',
    summary: 'Mengganti kata sandi, memasang 2FA, dan bila lupa kata sandi atau ganti ponsel.',
    page: { to: '/profil', label: 'Profil' },
    paths: ['/profil'],
    keywords: ['password', 'lupa', 'otp', 'authenticator', 'keamanan'],
    sections: [
      {
        title: 'Mengganti kata sandi',
        steps: ['Klik nama Anda di kiri bawah untuk membuka Profil.', 'Isi Kata sandi sekarang, lalu kata sandi baru dua kali (minimal 10 karakter), dan tekan Ganti kata sandi.'],
        note: 'Mengganti kata sandi mengakhiri sesi Anda di perangkat lain.',
      },
      {
        title: 'Memasang 2FA',
        steps: [
          'Pasang aplikasi autentikator di ponsel, mis. Google Authenticator atau Microsoft Authenticator.',
          'Di Profil, tekan Pasang 2FA lalu pindai kode QR dengan aplikasi itu.',
          'Masukkan kode 6 digit yang muncul di aplikasi untuk menyelesaikan.',
        ],
        note: '2FA wajib untuk Pemilik, Manager, dan Admin pelanggan.',
      },
      {
        title: 'Lupa kata sandi atau ganti ponsel',
        points: [
          'Minta admin membuka Pengguna dan akses, memilih nama Anda, lalu Atur ulang kata sandi atau Atur ulang 2FA.',
          'Akun yang terkunci karena salah kata sandi berkali-kali dibuka admin dengan Buka kunci akun.',
        ],
      },
    ],
  },

  // Transaksi --------------------------------------------------------------------------
  {
    id: 'faktur',
    group: 'Transaksi',
    title: 'Faktur penjualan',
    summary: 'Membuat, memposting, mengirim, dan membatalkan faktur ke pelanggan.',
    page: { to: '/penjualan', label: 'Penjualan' },
    paths: ['/penjualan', '/penjualan/baru'],
    visible: allow('sales.view'),
    keywords: ['invoice', 'tagih pelanggan', 'penjualan', 'pdf', 'email', 'batal', 'salah input'],
    sections: [
      {
        title: 'Membuat faktur',
        steps: [
          'Buka Penjualan, tekan Faktur baru.',
          'Pilih pelanggan dan tanggal. Jatuh tempo terisi dari termin pelanggan dan bisa diubah.',
          'Isi baris: pilih produk (harga dan akun terisi otomatis) atau pilih akun pendapatan, lalu uraian, qty, harga, diskon %, dan kode pajak (PPN atau tanpa PPN).',
          'Bila perlu, isi proyek, cabang, departemen, dan catatan.',
          'Tekan Simpan draf untuk menyimpan dulu, atau Simpan & posting untuk langsung membukukan.',
        ],
        note: 'DPP, DPP nilai lain (11/12), dan PPN dihitung otomatis sesuai kode pajak pada tanggal faktur.',
      },
      {
        title: 'Setelah diposting',
        points: [
          'Faktur mendapat nomor, mis. FJ/2026/10/0001, dan jurnal piutang, pendapatan, serta PPN keluaran dibuat otomatis.',
          'Baris barang mengurangi stok dan membukukan HPP.',
          'Faktur ber-PPN muncul di Pajak → Faktur keluaran untuk diekspor ke Coretax.',
        ],
      },
      {
        title: 'Mengirim ke pelanggan',
        steps: [
          'Klik faktur di daftar untuk membukanya.',
          'Tekan PDF untuk melihat, mengunduh, atau mencetak faktur (untuk draf: Pratinjau PDF).',
          'Tekan Kirim email untuk mengirimkannya ke pelanggan. PDF faktur dilampirkan otomatis.',
        ],
        note: 'Kirim email butuh pengaturan email di Sistem dan lisensi → Email. Logo faktur diatur di Pajak → Pengaturan.',
      },
      {
        title: 'Mengubah atau membatalkan',
        points: [
          'Draf: buka faktur lalu Ubah, atau Hapus draf. Draf juga bisa langsung diposting dari sini dengan tombol Posting.',
          'Sudah diposting: tidak bisa diubah. Tekan Void…, isi tanggal dan alasan, lalu buat faktur baru yang benar.',
          'Koreksi sebagian (retur atau potongan harga): pakai nota kredit, bukan void.',
          'Faktur yang sudah punya pelunasan atau nota kredit harus di-void pelunasan atau notanya dulu.',
          'Faktur yang faktur pajaknya sudah terbit di Coretax harus ditandai dibatalkan dulu di Pajak → Faktur keluaran.',
        ],
      },
      {
        title: 'Daftar faktur',
        points: [
          'Saring per bulan dan status: Draf, Belum lunas, Lunas, Void. Cari dengan nomor atau catatan.',
          'Tab Umur piutang menampilkan piutang yang belum lunas per tanggal, dikelompokkan menurut lama tunggakan.',
          'Pelanggan dengan limit kredit memberi peringatan bila faktur baru membuat piutangnya melewati limit.',
        ],
      },
    ],
  },
  {
    id: 'pelunasan',
    group: 'Transaksi',
    title: 'Pelunasan piutang',
    summary: 'Mencatat pembayaran dari pelanggan, termasuk sebagian, banyak faktur, PPh dipotong, dan uang muka.',
    page: { to: '/penjualan?tab=pelunasan', label: 'Penjualan → Pelunasan' },
    paths: ['/penjualan?tab=pelunasan', '/penjualan/pelunasan', '/penjualan?tab=umur'],
    visible: allow('sales.view'),
    keywords: ['pembayaran pelanggan', 'terima uang', 'piutang', 'cicilan', 'dp', 'uang muka', 'pph 23'],
    sections: [
      {
        title: 'Mencatat pelunasan',
        steps: [
          'Buka Penjualan, tekan Terima pembayaran.',
          'Pilih pelanggan. Faktur yang belum lunas akan muncul.',
          'Isi tanggal dan rekening tujuan (Masuk ke rekening).',
          'Isi jumlah yang dilunasi di setiap faktur. Boleh sebagian, dan satu pembayaran boleh untuk banyak faktur.',
          'Bila pelanggan memotong PPh 23 atau PPh final 4(2), pilih kode PPh dan isi nominalnya di faktur itu.',
          'Periksa ringkasan Piutang dilunasi dan Uang diterima, lalu tekan Posting pelunasan.',
        ],
        note: 'Faktur dengan PPh dipotong tetap lunas penuh. Potongan PPh 23 dicatat sebagai PPh dibayar di muka, PPh final 4(2) sebagai beban PPh final; minta bukti potongnya dari pelanggan.',
      },
      {
        title: 'Uang muka pelanggan',
        points: [
          'Uang yang diterima melebihi faktur yang dilunasi, atau diterima sebelum ada faktur, dicatat sebagai uang muka pelanggan.',
          'Uang muka dipakai untuk melunasi faktur berikutnya di form pelunasan.',
          'Bila uang muka dikembalikan ke pelanggan, pakai Kembalikan uang muka pelanggan di form pelunasan.',
        ],
      },
      {
        title: 'Penting',
        points: [
          'Pembayaran pelanggan dicatat di sini, bukan di Kas dan bank, supaya piutang ikut berkurang. Saldo rekening tetap bertambah karena pelunasan memposting jurnal ke rekening itu.',
          'Pelunasan yang salah dibatalkan dengan Void, lalu dicatat ulang.',
        ],
      },
    ],
  },
  {
    id: 'retur-penjualan',
    group: 'Transaksi',
    title: 'Retur dan nota kredit',
    summary: 'Mengurangi faktur yang sudah diposting karena barang kembali atau potongan harga.',
    page: { to: '/penjualan?tab=retur', label: 'Penjualan → Retur & nota kredit' },
    paths: ['/penjualan?tab=retur', '/penjualan/retur'],
    visible: allow('sales.view'),
    keywords: ['retur', 'potongan harga', 'diskon', 'credit note', 'barang kembali'],
    sections: [
      {
        title: 'Membuat nota kredit',
        steps: [
          'Buka faktur asal yang sudah diposting.',
          'Tekan Nota kredit / retur.',
          'Pilih baris yang diretur, isi qty dan harganya (untuk potongan harga, ubah harganya), tanggal nota, dan alasan.',
          'Tekan Posting.',
        ],
      },
      {
        title: 'Akibatnya',
        points: [
          'Sisa piutang faktur berkurang. Bila faktur sudah lunas, kelebihannya menjadi uang muka pelanggan.',
          'Barang retur masuk kembali ke stok dengan HPP asal.',
          'PPN keluaran berkurang di masa nota.',
        ],
      },
    ],
  },
  {
    id: 'penjualan-harian',
    group: 'Transaksi',
    title: 'Penjualan harian (retail)',
    summary: 'Membukukan rekap kasir per hari dan per toko tanpa faktur per pembeli.',
    page: { to: '/penjualan?tab=harian', label: 'Penjualan → Penjualan harian' },
    paths: ['/penjualan?tab=harian'],
    visible: allow('sales.view'),
    keywords: ['kasir', 'pos', 'toko', 'eceran', 'retail'],
    sections: [
      {
        title: 'Mengimpor rekap',
        steps: [
          'Buka Penjualan → tab Penjualan harian, tekan Impor rekap harian.',
          'Unduh template, isi rekap per tanggal dan toko (harga sudah termasuk PPN), lalu unggah.',
          'Tekan Periksa berkas, perbaiki baris yang ditolak, lalu Impor.',
        ],
        note: 'Satu rekap per tanggal dan toko. Untuk mengganti rekap, void rekap lama dulu. PPN-nya masuk Pajak → PPN digunggung.',
      },
    ],
  },
  {
    id: 'tagihan',
    group: 'Transaksi',
    title: 'Tagihan pembelian',
    summary: 'Mencatat tagihan pemasok, PPN masukan, dan PPh yang dipotong.',
    page: { to: '/pembelian', label: 'Pembelian' },
    paths: ['/pembelian', '/pembelian/baru'],
    visible: allow('purchase.view'),
    keywords: ['bill', 'pembelian', 'beli', 'biaya', 'pph 23', 'pph 4(2)', 'pph 26', 'nota'],
    sections: [
      {
        title: 'Membuat tagihan',
        steps: [
          'Buka Pembelian, tekan Tagihan baru.',
          'Pilih pemasok, tanggal, dan isi No. faktur pemasok.',
          'Isi baris: pilih produk, atau pilih akun beban, aset, atau persediaan. Isi qty, harga, diskon, dan kode pajak.',
          'Bila Anda memotong PPh, pilih di PPh dipotong: PPh 23 atau 4(2) untuk pemasok dalam negeri, PPh 26 untuk pemasok luar negeri. PPh dihitung dari DPP.',
          'Periksa Dibayar ke pemasok (total dikurangi PPh), lalu Simpan draf atau Simpan & posting.',
        ],
      },
      {
        title: 'Yang terjadi setelah diposting',
        points: [
          'Jurnal beban atau aset, PPN masukan, utang usaha, dan utang PPh dibuat otomatis.',
          'Barang menambah stok dan memperbarui HPP rata-rata.',
          'PPN masukan muncul di Pajak → Faktur masukan, dan PPh yang dipotong di Pajak → Bukti potong.',
        ],
      },
      {
        title: 'Tips',
        points: [
          'Lampirkan foto atau PDF nota di bagian Lampiran tagihan.',
          'Lajur memberi peringatan bila nomor faktur pemasok yang sama sudah pernah dicatat.',
          'Pembelian aset tetap (kendaraan, peralatan) dicatat ke akun aset, lalu didaftarkan di menu Aset tetap.',
          'Mengubah dan membatalkan sama seperti faktur penjualan: draf bisa diubah, yang diposting di-void atau dikoreksi dengan nota debit.',
        ],
      },
    ],
  },
  {
    id: 'pembayaran',
    group: 'Transaksi',
    title: 'Pembayaran ke pemasok',
    summary: 'Membayar tagihan, sebagian atau banyak sekaligus, dan memantau umur utang.',
    page: { to: '/pembelian?tab=pembayaran', label: 'Pembelian → Pembayaran' },
    paths: ['/pembelian?tab=pembayaran', '/pembelian/pembayaran', '/pembelian?tab=umur'],
    visible: allow('purchase.view'),
    keywords: ['bayar', 'hutang', 'utang', 'transfer ke pemasok', 'jatuh tempo'],
    sections: [
      {
        title: 'Mencatat pembayaran',
        steps: [
          'Buka Pembelian, tekan Bayar tagihan.',
          'Pilih pemasok. Tagihan yang belum lunas akan muncul.',
          'Isi tanggal dan Dibayar dari rekening.',
          'Isi jumlah yang dibayar di setiap tagihan, lalu tekan Posting pembayaran.',
        ],
        note: 'Uang muka ke pemasok dan pengembaliannya bekerja seperti uang muka pelanggan.',
      },
      {
        title: 'Umur utang dan proyeksi kas',
        points: [
          'Tab Umur utang menampilkan utang yang belum lunas per tanggal, menurut lama jatuh tempo.',
          'Di bawahnya, Proyeksi kebutuhan kas menunjukkan saldo kas ditambah piutang dan dikurangi utang yang jatuh tempo per minggu.',
        ],
      },
    ],
  },
  {
    id: 'retur-pembelian',
    group: 'Transaksi',
    title: 'Retur dan nota debit',
    summary: 'Mengurangi tagihan yang sudah diposting karena barang dikembalikan atau potongan harga.',
    page: { to: '/pembelian?tab=retur', label: 'Pembelian → Retur & nota debit' },
    paths: ['/pembelian?tab=retur', '/pembelian/retur'],
    visible: allow('purchase.view'),
    keywords: ['retur', 'debit note', 'kembalikan barang'],
    sections: [
      {
        title: 'Membuat nota debit',
        steps: ['Buka tagihan asal yang sudah diposting.', 'Tekan Nota debit / retur.', 'Pilih baris, isi qty dan harga, tanggal nota, dan alasan, lalu Posting.'],
      },
      {
        title: 'Akibatnya',
        points: [
          'Sisa utang tagihan berkurang. Bila tagihan sudah dibayar, kelebihannya menjadi uang muka ke pemasok.',
          'Barang retur keluar dari stok, dan PPN masukan berkurang di masa nota.',
          'PPh tagihan ikut berkurang, sehingga nilai bukti potongnya juga berkurang.',
        ],
        note: 'Tagihan yang PPN masukannya sudah ditandai tidak dikreditkan harus dikreditkan lagi dulu sebelum diberi nota debit.',
      },
    ],
  },
  {
    id: 'kas',
    group: 'Transaksi',
    title: 'Kas dan bank',
    summary: 'Mencatat penerimaan, pengeluaran, dan transfer di luar penjualan dan pembelian.',
    page: { to: '/kas', label: 'Kas dan bank' },
    paths: ['/kas'],
    visible: allow('cash.view'),
    keywords: ['bank', 'kas kecil', 'petty cash', 'biaya', 'pengeluaran', 'penerimaan', 'transfer', 'departemen'],
    sections: [
      {
        title: 'Mencatat transaksi',
        steps: [
          'Buka Kas dan bank, lalu tekan Penerimaan, Pengeluaran, atau Transfer.',
          'Pilih rekening kas atau bank dan tanggal.',
          'Isi baris: akun lawan (mis. beban listrik atau pendapatan bunga), departemen bila perlu, jumlah, dan keterangan.',
          'Tekan Simpan & posting. Bila belum yakin akunnya, tekan Simpan draf.',
        ],
      },
      {
        title: 'Draf tanpa akun',
        points: [
          'Staf boleh menyimpan draf tanpa memilih akun lawan. Draf belum bernomor dan belum masuk buku besar.',
          'Akuntan membuka draf, melengkapi akunnya, lalu memposting. Jumlah draf yang menunggu tampil di halaman Kas dan bank dan di Beranda.',
        ],
      },
      {
        title: 'Yang tidak dicatat di sini',
        points: [
          'Pembayaran dari pelanggan: catat di Penjualan → Terima pembayaran.',
          'Pembayaran ke pemasok: catat di Pembelian → Bayar tagihan.',
          'Kalau dicatat di Kas dan bank, piutang dan utangnya tidak berkurang.',
        ],
        note: 'Saldo rekening di halaman ini adalah saldo akun buku besarnya, jadi pelunasan dan pembayaran dari menu Penjualan dan Pembelian ikut terhitung.',
      },
    ],
  },
  {
    id: 'rekonsiliasi',
    group: 'Transaksi',
    title: 'Rekonsiliasi bank',
    summary: 'Mencocokkan mutasi di buku dengan rekening koran setiap bulan.',
    page: { to: '/kas/rekonsiliasi', label: 'Rekonsiliasi bank' },
    paths: ['/kas/rekonsiliasi'],
    visible: allow('cash.view', true),
    keywords: ['rekening koran', 'mutasi', 'cocokkan bank', 'selisih bank'],
    sections: [
      {
        title: 'Langkah',
        steps: [
          'Catat dulu biaya admin, bunga, dan pajak bunga dari rekening koran di Kas dan bank.',
          'Buka Kas dan bank → Rekonsiliasi bank, lalu pilih rekeningnya.',
          'Tekan Rekonsiliasi baru, isi Tanggal rekening koran dan Saldo akhir rekening koran.',
          'Centang setiap mutasi yang juga ada di rekening koran.',
          'Bila Selisih sudah nol (Cocok), tekan Tutup rekonsiliasi.',
        ],
        note: 'Mutasi yang belum muncul di rekening koran terbawa ke rekonsiliasi berikutnya. Bila ada selisih, periksa transaksi yang belum dicatat atau salah nominal.',
      },
    ],
  },
  {
    id: 'kontak',
    group: 'Transaksi',
    title: 'Kontak pelanggan dan pemasok',
    summary: 'Data pelanggan dan pemasok: NPWP, PKP, termin, limit kredit, dan pemasok luar negeri.',
    page: { to: '/kontak', label: 'Kontak' },
    paths: ['/kontak'],
    visible: allow('core.contact.view'),
    keywords: ['pelanggan', 'pemasok', 'supplier', 'customer', 'npwp', 'nik', 'luar negeri'],
    sections: [
      {
        title: 'Menambah kontak',
        steps: [
          'Buka Kontak, tekan Kontak baru.',
          'Isi nama, centang Pelanggan dan/atau Pemasok, dan Pengusaha Kena Pajak (PKP) bila berlaku.',
          'Untuk kontak dalam negeri, isi NPWP 16 digit (atau NIK bila tidak punya NPWP) dan NITKU. Data ini dipakai faktur pajak dan bukti potong.',
          'Untuk pemasok luar negeri, pilih Negara lalu isi TIN di negara asal dan alamatnya. Pemasok ini dipotong PPh 26.',
          'Isi termin pembayaran, email penagihan, telepon, alamat, rekening bank, dan limit kredit bila perlu, lalu simpan.',
        ],
      },
      {
        title: 'Tips',
        points: [
          'Banyak kontak sekaligus bisa dimasukkan lewat Impor data → Kontak.',
          'Kontak yang mewakili PT lain dalam grup bisa ditautkan, sehingga faktur ke kontak itu otomatis membuat tagihan di PT lawan.',
        ],
      },
    ],
  },

  // Pajak ------------------------------------------------------------------------------
  {
    id: 'faktur-keluaran',
    group: 'Pajak',
    title: 'Faktur pajak keluaran',
    summary: 'Mengekspor faktur ber-PPN ke XML Coretax dan mencatat nomor faktur pajak yang terbit.',
    page: { to: '/pajak', label: 'Pajak → Faktur keluaran' },
    paths: ['/pajak', '/pajak?tab=faktur'],
    visible: allow('tax.view', true),
    keywords: ['coretax', 'xml', 'efaktur', 'e-faktur', 'nsfp', 'ppn keluaran'],
    sections: [
      {
        title: 'Sebelum mulai',
        points: [
          'Lengkapi profil pajak PT (NPWP, NITKU, alamat, PKP) di Pajak → Pengaturan.',
          'Pastikan pelanggan punya NPWP atau NIK 16 digit di menu Kontak.',
        ],
      },
      {
        title: 'Mengekspor ke Coretax',
        steps: [
          'Buka Pajak → Faktur keluaran, pilih masa pajak.',
          'Periksa faktur bertanda merah (mis. NPWP pembeli kosong) dan perbaiki dulu.',
          'Bila perlu, centang faktur lalu ubah kode transaksi. Kode 04 dipakai untuk DPP nilai lain.',
          'Centang faktur yang siap, tekan Ekspor XML.',
          'Unggah berkas XML itu di Coretax lalu terbitkan faktur pajaknya di Coretax.',
        ],
        note: 'Untuk pertama kali, uji dengan satu faktur dulu sebelum mengekspor banyak faktur.',
      },
      {
        title: 'Mencatat nomor yang terbit',
        points: [
          'Satu per satu: klik faktur → Catat nomor terbit, isi nomor faktur pajak dan tanggalnya.',
          'Sekaligus: Impor nomor faktur, lalu pilih atau tempel CSV daftar faktur keluaran unduhan Coretax.',
        ],
      },
      {
        title: 'Membatalkan',
        steps: [
          'Batalkan faktur pajaknya di Coretax.',
          'Di Lajur, klik faktur → Tandai dibatalkan, isi alasannya.',
          'Setelah itu faktur penjualannya boleh di-void.',
        ],
        note: 'Bila XML sudah diekspor tetapi belum diterbitkan, pakai Batalkan ekspor untuk mengembalikannya ke status siap ekspor.',
      },
    ],
  },
  {
    id: 'faktur-masukan',
    group: 'Pajak',
    title: 'Faktur pajak masukan',
    summary: 'Mencatat nomor faktur pemasok, masa pengkreditan, PPN tidak dikreditkan, dan cocok dengan Coretax.',
    page: { to: '/pajak?tab=masukan', label: 'Pajak → Faktur masukan' },
    paths: ['/pajak?tab=masukan'],
    visible: allow('tax.view', true),
    keywords: ['ppn masukan', 'kredit pajak', 'coretax', 'pengkreditan'],
    sections: [
      {
        title: 'Cara kerjanya',
        text: 'Setiap tagihan ber-PPN yang diposting otomatis dikreditkan pada masa tagihannya. Halaman ini untuk melengkapi dan menyesuaikannya.',
      },
      {
        title: 'Mencatat nomor faktur pajak pemasok',
        steps: [
          'Buka Pajak → Faktur masukan, pilih masa.',
          'Klik tagihan, isi Nomor faktur pajak pemasok dan Tanggal faktur.',
          'Bila faktur diterima terlambat, pilih Masa pengkreditan yang lain (paling lama tiga masa sesudah masa faktur).',
          'Tekan Simpan.',
        ],
      },
      {
        title: 'PPN tidak dikreditkan',
        steps: [
          'Klik tagihan, pilih tab Tidak dikreditkan.',
          'Isi tanggal jurnal dan alasan, mis. faktur pajak pemasok cacat.',
          'Tekan Tandai tidak dikreditkan. PPN-nya dijurnal ke akun biaya tagihan dan keluar dari rekap PPN.',
        ],
        note: 'Bila ternyata bisa dikreditkan, buka lagi lalu Kreditkan lagi. Jurnalnya otomatis di-void.',
      },
      {
        title: 'Mencocokkan dengan Coretax',
        steps: [
          'Unduh daftar faktur pajak masukan dari Coretax (Excel atau CSV).',
          'Tekan Cocokkan dengan Coretax, pilih berkasnya, lalu Cocokkan.',
          'Lajur mencatat nomor faktur yang cocok dan menampilkan selisih PPN, faktur di Coretax yang belum ada tagihannya, dan tagihan yang belum ada fakturnya di Coretax.',
        ],
      },
    ],
  },
  {
    id: 'bukti-potong',
    group: 'Pajak',
    title: 'Bukti potong PPh 23, 4(2), dan 26',
    summary: 'Melengkapi kode objek pajak, mengekspor XML BPPU/BPNR, dan mencatat nomor bukti potong.',
    page: { to: '/pajak?tab=bupot', label: 'Pajak → Bukti potong' },
    paths: ['/pajak?tab=bupot'],
    visible: allow('tax.view', true),
    keywords: ['bupot', 'bppu', 'bpnr', 'pph 23', 'pph 26', 'pph 4(2)', 'kop', 'coretax'],
    sections: [
      {
        title: 'Cara kerjanya',
        text: 'Setiap tagihan yang memotong PPh otomatis menjadi satu bukti potong, dengan nilai bersih sesudah nota debit. Pemasok dalam negeri diekspor sebagai BPPU, pemasok luar negeri (PPh 26) sebagai BPNR.',
      },
      {
        title: 'Melengkapi dan mengekspor',
        steps: [
          'Buka Pajak → Bukti potong, pilih masa.',
          'Pasang kode objek pajak: centang bukti potong, pilih kodenya di kotak Kode objek pajak, lalu Terapkan. Bisa juga per bukti potong lewat dialognya.',
          'Bila perlu, klik bukti potong untuk mengubah fasilitas, dokumen referensi, dan tanggal pemotongan.',
          'Centang bukti potong yang tidak bermasalah, tekan Ekspor XML.',
          'Unggah XML di Coretax dan terbitkan bukti potongnya.',
        ],
        note: 'Kode objek pajak bawaan per jenis PPh diatur di Pajak → Pengaturan, sehingga tagihan berikutnya terisi otomatis.',
      },
      {
        title: 'Mencatat nomor yang terbit',
        points: [
          'Satu per satu: klik bukti potong → Catat nomor terbit.',
          'Sekaligus: Impor nomor bukti potong, lalu unggah daftar bukti potong dari Coretax. Dicocokkan lewat nomor dokumen referensi.',
        ],
      },
      {
        title: 'Membatalkan',
        points: [
          'Belum terbit: klik bukti potong → Batalkan ekspor.',
          'Sudah terbit: batalkan di Coretax, lalu di Lajur → Tandai dibatalkan. Setelah itu tagihannya boleh di-void.',
        ],
      },
    ],
  },
  {
    id: 'rekap-pajak',
    group: 'Pajak',
    title: 'Rekap PPN, PPh, dan PPN digunggung',
    summary: 'Angka untuk SPT Masa PPN dan PPh Unifikasi, serta pengaturan pajak PT.',
    page: { to: '/pajak?tab=ppn', label: 'Pajak → Rekap PPN' },
    paths: ['/pajak?tab=ppn', '/pajak?tab=pph', '/pajak?tab=digunggung', '/pajak?tab=pengaturan'],
    visible: allow('tax.view', true),
    keywords: ['spt', 'spt masa', 'kurang bayar', 'lebih bayar', 'setor pajak', 'digunggung', 'npwp pt'],
    sections: [
      {
        title: 'Rekap PPN',
        points: [
          'Menampilkan PPN keluaran, PPN masukan per masa pengkreditan, kompensasi, kurang bayar, dan lebih bayar per bulan.',
          'Lebih bayar otomatis dikompensasikan ke masa berikutnya. Kurang bayar disetor paling lambat akhir bulan berikutnya.',
        ],
      },
      {
        title: 'PPh dipotong',
        points: [
          'PPh yang kita potong dari pemasok: disetor dan dilaporkan di SPT Masa PPh Unifikasi.',
          'PPh yang dipotong pelanggan: kredit pajak (PPh 23) atau beban final (4(2)). Minta bukti potongnya dari pelanggan.',
        ],
      },
      {
        title: 'PPN digunggung',
        text: 'Untuk PT retail: rekap penjualan eceran per masa dari penjualan harian, bisa diekspor ke Excel atau CSV.',
      },
      {
        title: 'Pengaturan',
        points: [
          'Profil pajak PT: NPWP, NITKU, alamat, dan status PKP.',
          'Logo yang tampil di PDF faktur.',
          'Isian XML Coretax (jenis penyerahan, kode barang/jasa, kode satuan) dan kode objek pajak bawaan untuk bukti potong.',
        ],
      },
    ],
  },

  // Operasional ------------------------------------------------------------------------
  {
    id: 'proyek',
    group: 'Operasional',
    title: 'Proyek',
    summary: 'Laba per proyek, RAB, dan faktur uang muka, termin, serta retensi.',
    page: { to: '/proyek', label: 'Proyek' },
    paths: ['/proyek'],
    visible: allow('project.view'),
    keywords: ['rab', 'termin', 'retensi', 'konstruksi', 'uang muka', 'laba proyek'],
    sections: [
      {
        title: 'Membuat proyek',
        steps: [
          'Buka Proyek, tekan Proyek baru.',
          'Isi kode, nama proyek, pelanggan, nilai kontrak (sebelum PPN), tanggal mulai dan target selesai. Untuk konstruksi, isi juga uang muka %, retensi %, dan PPh yang dipotong pemberi kerja.',
          'Buka proyek lalu Ubah RAB untuk mengisi anggaran per akun biaya.',
        ],
      },
      {
        title: 'Mencatat biaya dan pendapatan proyek',
        points: [
          'Pilih proyek di tagihan, transaksi kas, jurnal, pemakaian material, atau faktur. Baris itu otomatis masuk laba proyek.',
          'Halaman proyek menampilkan pendapatan, biaya, margin, dan RAB vs aktual. Angkanya selalu sama dengan buku besar.',
        ],
      },
      {
        title: 'Faktur konstruksi',
        steps: [
          'Di halaman proyek, tekan Faktur uang muka untuk menagih uang muka.',
          'Tekan Faktur termin dan isi progres kumulatif. Termin = progres × nilai kontrak − termin sebelumnya; uang muka dipotong proporsional dan retensi ditahan.',
          'Saat masa pemeliharaan selesai, tekan Lepas retensi.',
          'Lajur membuat draf faktur. Periksa lalu posting dari menu Penjualan.',
        ],
      },
    ],
  },
  {
    id: 'persediaan',
    group: 'Operasional',
    title: 'Produk dan persediaan',
    summary: 'Master barang dan jasa, posisi stok, kartu stok, penyesuaian, pemakaian, dan stock opname.',
    page: { to: '/persediaan', label: 'Persediaan' },
    paths: ['/persediaan', '/produk'],
    visible: allow('inventory.view'),
    keywords: ['inventaris', 'inventory', 'stok', 'gudang', 'barang', 'hpp', 'opname', 'kartu stok', 'produk'],
    sections: [
      {
        title: 'Produk dan jasa',
        steps: [
          'Buka Produk dan jasa, tekan Produk baru.',
          'Pilih Barang (dilacak stoknya) atau Jasa, isi kode, nama, satuan, harga jual, dan akun bawaannya.',
          'Pilih produk di faktur atau tagihan supaya harga, akun, dan stok terisi otomatis.',
        ],
      },
      {
        title: 'Stok dan HPP',
        points: [
          'Tagihan menambah stok, faktur mengurangi stok dan membukukan HPP rata-rata tertimbang otomatis.',
          'Persediaan → Posisi stok menampilkan qty dan nilai per tanggal. Klik produk untuk melihat kartu stoknya.',
          'Kartu "Cocok dengan buku besar?" membandingkan nilai kartu stok dengan saldo akun persediaan.',
        ],
      },
      {
        title: 'Penyesuaian, pemakaian, dan stock opname',
        points: [
          'Penyesuaian: koreksi qty atau nilai stok, mis. barang rusak.',
          'Pemakaian: barang dipakai sendiri atau untuk proyek, dibebankan ke akun biaya.',
          'Stock opname: tekan Isi semua barang, isi qty hasil hitung fisik, lalu Simpan & posting. Selisihnya dijurnal otomatis.',
        ],
        note: 'Jangan mengubah akun persediaan lewat jurnal manual, supaya nilainya tetap sama dengan kartu stok.',
      },
    ],
  },
  {
    id: 'aset',
    group: 'Operasional',
    title: 'Aset tetap',
    summary: 'Register aset, penyusutan bulanan otomatis, penyusutan fiskal, dan pelepasan aset.',
    page: { to: '/aset', label: 'Aset tetap' },
    paths: ['/aset'],
    visible: allow('asset.view', true),
    keywords: ['inventaris kantor', 'inventaris', 'kendaraan', 'peralatan', 'mesin', 'gedung', 'penyusutan', 'depresiasi'],
    sections: [
      {
        title: 'Persiapan',
        steps: ['Buka Aset tetap → tab Kategori.', 'Tekan Buat kategori bawaan, atau Kategori baru untuk kategori sendiri. Periksa umur dan kelompok fiskalnya.'],
      },
      {
        title: 'Mendaftarkan aset',
        steps: [
          'Catat dulu pembeliannya lewat tagihan, kas, atau jurnal ke akun aset.',
          'Tekan Aset baru, pilih kategori, isi nama, tanggal perolehan, harga perolehan, lokasi, dan penanggung jawab.',
          'Untuk aset lama, isi akumulasi penyusutan awal sampai bulan tertentu.',
        ],
        note: 'Kartu pencocokan di halaman Aset tetap membandingkan total register dengan saldo akun aset dan akumulasi penyusutan.',
      },
      {
        title: 'Penyusutan',
        points: [
          'Penyusutan bulan lalu diposting otomatis mulai tanggal 1. Bisa juga diposting lebih awal di tab Penyusutan.',
          'Bulan yang terlewat ikut diposting di jurnal berikutnya.',
          'Tab Penyusutan fiskal menampilkan penyusutan menurut pajak dan koreksinya untuk SPT Tahunan.',
        ],
      },
      {
        title: 'Menjual atau menghapus aset',
        steps: ['Klik aset, tekan Lepas aset….', 'Pilih Dijual, Lewat faktur (bila terutang PPN), atau Dihapus. Isi tanggal dan nilainya.', 'Tekan Posting pelepasan. Laba atau ruginya dijurnal otomatis.'],
      },
    ],
  },

  // Laporan ----------------------------------------------------------------------------
  {
    id: 'laporan',
    group: 'Laporan',
    title: 'Laporan keuangan',
    summary: 'Neraca, laba rugi, arus kas, neraca saldo, ringkasan penjualan/pembelian, dan kartu kontak.',
    page: { to: '/laporan', label: 'Laporan keuangan' },
    paths: ['/laporan'],
    visible: allow('report.view'),
    keywords: ['neraca', 'laba rugi', 'arus kas', 'excel', 'pdf', 'cetak'],
    sections: [
      {
        title: 'Membuka laporan',
        steps: [
          'Buka Laporan keuangan, pilih tab: Neraca, Laba rugi, Arus kas, Neraca saldo, Penjualan dan pembelian, atau Kartu kontak.',
          'Pilih periode: Bulan ini, Bulan lalu, atau Pilih tanggal.',
          'Laba rugi bisa dipilah per cabang dan departemen.',
        ],
      },
      {
        title: 'Ekspor dan cetak',
        points: [
          'Tekan Excel atau CSV untuk mengunduh. Ekspor tetap bisa walau lisensi berakhir.',
          'Tekan Cetak / PDF, lalu pilih "Simpan sebagai PDF" di dialog cetak browser.',
        ],
      },
      {
        title: 'Angka tidak sesuai?',
        points: [
          'Pastikan semua draf sudah diposting. Draf tidak masuk laporan.',
          'Periksa periode yang dipilih dan PT aktif.',
          'Telusuri akunnya di Buku besar; klik nomor jurnal untuk melihat dokumen sumbernya.',
        ],
      },
    ],
  },
  {
    id: 'konsolidasi',
    group: 'Laporan',
    title: 'Konsolidasi grup',
    summary: 'Laporan gabungan semua PT dengan eliminasi transaksi antar-PT.',
    page: { to: '/konsolidasi', label: 'Konsolidasi grup' },
    paths: ['/konsolidasi'],
    visible: allow('consol.view', true),
    keywords: ['grup', 'gabungan', 'eliminasi', 'antar pt'],
    sections: [
      {
        title: 'Langkah',
        steps: [
          'Buka Konsolidasi grup → tab Pemetaan akun grup. Akun berkode sama otomatis digabung; petakan ulang bila isinya berbeda.',
          'Buka tab Laporan konsolidasi, centang PT yang digabung, lalu isi Neraca per dan Laba rugi sejak.',
          'Periksa daftar selisih antar-PT yang tidak berpasangan dan bereskan di PT masing-masing.',
        ],
        note: 'Eliminasi hanya ada di laporan ini; buku setiap PT tetap bersih. Konsolidasi saat ini untuk kepemilikan 100% dan rupiah.',
      },
    ],
  },

  // Akuntansi --------------------------------------------------------------------------
  {
    id: 'jurnal',
    group: 'Akuntansi',
    title: 'Jurnal umum dan penyesuaian',
    summary: 'Mencatat transaksi di luar modul, mengubah jurnal, void, dan jurnal berulang.',
    page: { to: '/jurnal', label: 'Jurnal' },
    paths: ['/jurnal'],
    visible: allow('ledger.view', true),
    keywords: ['jurnal umum', 'koreksi', 'akrual', 'jurnal balik', 'berulang', 'edit jurnal'],
    sections: [
      {
        title: 'Membuat jurnal umum',
        steps: [
          'Buka Jurnal, tekan Buat jurnal umum.',
          'Isi tanggal dan keterangan.',
          'Isi baris akun dengan debit atau kredit. Proyek, cabang, dan departemen bisa diisi per baris.',
          'Total debit harus sama dengan kredit. Tombol Isi selisih membantu mengisi baris terakhir.',
          'Tekan Posting jurnal.',
        ],
      },
      {
        title: 'Jurnal penyesuaian',
        points: [
          'Tekan Jurnal penyesuaian untuk akrual, koreksi akhir bulan, dan sejenisnya. Nomornya berseri JP dan bertanggal akhir periode.',
          'Centang Balik otomatis untuk akrual: Lajur langsung membuat jurnal pembalik (seri JB) di awal bulan berikutnya.',
          'Hanya jurnal penyesuaian yang boleh masuk ke periode berstatus Penyesuaian.',
        ],
      },
      {
        title: 'Mengubah, membatalkan, dan mengulang',
        points: [
          'Ubah jurnal: hanya jurnal manual, selama periodenya belum dikunci. Nomornya tetap dan riwayat versinya tersimpan.',
          'Void jurnal…: membuat jurnal pembalik. Jurnal dari modul (faktur, tagihan, kas) dibatalkan dari dokumennya, bukan dari sini.',
          'Jadikan berulang…: jurnal dibuat otomatis setiap bulan, mis. sewa atau penyusutan manual. Jadwalnya ada di tab Berulang.',
        ],
      },
    ],
  },
  {
    id: 'buku-besar',
    group: 'Akuntansi',
    title: 'Buku besar, neraca lajur, dan bagan akun',
    summary: 'Menelusuri mutasi per akun, melihat neraca lajur, dan mengelola akun.',
    page: { to: '/buku-besar', label: 'Buku besar' },
    paths: ['/buku-besar', '/neraca-lajur', '/akun'],
    visible: allow('ledger.view', true),
    keywords: ['akun', 'coa', 'chart of account', 'saldo akun', 'neraca saldo'],
    sections: [
      {
        title: 'Buku besar',
        steps: ['Buka Buku besar, ketik kode atau nama akun.', 'Pilih rentang tanggal Dari dan Sampai.', 'Klik nomor jurnal untuk melihat detailnya dan membuka dokumen sumbernya.'],
      },
      {
        title: 'Neraca lajur',
        text: 'Menampilkan neraca saldo, penyesuaian, neraca saldo disesuaikan, laba rugi, dan neraca dalam satu tabel per periode.',
      },
      {
        title: 'Bagan akun',
        points: [
          'Angka kepala kode menentukan kelompok: 1 aset, 2 liabilitas, 3 ekuitas, 4 pendapatan usaha, 5 beban pokok, 6 beban usaha, 7 pendapatan lain-lain, 8 beban lain-lain, 9 pajak penghasilan.',
          'Tekan Akun baru untuk menambah akun di bawah akun induknya. Akun header hanya mengelompokkan; jurnal memakai akun detail.',
          'Kode akun yang salah ketik bisa dikoreksi lewat tombol ubah di baris akun, selama angka kepalanya sama (mis. 6-1105 menjadi 6-1150). Kode akun sistem (mis. 1-1401 PPN masukan) dan akun kelompok terkunci karena dipakai posting otomatis.',
          'Akun tidak dihapus. Bila tidak dipakai lagi, klik akunnya dan matikan Akun aktif (akun yang masih bersaldo tidak bisa dinonaktifkan).',
        ],
      },
    ],
  },
  {
    id: 'periode',
    group: 'Akuntansi',
    title: 'Tutup buku bulanan',
    summary: 'Urutan tutup buku dan status periode: Terbuka, Penyesuaian, Terkunci.',
    page: { to: '/periode', label: 'Periode dan tutup buku' },
    paths: ['/periode'],
    visible: allow('ledger.view', true),
    keywords: ['tutup buku', 'closing', 'kunci periode', 'akhir bulan'],
    sections: [
      {
        title: 'Status periode',
        points: [
          'Terbuka: semua jenis jurnal boleh masuk.',
          'Penyesuaian: ditutup untuk transaksi; hanya jurnal penyesuaian yang boleh masuk.',
          'Terkunci: dikunci final; tidak ada jurnal yang bisa masuk dan jurnal manual tidak bisa diubah lagi.',
        ],
      },
      {
        title: 'Urutan tutup buku yang disarankan',
        steps: [
          'Posting atau hapus semua draf faktur, tagihan, dan kas bulan itu.',
          'Selesaikan rekonsiliasi bank setiap rekening.',
          'Pastikan penyusutan aset bulan itu sudah diposting.',
          'Di Periode, tekan Tutup untuk transaksi.',
          'Catat jurnal penyesuaian (akrual, koreksi), lalu periksa Neraca lajur dan laporan.',
          'Selesaikan ekspor pajak masa itu (faktur keluaran, bukti potong) dan cocokkan faktur masukan.',
          'Tekan Kunci final.',
        ],
        note: 'Buka kembali hanya bila benar-benar perlu koreksi; tindakan ini tercatat di audit trail.',
      },
    ],
  },
  {
    id: 'cabang',
    group: 'Akuntansi',
    title: 'Cabang, departemen, dan penomoran',
    summary: 'Memilah laporan per cabang dan departemen, dan mengatur format nomor dokumen.',
    page: { to: '/cabang', label: 'Cabang dan penomoran' },
    paths: ['/cabang'],
    visible: allow('ledger.view', true),
    keywords: ['departemen', 'nomor dokumen', 'format nomor', 'penomoran'],
    sections: [
      {
        title: 'Cabang dan departemen',
        points: [
          'Tambah cabang dan departemen di tab masing-masing.',
          'Isi cabang dan departemen di faktur, tagihan, dan transaksi kas. Nilainya diwariskan ke setiap baris jurnal, sehingga laba rugi bisa dipilah.',
          'Pengguna bisa dibatasi ke cabang tertentu di menu Pengguna dan akses.',
        ],
      },
      {
        title: 'Penomoran dokumen',
        points: [
          'Tab Penomoran dokumen mengatur awalan, kapan nomor diulang (tiap bulan, tiap tahun, atau tidak pernah), jumlah digit urut, dan nomor terpisah per cabang untuk setiap jenis dokumen.',
          'Perubahan berlaku untuk dokumen berikutnya; nomor yang sudah terbit tidak berubah.',
        ],
      },
    ],
  },
  {
    id: 'impor',
    group: 'Akuntansi',
    title: 'Impor data dan saldo awal',
    summary: 'Memindahkan data dari Excel atau aplikasi lama saat mulai memakai Lajur.',
    page: { to: '/impor', label: 'Impor data' },
    paths: ['/impor'],
    visible: allow('ledger.view', true),
    keywords: ['excel', 'migrasi', 'saldo awal', 'upload', 'template'],
    sections: [
      {
        title: 'Langkah',
        steps: [
          'Buka Impor data, pilih jenisnya.',
          'Unduh template, isi di Excel, lalu pilih berkasnya.',
          'Untuk saldo awal, isi tanggal saldo awal (biasanya akhir bulan sebelum mulai memakai Lajur).',
          'Tekan Periksa berkas. Perbaiki baris yang ditolak sesuai pesannya.',
          'Tekan Impor. Berkas diimpor seluruhnya atau tidak sama sekali.',
        ],
      },
      {
        title: 'Urutan migrasi yang disarankan',
        steps: ['Kontak', 'Produk dan jasa', 'Neraca saldo awal', 'Saldo awal piutang dan saldo awal utang (per faktur atau tagihan yang belum lunas)', 'Saldo awal stok'],
        note: 'Setelah impor, cocokkan Neraca saldo dan Umur piutang/utang dengan data aplikasi lama.',
      },
    ],
  },

  // Admin ------------------------------------------------------------------------------
  {
    id: 'pengguna',
    group: 'Admin',
    title: 'Pengguna dan akses',
    summary: 'Menambah pengguna, memberi peran per PT, membatasi cabang, dan persona kustom.',
    page: { to: '/pengguna', label: 'Pengguna dan akses' },
    paths: ['/pengguna'],
    visible: allow('platform.user.manage'),
    keywords: ['user', 'akun pengguna', 'hak akses', 'persona', 'reset password'],
    sections: [
      {
        title: 'Menambah pengguna',
        steps: [
          'Buka Pengguna dan akses, tekan Pengguna baru.',
          'Isi nama, email, dan kata sandi awal.',
          'Beri peran di setiap PT yang boleh dibukanya. Bila perlu, batasi ke cabang tertentu.',
          'Minta pengguna mengganti kata sandi dan memasang 2FA saat pertama masuk.',
        ],
      },
      {
        title: 'Persona kustom',
        text: 'Tab Persona → Persona baru untuk membuat peran dengan izin pilihan, mis. kasir yang hanya mencatat kas.',
      },
      {
        title: 'Bantuan pengguna',
        points: ['Atur ulang kata sandi bila pengguna lupa.', 'Atur ulang 2FA bila pengguna ganti ponsel.', 'Buka kunci akun bila terkunci karena salah kata sandi berkali-kali.'],
      },
    ],
  },
  {
    id: 'audit',
    group: 'Admin',
    title: 'Audit trail',
    summary: 'Jejak siapa mengubah apa, kapan, dan dari mana.',
    page: { to: '/audit', label: 'Audit trail' },
    paths: ['/audit'],
    visible: allow('platform.audit.view'),
    keywords: ['log', 'riwayat perubahan', 'jejak'],
    sections: [
      {
        title: 'Membaca audit trail',
        points: [
          'Setiap posting, void, perubahan data, login, dan perubahan akses tercatat dengan waktu, pengguna, rincian sebelum dan sesudah, serta alamat IP. Catatan terbaru tampil paling atas.',
          'Lencana Rantai utuh berarti tidak ada catatan lama yang diubah diam-diam. Bila muncul Rantai rusak, segera laporkan ke Pemilik.',
          'Catatan audit tidak bisa diubah atau dihapus siapa pun.',
        ],
      },
    ],
  },
  {
    id: 'sistem',
    group: 'Admin',
    title: 'Sistem, lisensi, dan backup',
    summary: 'Status dan perpanjangan lisensi, backup, email SMTP, pembaruan, dan PT dalam grup.',
    page: { to: '/sistem', label: 'Sistem dan lisensi' },
    paths: ['/sistem', '/pt'],
    visible: allow('platform.system.manage'),
    keywords: ['lisensi', 'license', 'backup', 'smtp', 'email', 'update', 'pembaruan', 'pt baru'],
    sections: [
      {
        title: 'Lisensi',
        points: [
          'Status lisensi tampil di kanan atas. Banner muncul bila lisensi akan atau sudah berakhir.',
          'Memperpanjang: Sistem dan lisensi → Lisensi, pilih paket, bayar lewat tautannya, tekan Unggah bukti bayar. Setelah disetujui, tekan Cek ulang lisensi.',
          'Lisensi berakhir: aplikasi hanya bisa dibaca, tetapi laporan dan ekspor tetap bisa diunduh.',
        ],
      },
      {
        title: 'Backup',
        points: [
          'Backup harian berisi basis data dan lampiran, terenkripsi. Tab Backup menampilkan status dan riwayatnya.',
          'Banner kuning muncul bila backup belum diatur, gagal, atau tidak ada backup sukses dalam 48 jam. Segera hubungi admin IT.',
        ],
      },
      {
        title: 'Email, pembaruan, dan PT',
        points: [
          'Tab Email: isi server SMTP kantor untuk mengirim faktur dan notifikasi, lalu kirim email uji.',
          'Tab Pembaruan: memasang versi baru. Backup dibuat otomatis sebelum pembaruan.',
          'PT dalam grup: tekan PT baru untuk menambah PT dengan bagan akunnya sendiri, atau Ubah untuk mengoreksi nama PT dan nama singkatnya.',
        ],
      },
    ],
  },
]

export const faqs: Faq[] = [
  {
    q: 'Inventaris barang ada di menu mana?',
    a: 'Barang dagangan atau material yang dijual dan dipakai ada di Persediaan (stok, kartu stok, stock opname) dan Produk dan jasa (master barang). Peralatan kantor, kendaraan, mesin, dan gedung ada di Aset tetap.',
    topic: 'persediaan',
  },
  {
    q: 'Faktur atau tagihan salah input, bagaimana memperbaikinya?',
    a: 'Masih draf: buka lalu Ubah. Sudah diposting: Void lalu buat ulang yang benar. Bila hanya sebagian yang salah (retur atau potongan harga), buat nota kredit atau nota debit.',
    topic: 'faktur',
  },
  {
    q: 'Kenapa faktur tidak bisa di-void?',
    a: 'Void dulu pelunasan dan nota kreditnya. Bila faktur pajaknya sudah terbit, batalkan di Coretax lalu tandai dibatalkan di Pajak → Faktur keluaran. Periode yang sudah ditutup juga menolak void bertanggal di periode itu; pilih tanggal void di periode yang masih terbuka.',
    topic: 'faktur',
  },
  {
    q: 'Pembayaran pelanggan dicatat di Kas dan bank atau di Penjualan?',
    a: 'Di Penjualan → Terima pembayaran. Begitu juga pembayaran ke pemasok di Pembelian → Bayar tagihan. Kalau dicatat di Kas dan bank, piutang atau utangnya tidak berkurang.',
    topic: 'pelunasan',
  },
  {
    q: 'Kenapa tidak bisa memposting di tanggal tertentu?',
    a: 'Periodenya sudah ditutup (Penyesuaian) atau dikunci (Terkunci). Minta akuntan memeriksa menu Periode. Di periode Penyesuaian hanya jurnal penyesuaian yang boleh masuk.',
    topic: 'periode',
  },
  {
    q: 'Apakah jurnal yang sudah diposting bisa diubah?',
    a: 'Jurnal manual bisa diubah lewat Ubah jurnal selama periodenya belum dikunci, dengan riwayat versi. Jurnal dari faktur, tagihan, dan kas dikoreksi lewat dokumennya (void atau nota).',
    topic: 'jurnal',
  },
  {
    q: 'Kenapa menu Pajak, Jurnal, atau Laporan tidak muncul di layar saya?',
    a: 'Menu mengikuti peran Anda di PT aktif. Pengguna yang dibatasi ke cabang tertentu juga tidak melihat menu yang mencakup seluruh PT. Minta pengelola pengguna menyesuaikan akses Anda, atau periksa apakah PT aktifnya benar.',
    topic: 'mulai',
  },
  {
    q: 'Bagaimana mencatat pembelian kendaraan atau peralatan?',
    a: 'Catat tagihannya (atau transaksi kas) ke akun aset, mis. kendaraan. Lalu daftarkan di Aset tetap → Aset baru supaya penyusutannya berjalan otomatis.',
    topic: 'aset',
  },
  {
    q: 'Bagaimana memasukkan saldo awal saat mulai memakai Lajur?',
    a: 'Lewat Impor data: neraca saldo awal, saldo awal piutang, saldo awal utang, dan saldo awal stok. Ikuti urutan di panduan Impor data.',
    topic: 'impor',
  },
  {
    q: 'Faktur pajak masukan baru diterima bulan depan, bagaimana?',
    a: 'Buka Pajak → Faktur masukan, klik tagihannya, catat nomor faktur pajak, dan pilih masa pengkreditan yang lebih akhir (paling lama tiga masa sesudah masa faktur).',
    topic: 'faktur-masukan',
  },
  {
    q: 'Bagaimana menyimpan laporan sebagai PDF?',
    a: 'Tekan Cetak / PDF di halaman laporan, lalu pilih "Simpan sebagai PDF" sebagai printer di dialog cetak browser.',
    topic: 'laporan',
  },
  {
    q: 'Saya lupa kata sandi atau ganti ponsel untuk 2FA.',
    a: 'Hubungi admin. Admin membuka Pengguna dan akses, memilih nama Anda, lalu Atur ulang kata sandi atau Atur ulang 2FA.',
    topic: 'profil',
  },
  {
    q: 'Lisensi berakhir, apakah data hilang?',
    a: 'Tidak. Aplikasi masuk mode baca saja: data tetap bisa dilihat dan laporan tetap bisa diekspor. Setelah admin memperpanjang lisensi, semua fitur aktif kembali.',
    topic: 'sistem',
  },
  {
    q: 'Salah ketik nama PT atau kode akun, bisa diperbaiki?',
    a: 'Bisa. Nama PT diubah admin di PT dalam grup → Ubah. Kode akun diubah di Bagan akun lewat tombol ubah di baris akunnya, selama angka kepalanya tetap sama; jurnal lama ikut memakai kode baru. Kode akun sistem untuk posting otomatis tidak bisa diubah.',
    topic: 'buku-besar',
  },
  {
    q: 'Format nomor faktur ingin diganti, bisa?',
    a: 'Bisa, di Cabang dan penomoran → Penomoran dokumen. Perubahan berlaku untuk dokumen berikutnya.',
    topic: 'cabang',
  },
]

export function visibleTopics(me: Me): GuideTopic[] {
  return topics.filter((t) => !t.visible || t.visible(me))
}

/** Topik panduan untuk halaman yang sedang dibuka; yang paling spesifik menang. */
export function topicForPath(list: GuideTopic[], pathname: string, search: string): GuideTopic | undefined {
  const tab = new URLSearchParams(search).get('tab')
  let best: GuideTopic | undefined
  let score = -1
  for (const t of list) {
    for (const p of t.paths ?? []) {
      const [path, query] = p.split('?')
      const pathOk = path === '/' ? pathname === '/' : pathname === path || pathname.startsWith(path + '/')
      if (!pathOk) continue
      const wantTab = query ? new URLSearchParams(query).get('tab') : null
      if (wantTab && wantTab !== tab) continue
      const s = path.length * 2 + (wantTab ? 1 : 0)
      if (s > score) {
        best = t
        score = s
      }
    }
  }
  return best
}

/** Teks lengkap topik untuk pencarian. */
export function topicText(t: GuideTopic): string {
  return [t.title, t.summary, ...(t.keywords ?? []), ...t.sections.flatMap((s) => [s.title, s.text ?? '', ...(s.steps ?? []), ...(s.points ?? []), s.note ?? ''])].join(' ').toLowerCase()
}
