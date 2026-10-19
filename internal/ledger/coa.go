package ledger

import (
	"fmt"
	"regexp"
	"strings"

	"lajur/internal/platform/money"
)

// Side adalah sisi saldo normal akun.
type Side string

const (
	Debit  Side = "debit"
	Credit Side = "kredit"
)

// Class adalah satu kelompok kode akun (PRD, tabel Kelompok kode akun). Angka kepala
// kode menentukan kelas; kelas menentukan saldo normal dan letaknya di laporan.
type Class struct {
	Class     int    `json:"class"`
	Name      string `json:"name"`
	Normal    Side   `json:"normal"`
	Statement string `json:"statement"` // "neraca" atau "laba_rugi"
}

var Classes = []Class{
	{1, "Aset", Debit, "neraca"},
	{2, "Liabilitas", Credit, "neraca"},
	{3, "Ekuitas", Credit, "neraca"},
	{4, "Pendapatan usaha", Credit, "laba_rugi"},
	{5, "Beban pokok", Debit, "laba_rugi"},
	{6, "Beban usaha", Debit, "laba_rugi"},
	{7, "Pendapatan lain-lain", Credit, "laba_rugi"},
	{8, "Beban lain-lain", Debit, "laba_rugi"},
	{9, "Pajak penghasilan", Debit, "laba_rugi"},
}

func ClassOf(n int) Class { return Classes[n-1] }

// Present mengubah saldo debit − kredit menjadi saldo menurut sisi normal kelas ini:
// positif berarti saldo normal, negatif berarti saldo berlawanan.
func (c Class) Present(net money.Amount) money.Amount {
	if c.Normal == Credit {
		return net.Neg()
	}
	return net
}

// MaxLevel adalah kedalaman bagan akun (GL-01).
const MaxLevel = 5

var codePattern = regexp.MustCompile(`^[1-9](-[0-9]{1,8}(\.[0-9]{1,4}){0,2})?$`)

// classOfCode mengembalikan kelas dari angka kepala kode, atau 0 bila format kode salah.
func classOfCode(code string) int {
	if !codePattern.MatchString(code) {
		return 0
	}
	return int(code[0] - '0')
}

// Templates adalah pilihan template bagan akun saat PT baru mulai.
var Templates = []string{"jasa", "konstruksi", "retail", "kosong"}

// TemplateAccount adalah satu akun di template; Parent kosong untuk akun kelompok (level 1).
type TemplateAccount struct {
	Code   string
	Name   string
	Parent string
	Level  int
	Header bool
}

