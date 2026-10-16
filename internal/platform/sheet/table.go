package sheet

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Column adalah kolom yang dikenali di baris judul. Nama dicocokkan tanpa membedakan huruf
// besar, spasi, tanda baca, dan tanda bintang (penanda wajib di template).
type Column struct {
	Key      string
	Names    []string // nama pertama dipakai di template; sisanya alias
	Required bool
}

// Table adalah isi berkas setelah baris judulnya ditemukan.
type Table struct {
	index map[string]int
	Rows  []Row
}

// Row adalah satu baris data; Line adalah nomor baris seperti terlihat di Excel.
type Row struct {
	Line  int
	cells []string
	t     *Table
}

func (r Row) Get(key string) string {
	i, ok := r.t.index[key]
	if !ok || i >= len(r.cells) {
		return ""
	}
	return strings.TrimSpace(r.cells[i])
}

// Has: kolom ini ada di berkas (meski boleh kosong per baris).
func (t Table) Has(key string) bool { _, ok := t.index[key]; return ok }

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

func normalize(s string) string { return nonWord.ReplaceAllString(strings.ToLower(s), "") }

// Parse mencari baris judul di 30 baris pertama (template Lajur punya judul dan petunjuk di
// atasnya), lalu mengambil baris data di bawahnya. Baris kosong dilewati.
func Parse(rows [][]string, cols []Column) (Table, error) {
	want := map[string]string{}
	for _, c := range cols {
		for _, n := range c.Names {
			want[normalize(n)] = c.Key
		}
	}
	for h := 0; h < len(rows) && h < 30; h++ {
		index := map[string]int{}
		for i, cell := range rows[h] {
			if key, ok := want[normalize(cell)]; ok {
				if _, dup := index[key]; !dup {
					index[key] = i
				}
			}
		}
		var missing []string
		for _, c := range cols {
			if _, ok := index[c.Key]; c.Required && !ok {
				missing = append(missing, c.Names[0])
			}
		}
		if len(index) == 0 || len(missing) > 0 {
			if len(index) >= 2 && len(missing) > 0 {
				return Table{}, httpx.Unprocessable("import.columns", "Kolom wajib tidak ditemukan: "+strings.Join(missing, ", ")+". Pakai template dari halaman impor.")
			}
			continue
		}
		t := Table{index: index}
		for i := h + 1; i < len(rows); i++ {
			if blank(rows[i]) {
				continue
			}
			t.Rows = append(t.Rows, Row{Line: i + 1, cells: rows[i], t: &t})
		}
		if len(t.Rows) > MaxRows {
			return Table{}, httpx.Unprocessable("import.too_many_rows", fmt.Sprintf("Maksimal %d baris per berkas; pecah berkasnya.", MaxRows))
		}
		return t, nil
	}
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		if c.Required {
			names = append(names, c.Names[0])
		}
	}
	return Table{}, httpx.Unprocessable("import.columns", "Baris judul tidak ditemukan. Kolom wajib: "+strings.Join(names, ", ")+". Pakai template dari halaman impor.")
}

var thousands = regexp.MustCompile(`^\d{1,3}(\.\d{3})+$`)

// Amount membaca angka dengan kebiasaan Indonesia: "1.250.000", "1250000", "1.250.000,50",
// "Rp 1.250.000", "-500" atau "(500)". Maksimal 4 desimal.
func Amount(s string) (money.Amount, error) {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), " ", ""), " ", "")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "Rp"), "rp")
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "−") {
		neg, s = true, strings.TrimLeft(s, "-−")
	}
	switch {
	case strings.Contains(s, ","):
		s = strings.Replace(strings.ReplaceAll(s, ".", ""), ",", ".", 1)
	case thousands.MatchString(s):
		s = strings.ReplaceAll(s, ".", "")
	}
	a, err := money.Parse(s)
	if err != nil || s == "" {
		return money.Amount{}, fmt.Errorf("angka %q tidak terbaca", s)
	}
	if neg {
		a = a.Neg()
	}
	return a, nil
}

var (
	excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	dateForms  = []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "2-1-2006", "02.01.2006"}
)

// Date membaca "2026-10-01", "01/10/2026" (hari/bulan/tahun), atau nomor tanggal Excel.
func Date(s string) (civil.Date, error) {
	s = strings.TrimSpace(s)
	if n, ok := serial(s); ok {
		t := excelEpoch.AddDate(0, 0, n)
		return civil.New(t.Year(), t.Month(), t.Day()), nil
	}
	for _, f := range dateForms {
		if t, err := time.Parse(f, s); err == nil {
			return civil.New(t.Year(), t.Month(), t.Day()), nil
		}
	}
	return civil.Date{}, fmt.Errorf("tanggal %q tidak terbaca; pakai format 31/12/2026", s)
}

// serial membaca nomor tanggal Excel (bagian jam diabaikan), dalam rentang tahun 1950–2150.
func serial(s string) (int, bool) {
	whole, _, _ := strings.Cut(s, ",")
	n, err := strconv.Atoi(whole)
	if err != nil || n < 18264 || n > 91310 {
		return 0, false
	}
	return n, true
}

// Bool membaca ya/tidak, y/t, true/false, 1/0. Kosong = def.
func Bool(s string, def bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return def, nil
	case "ya", "y", "true", "1", "benar", "yes":
		return true, nil
	case "tidak", "t", "false", "0", "salah", "no", "n":
		return false, nil
	}
	return false, fmt.Errorf("isi ya atau tidak, bukan %q", s)
}

// Int membaca bilangan bulat tidak negatif, mis. termin hari.
func Int(s string, def int) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	a, err := Amount(s)
	if err != nil {
		return 0, err
	}
	r, _ := new(big.Rat).SetString(a.String())
	if r == nil || !r.IsInt() || r.Sign() < 0 || r.Num().Int64() > 3650 {
		return 0, fmt.Errorf("isi bilangan bulat 0–3650, bukan %q", s)
	}
	return int(r.Num().Int64()), nil
}
