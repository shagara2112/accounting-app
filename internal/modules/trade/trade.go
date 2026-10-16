// Package trade berisi perhitungan bersama dokumen dagang, yaitu faktur penjualan dan tagihan
// pembelian: nilai baris, diskon, DPP, PPN per kode pajak, serta pengelompokan umur piutang
// dan utang. Tidak menyentuh basis data.
package trade

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/modules/tax"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	MaxLines       = 200
	maxDescription = 300
)

type LineInput struct {
	Description string       `json:"description"`
	AccountID   uuid.UUID    `json:"accountId"`
	Qty         money.Amount `json:"qty"`
	UnitPrice   money.Amount `json:"unitPrice"`
	DiscountPct money.Amount `json:"discountPct"` // 0–100, maksimal 2 desimal
	TaxCode     string       `json:"taxCode"`
	ProductID   *uuid.UUID   `json:"productId,omitempty"`
	ProjectID   *uuid.UUID   `json:"projectId,omitempty"` // dimensi proyek per baris (tagihan pembelian)
	// DiscountAmount menggantikan diskon persen dengan nominal tetap; dipakai faktur termin
	// untuk potongan uang muka supaya total potongan sama persis dengan uang muka.
	DiscountAmount *money.Amount `json:"discountAmount,omitempty"`
}

type Line struct {
	LineInput
	Gross    money.Amount `json:"gross"`    // qty × harga, dibulatkan ke rupiah
	Discount money.Amount `json:"discount"` // gross × diskon %, dibulatkan ke rupiah
	Net      money.Amount `json:"net"`      // DPP baris
	OtherDPP money.Amount `json:"otherDpp"`
	VAT      money.Amount `json:"vat"`
}

type Totals struct {
	Subtotal money.Amount `json:"subtotal"`
	Discount money.Amount `json:"discount"`
	DPP      money.Amount `json:"dpp"`
	OtherDPP money.Amount `json:"otherDpp"`
	VAT      money.Amount `json:"vat"`
	Total    money.Amount `json:"total"` // DPP + PPN
}

var hundred = money.FromInt(100)

// Compute menghitung semua baris pada tanggal dokumen. PPN dihitung per baris dengan kode
// pajak yang berlaku pada tanggal itu (TAX-02). Bila vatAllowed false (PT non-PKP),
// kode PPN bertarif ditolak.
func Compute(t tax.Table, date civil.Date, in []LineInput, vatAllowed bool) ([]Line, Totals, error) {
	if len(in) == 0 {
		return nil, Totals{}, httpx.Unprocessable("document.no_lines", "Isi minimal satu baris.")
	}
	if len(in) > MaxLines {
		return nil, Totals{}, httpx.Unprocessable("document.too_many_lines", fmt.Sprintf("Maksimal %d baris.", MaxLines))
	}
	fields := map[string]string{}
	lines := make([]Line, len(in))
	var tot Totals
	for i, li := range in {
		key := fmt.Sprintf("lines.%d.", i)
		li.Description = strings.TrimSpace(li.Description)
		if utf8.RuneCountInString(li.Description) > maxDescription {
			fields[key+"description"] = fmt.Sprintf("Keterangan maksimal %d karakter.", maxDescription)
		}
		if li.AccountID == uuid.Nil {
			fields[key+"accountId"] = "Pilih akun."
		}
		if li.Qty.Sign() <= 0 {
			fields[key+"qty"] = "Kuantitas harus lebih dari 0."
		}
		if li.UnitPrice.Sign() < 0 {
			fields[key+"unitPrice"] = "Harga tidak boleh negatif."
		}
		pct := li.DiscountPct
		if pct.Sign() < 0 || pct.Cmp(hundred) > 0 || !pct.MulFrac(1, 1, 2, money.Down).Equal(pct) {
			fields[key+"discountPct"] = "Diskon 0–100% dengan maksimal 2 desimal."
		}
		if d := li.DiscountAmount; d != nil && (d.Sign() < 0 || !d.MulFrac(1, 1, 0, money.Down).Equal(*d)) {
			fields[key+"discountAmount"] = "Potongan harus rupiah bulat dan tidak negatif."
		}
		code, err := t.Find(li.TaxCode, date)
		switch {
		case err != nil:
			fields[key+"taxCode"] = problemDetail(err)
		case code.Kind != tax.KindPPN:
			fields[key+"taxCode"] = fmt.Sprintf("%s bukan kode PPN.", code.Code)
		case !vatAllowed && code.RateBP > 0:
			fields[key+"taxCode"] = "PT ini bukan PKP, sehingga tidak boleh memungut PPN. Pilih NON-PPN."
		}
		if len(fields) > 0 {
			continue
		}

		l := Line{LineInput: li}
		l.Gross = li.Qty.Mul(li.UnitPrice, 0, money.HalfUp)
		l.Discount = l.Gross.Mul(pct.MulFrac(1, 100, 4, money.HalfUp), 0, money.HalfUp)
		if li.DiscountAmount != nil {
			if li.DiscountAmount.Cmp(l.Gross) > 0 {
				fields[key+"discountAmount"] = "Potongan melebihi nilai baris."
				continue
			}
			l.Discount = *li.DiscountAmount
		}
		l.Net = l.Gross.Sub(l.Discount)
		l.OtherDPP, l.VAT = code.VAT(l.Net)
		lines[i] = l

		tot.Subtotal = tot.Subtotal.Add(l.Gross)
		tot.Discount = tot.Discount.Add(l.Discount)
		tot.DPP = tot.DPP.Add(l.Net)
		tot.OtherDPP = tot.OtherDPP.Add(l.OtherDPP)
		tot.VAT = tot.VAT.Add(l.VAT)
	}
	if len(fields) > 0 {
		return nil, Totals{}, httpx.Unprocessable("document.invalid", FirstMessage(fields)).WithFields(fields)
	}
	tot.Total = tot.DPP.Add(tot.VAT)
	if tot.Total.Sign() <= 0 {
		return nil, Totals{}, httpx.Unprocessable("document.zero_total", "Total dokumen harus lebih dari 0.")
	}
	return lines, tot, nil
}

