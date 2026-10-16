package asset

import (
	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// Metode penyusutan.
const (
	Straight  = "garis_lurus"
	Declining = "saldo_menurun"
	None      = "tidak_disusutkan" // mis. tanah
)

// FiscalGroup adalah kelompok harta untuk penyusutan fiskal (UU PPh Pasal 11, PMK 96/2009 dan
// perubahannya). Tarif garis lurus = 1/umur, saldo menurun = 2/umur; bangunan hanya garis lurus.
type FiscalGroup struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	Years     int    `json:"years"`
	Declining bool   `json:"declining"` // saldo menurun diperbolehkan
}

var FiscalGroups = []FiscalGroup{
	{"kelompok_1", "Kelompok 1 (4 tahun)", 4, true},
	{"kelompok_2", "Kelompok 2 (8 tahun)", 8, true},
	{"kelompok_3", "Kelompok 3 (16 tahun)", 16, true},
	{"kelompok_4", "Kelompok 4 (20 tahun)", 20, true},
	{"bangunan_permanen", "Bangunan permanen (20 tahun)", 20, false},
	{"bangunan_tidak_permanen", "Bangunan tidak permanen (10 tahun)", 10, false},
	{"tidak_disusutkan", "Tidak disusutkan (tanah)", 0, false},
}

func fiscalGroup(code string) (FiscalGroup, bool) {
	for _, g := range FiscalGroups {
		if g.Code == code {
			return g, true
		}
	}
	return FiscalGroup{}, false
}

// Plan adalah parameter penyusutan komersial satu aset.
type Plan struct {
	Method     string
	Cost       money.Amount
	Residual   money.Amount
	LifeMonths int
	Start      civil.Date // bulan pertama penyusutan (tanggal 1)
	// Aset lama: penyusutan s.d. akhir OpeningMonth sudah tercatat sebesar OpeningAccum
	// sebelum memakai Lajur; jadwal dilanjutkan dari nilai buku sisanya.
	OpeningAccum money.Amount
	OpeningMonth *civil.Date
}

// Month adalah penyusutan satu bulan beserta akumulasi dan nilai buku sesudahnya.
type Month struct {
	Month     civil.Date   `json:"month"`
	Amount    money.Amount `json:"amount"`
	Accum     money.Amount `json:"accum"`
	BookValue money.Amount `json:"bookValue"`
}

// End adalah bulan terakhir masa manfaat.
func (p Plan) End() civil.Date { return p.Start.AddMonths(p.LifeMonths - 1) }

func monthsBetween(a, b civil.Date) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

func rupiah(a money.Amount, num, den int64) money.Amount { return a.MulFrac(num, den, 0, money.HalfUp) }

// Months menghitung penyusutan komersial bulan demi bulan sampai nilai buku mencapai nilai
// sisa, dimulai sesudah saldo awal bila ada. Semua angka dibulatkan ke rupiah penuh, dan
// selisih pembulatan masuk bulan terakhir.
//
// Garis lurus: (nilai buku − nilai sisa) dibagi rata ke sisa bulan masa manfaat.
// Saldo menurun: tarif tahunan 2/umur dari nilai buku awal tiap tahun kalender (tahun pertama
// dari bulan mulai). Pada 12 bulan terakhir masa manfaat, sisa nilai buku disusutkan rata,
// sehingga aset habis tepat di akhir masa manfaat.
func (p Plan) Months() []Month {
	if p.Method == None || p.LifeMonths <= 0 || p.Cost.Cmp(p.Residual) <= 0 {
		return nil
	}
	depreciable := p.Cost.Sub(p.Residual)
	accum := money.Amount{}
	first := p.Start
	if p.OpeningMonth != nil {
		accum = p.OpeningAccum
		if next := p.OpeningMonth.AddMonths(1); next.After(first) {
			first = next
		}
	}
	end := p.End()
	if first.After(end) || accum.Cmp(depreciable) >= 0 {
		return nil
	}
	remaining := monthsBetween(first, end) + 1

	var out []Month
	var monthly, base money.Amount
	switched := false // saldo menurun sudah beralih ke rata di 12 bulan terakhir
	for i := 0; i < remaining; i++ {
		m := first.AddMonths(i)
		left := remaining - i
		var amount money.Amount
		switch {
		case left == 1:
			amount = depreciable.Sub(accum)
		case p.Method == Straight:
			if i == 0 {
				monthly = rupiah(depreciable.Sub(accum), 1, int64(remaining))
			}
			amount = monthly
		case left <= 12:
			if !switched {
				switched = true
				monthly = rupiah(depreciable.Sub(accum), 1, int64(left))
			}
			amount = monthly
		default:
			if i == 0 || m.Month() == 1 {
				base = p.Cost.Sub(accum)
			}
			amount = rupiah(base, 2, int64(p.LifeMonths))
		}
		if rest := depreciable.Sub(accum); amount.Cmp(rest) > 0 {
			amount = rest
		}
		if amount.Sign() <= 0 {
			break
		}
		accum = accum.Add(amount)
		out = append(out, Month{Month: m, Amount: amount, Accum: accum, BookValue: p.Cost.Sub(accum)})
	}
	return out
}

// Year adalah penyusutan satu tahun buku (tahun kalender).
type Year struct {
	Year      int          `json:"year"`
	Amount    money.Amount `json:"amount"`
	Accum     money.Amount `json:"accum"`
	BookValue money.Amount `json:"bookValue"`
}

// ByYear merangkum penyusutan bulanan per tahun kalender.
func ByYear(months []Month) []Year {
	var out []Year
	for _, m := range months {
		if len(out) == 0 || out[len(out)-1].Year != m.Month.Year() {
			out = append(out, Year{Year: m.Month.Year()})
		}
		y := &out[len(out)-1]
		y.Amount = y.Amount.Add(m.Amount)
		y.Accum, y.BookValue = m.Accum, m.BookValue
	}
	return out
}

// Fiscal menghitung penyusutan fiskal per tahun pajak. Penyusutan dimulai pada bulan mulai;
// tahun pertama dihitung sebanding jumlah bulannya. Garis lurus: harga perolehan × tarif.
// Saldo menurun: nilai sisa buku awal tahun × tarif. Pada tahun berakhirnya masa manfaat,
// sisa nilai buku disusutkan sekaligus. Nilai sisa fiskal selalu nol.
func Fiscal(groupCode, method string, cost money.Amount, start civil.Date) []Year {
	g, ok := fiscalGroup(groupCode)
	if !ok || g.Years == 0 || cost.Sign() <= 0 {
		return nil
	}
	if !g.Declining {
		method = Straight
	}
	endYear := start.AddMonths(g.Years*12 - 1).Year()
	accum := money.Amount{}
	var out []Year
	for y := start.Year(); y <= endYear; y++ {
		months := int64(12)
		if y == start.Year() {
			months = int64(13 - int(start.Month()))
		}
		var amount money.Amount
		switch {
		case y == endYear:
			amount = cost.Sub(accum)
		case method == Declining:
			amount = cost.Sub(accum).MulFrac(2*months, int64(g.Years)*12, 0, money.HalfUp)
		default:
			amount = cost.MulFrac(months, int64(g.Years)*12, 0, money.HalfUp)
		}
		if rest := cost.Sub(accum); amount.Cmp(rest) > 0 {
			amount = rest
		}
		accum = accum.Add(amount)
		out = append(out, Year{Year: y, Amount: amount, Accum: accum, BookValue: cost.Sub(accum)})
	}
	return out
}