// Template mengembalikan bagan akun standar untuk satu industri, urut dari induk ke anak.
// "kosong" hanya berisi sembilan akun kelompok.
func Template(name string) ([]TemplateAccount, error) {
	known := false
	for _, t := range Templates {
		known = known || t == name
	}
	if !known {
		return nil, fmt.Errorf("template %q tidak dikenal", name)
	}

	type node struct {
		TemplateAccount
		tags []string
	}
	var (
		out   []TemplateAccount
		stack []string // kode induk per level
	)
	for i, raw := range strings.Split(strings.Trim(coaTemplate, "\n"), "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		indent := len(raw) - len(trimmed)
		if indent%2 != 0 {
			panic(fmt.Sprintf("template bagan akun baris %d: indentasi harus kelipatan dua spasi", i+1))
		}
		level := indent/2 + 1
		text, tagText, _ := strings.Cut(trimmed, " @")
		code, accName, _ := strings.Cut(text, " ")
		n := node{TemplateAccount: TemplateAccount{Code: code, Name: accName, Level: level}}
		if tagText != "" {
			n.tags = strings.Split(tagText, ",")
		}

		if level > len(stack)+1 {
			panic(fmt.Sprintf("template bagan akun baris %d: level melompat", i+1))
		}
		stack = stack[:level-1]
		include := name != "kosong" || level == 1
		if len(n.tags) > 0 && !contains(n.tags, name) {
			include = false
		}
		if !include {
			// Anak dari akun yang tidak disertakan juga tidak disertakan.
			stack = append(stack, "")
			continue
		}
		if level > 1 {
			parent := stack[level-2]
			if parent == "" {
				stack = append(stack, "")
				continue
			}
			n.Parent = parent
			for j := range out {
				if out[j].Code == parent {
					out[j].Header = true
				}
			}
		}
		n.Header = level == 1
		out = append(out, n.TemplateAccount)
		stack = append(stack, code)
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// coaTemplate adalah bagan akun standar SAK EP untuk grup usaha jasa, konstruksi, dan retail.
// Indentasi dua spasi = satu level. "@jasa,retail" berarti akun hanya ada di industri itu;
// akun tanpa tanda ada di semua industri. Akun yang punya anak otomatis menjadi akun header.
// Kode sengaja sama di semua industri untuk akun yang sama, supaya konsolidasi grup mudah.
const coaTemplate = `
1 Aset
  1-1000 Aset lancar
    1-1100 Kas dan setara kas
      1-1101 Kas kecil
      1-1102 Kas besar
      1-1103 Kas toko @retail
      1-1111 Bank BCA
      1-1112 Bank Mandiri
    1-1200 Piutang
      1-1201 Piutang usaha
      1-1202 Piutang retensi
      1-1203 Piutang kartu dan QRIS @retail
      1-1204 Piutang karyawan
      1-1205 Piutang lain-lain
      1-1209 Cadangan kerugian piutang
    1-1300 Persediaan @konstruksi,retail
      1-1301 Persediaan material @konstruksi
      1-1302 Persediaan barang dagang @retail
    1-1400 Pajak dibayar di muka
      1-1401 PPN masukan
      1-1402 PPh 22 dibayar di muka
      1-1403 PPh 23 dibayar di muka
      1-1404 PPh 25 dibayar di muka
    1-1500 Biaya dibayar di muka dan uang muka
      1-1501 Sewa dibayar di muka
      1-1502 Asuransi dibayar di muka
      1-1503 Uang muka pembelian
      1-1504 Uang muka subkontraktor @konstruksi
  1-2000 Aset tidak lancar
    1-2100 Aset tetap
      1-2101 Tanah
      1-2102 Bangunan
      1-2103 Kendaraan
      1-2104 Peralatan kantor
      1-2105 Alat berat @konstruksi
      1-2106 Peralatan toko @retail
    1-2200 Akumulasi penyusutan
      1-2202 Akumulasi penyusutan bangunan
      1-2203 Akumulasi penyusutan kendaraan
      1-2204 Akumulasi penyusutan peralatan kantor
      1-2205 Akumulasi penyusutan alat berat @konstruksi
      1-2206 Akumulasi penyusutan peralatan toko @retail
2 Liabilitas
  2-1000 Liabilitas jangka pendek
    2-1100 Utang usaha
      2-1101 Utang usaha
      2-1102 Utang subkontraktor @konstruksi
      2-1103 Utang retensi subkontraktor @konstruksi
    2-1200 Utang pajak
      2-1201 PPN keluaran
      2-1202 Utang PPh 21
      2-1203 Utang PPh 23
      2-1204 Utang PPh 4(2)
      2-1205 Utang PPh 29
      2-1206 Utang PPh 26
    2-1300 Beban yang masih harus dibayar
      2-1301 Gaji yang masih harus dibayar
      2-1302 Beban yang masih harus dibayar lainnya
    2-1400 Uang muka dan pendapatan diterima di muka
      2-1401 Uang muka pelanggan
      2-1402 Uang muka proyek @konstruksi
      2-1403 Pendapatan diterima di muka @jasa
  2-2000 Liabilitas jangka panjang
    2-2101 Utang bank jangka panjang
    2-2102 Utang pihak berelasi
3 Ekuitas
  3-1000 Modal
    3-1001 Modal disetor
    3-1002 Tambahan modal disetor
  3-2000 Saldo laba
    3-2001 Laba ditahan
    3-2002 Laba tahun berjalan
    3-2003 Dividen
  3-3000 Ekuitas lain
    3-3001 Ekuitas saldo awal
4 Pendapatan usaha
  4-1000 Pendapatan usaha
    4-1001 Pendapatan jasa @jasa
    4-1002 Pendapatan retainer @jasa
    4-1001 Pendapatan proyek konstruksi @konstruksi
    4-1002 Pendapatan sewa alat @konstruksi
    4-1001 Penjualan barang dagang @retail
    4-1002 Penjualan online @retail
  4-2000 Potongan dan retur
    4-2001 Potongan penjualan
    4-2002 Retur penjualan @retail
5 Beban pokok
  5-1000 Beban pokok jasa @jasa
    5-1001 Beban tenaga ahli @jasa
    5-1002 Beban subkontraktor jasa @jasa
    5-1003 Beban perjalanan proyek @jasa
  5-1000 Biaya proyek @konstruksi
    5-1001 Biaya material @konstruksi
    5-1002 Biaya upah langsung @konstruksi
    5-1003 Biaya subkontraktor @konstruksi
    5-1004 Biaya sewa alat @konstruksi
    5-1005 Biaya overhead proyek @konstruksi
  5-1000 Harga pokok penjualan @retail
    5-1001 HPP barang dagang @retail
    5-1002 Ongkos angkut pembelian @retail
    5-1003 Selisih persediaan @retail
6 Beban usaha
  6-1000 Beban pegawai
    6-1001 Gaji dan upah
    6-1002 Tunjangan dan THR
    6-1003 Iuran BPJS
  6-2000 Beban operasional
    6-2001 Beban sewa
    6-2002 Listrik, air, dan telepon
    6-2003 Internet dan langganan perangkat lunak
    6-2004 Perlengkapan kantor
    6-2005 Transportasi dan perjalanan dinas
    6-2006 Pemeliharaan dan perbaikan
    6-2007 Asuransi
    6-2008 Jasa profesional
    6-2009 Pemasaran dan promosi
  6-3000 Beban penyusutan
    6-3002 Penyusutan bangunan
    6-3003 Penyusutan kendaraan
    6-3004 Penyusutan peralatan kantor
    6-3005 Penyusutan alat berat @konstruksi
    6-3006 Penyusutan peralatan toko @retail
  6-4000 Beban umum lainnya
    6-4001 Beban kerugian piutang
    6-4002 Beban administrasi umum
    6-4003 Beban PPN masukan tidak dapat dikreditkan
7 Pendapatan lain-lain
  7-1000 Pendapatan lain-lain
    7-1001 Pendapatan jasa giro dan bunga
    7-1002 Laba selisih kurs
    7-1003 Laba penjualan aset tetap
    7-1009 Pendapatan lain-lain
8 Beban lain-lain
  8-1000 Beban lain-lain
    8-1001 Beban administrasi bank
    8-1002 Beban bunga pinjaman
    8-1003 Rugi selisih kurs
    8-1004 PPh final jasa giro
    8-1005 Rugi pelepasan aset tetap
    8-1009 Beban lain-lain
9 Pajak penghasilan
  9-1000 Pajak penghasilan
    9-1001 Beban pajak kini
    9-1002 Beban pajak tangguhan
    9-1003 Beban PPh final
`