func problemDetail(err error) string {
	if p, ok := err.(*httpx.Problem); ok {
		return p.Detail
	}
	return err.Error()
}

// FirstMessage mengambil pesan field pertama (urut kunci) sebagai ringkasan problem.
func FirstMessage(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fields[keys[0]]
}

// Bucket adalah kelompok umur piutang atau utang.
type Bucket string

const (
	Current Bucket = "current" // belum jatuh tempo
	Days30  Bucket = "1-30"
	Days60  Bucket = "31-60"
	Days90  Bucket = "61-90"
	Over90  Bucket = "90+"
)

var Buckets = []Bucket{Current, Days30, Days60, Days90, Over90}

// BucketOf menentukan kelompok umur berdasarkan hari lewat jatuh tempo per tanggal asOf.
func BucketOf(due, asOf civil.Date) (Bucket, int) {
	late := asOf.DaysSince(due)
	switch {
	case late <= 0:
		return Current, late
	case late <= 30:
		return Days30, late
	case late <= 60:
		return Days60, late
	case late <= 90:
		return Days90, late
	default:
		return Over90, late
	}
}

// AgingRow adalah sisa piutang atau utang satu kontak, per kelompok umur.
type AgingRow struct {
	ContactID   uuid.UUID               `json:"contactId"`
	ContactName string                  `json:"contactName"`
	Buckets     map[Bucket]money.Amount `json:"buckets"`
	Total       money.Amount            `json:"total"`
	Documents   int                     `json:"documents"`
}

type Aging struct {
	AsOf   civil.Date              `json:"asOf"`
	Rows   []AgingRow              `json:"rows"`
	Totals map[Bucket]money.Amount `json:"totals"`
	Total  money.Amount            `json:"total"`
}

// AgingItem adalah satu dokumen yang belum lunas.
type AgingItem struct {
	ContactID   uuid.UUID
	Due         civil.Date
	Outstanding money.Amount
}

// BuildAging mengelompokkan dokumen per kontak dan umur, urut sisa terbesar.
func BuildAging(asOf civil.Date, items []AgingItem, names map[uuid.UUID]string) Aging {
	out := Aging{AsOf: asOf, Rows: []AgingRow{}, Totals: emptyBuckets()}
	index := map[uuid.UUID]int{}
	for _, it := range items {
		if it.Outstanding.Sign() <= 0 {
			continue
		}
		i, ok := index[it.ContactID]
		if !ok {
			i = len(out.Rows)
			index[it.ContactID] = i
			out.Rows = append(out.Rows, AgingRow{ContactID: it.ContactID, ContactName: names[it.ContactID], Buckets: emptyBuckets()})
		}
		b, _ := BucketOf(it.Due, asOf)
		r := &out.Rows[i]
		r.Buckets[b] = r.Buckets[b].Add(it.Outstanding)
		r.Total = r.Total.Add(it.Outstanding)
		r.Documents++
		out.Totals[b] = out.Totals[b].Add(it.Outstanding)
		out.Total = out.Total.Add(it.Outstanding)
	}
	sort.SliceStable(out.Rows, func(i, j int) bool { return out.Rows[i].Total.Cmp(out.Rows[j].Total) > 0 })
	return out
}

func emptyBuckets() map[Bucket]money.Amount {
	m := make(map[Bucket]money.Amount, len(Buckets))
	for _, b := range Buckets {
		m[b] = money.Amount{}
	}
	return m
}

// MonthVAT adalah jumlah DPP dan PPN dokumen yang diposting dalam satu masa pajak.
type MonthVAT struct {
	Month     civil.Date   `json:"month"`
	Documents int64        `json:"documents"`
	DPP       money.Amount `json:"dpp"`
	VAT       money.Amount `json:"vat"`
}
