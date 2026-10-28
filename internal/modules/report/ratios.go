package report

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// Rasio keuangan (RPT-09) per akhir bulan. Neraca dihitung sejak awal sampai akhir bulan, laba
// rugi sejak awal tahun buku (tanpa jurnal penutup). Aset lancar = akun kelas 1 berkode 1-1…,
// liabilitas jangka pendek = akun kelas 2 berkode 2-1…, mengikuti template bagan akun dan skema
// kode yang lazim. Kas = rekening kas dan bank yang terdaftar. Piutang dan utang usaha dari akun
// sistemnya.

// RatioBasis adalah angka penyusun rasio satu bulan, supaya rasio bisa ditelusuri.
type RatioBasis struct {
	Month              string       `json:"month"`
	Days               int          `json:"days"` // hari sejak awal tahun buku s.d. akhir bulan
	CurrentAssets      money.Amount `json:"currentAssets"`
	CurrentLiabilities money.Amount `json:"currentLiabilities"`
	Cash               money.Amount `json:"cash"`
	Liabilities        money.Amount `json:"liabilities"`
	Equity             money.Amount `json:"equity"`
	Revenue            money.Amount `json:"revenue"` // pendapatan usaha (kelas 4)
	COGS               money.Amount `json:"cogs"`    // beban pokok (kelas 5)
	Opex               money.Amount `json:"opex"`    // beban usaha (kelas 6)
	NetProfit          money.Amount `json:"netProfit"`
	Receivable         money.Amount `json:"receivable"`
	Payable            money.Amount `json:"payable"`
}

type RatioRow struct {
	Key     string     `json:"key"`
	Name    string     `json:"name"`
	Formula string     `json:"formula"`
	Unit    string     `json:"unit"` // x, %, atau hari
	Guide   string     `json:"guide"`
	Values  []*float64 `json:"values"` // nil bila tidak bisa dihitung (mis. pembagi nol)
	// Status bulan terakhir menurut panduan umum: good, watch, bad, atau kosong bila tidak dinilai.
	Status string `json:"status,omitempty"`
}

type Ratios struct {
	Months []string     `json:"months"`
	Rows   []RatioRow   `json:"rows"`
	Basis  []RatioBasis `json:"basis"`
}

func toFloat(a money.Amount) float64 {
	v, _ := strconv.ParseFloat(a.String(), 64)
	return v
}

func div(a, b money.Amount, scale float64) *float64 {
	if b.Sign() <= 0 {
		return nil
	}
	v := toFloat(a) / toFloat(b) * scale
	return &v
}

// Ratios menghitung rasio untuk `months` akhir bulan yang berakhir di bulan month.
func (s *service) Ratios(ctx context.Context, companyID uuid.UUID, month civil.Date, months int) (Ratios, error) {
	if months < 1 || months > 24 {
		months = 6
	}
	accounts, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return Ratios{}, err
	}
	byID := make(map[uuid.UUID]ledger.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	cashAccounts, err := s.Cash.Accounts(ctx, companyID)
	if err != nil {
		return Ratios{}, err
	}
	isCash := map[uuid.UUID]bool{}
	for _, c := range cashAccounts {
		isCash[c.AccountID] = true
	}
	role := func(r ledger.Role) uuid.UUID {
		a, err := s.Ledger.RoleAccount(ctx, companyID, r)
		if err != nil {
			return uuid.Nil // akun sistem belum dipilih: rasionya tidak dihitung
		}
		return a.ID
	}
	receivable, payable := role(ledger.Receivable), role(ledger.Payable)
	startMonth, err := s.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return Ratios{}, err
	}

	out := Ratios{}
	for i := months - 1; i >= 0; i-- {
		end := month.MonthStart().AddMonths(-i).MonthEnd()
		bal, err := s.net(ctx, companyID, beginning, end, false)
		if err != nil {
			return Ratios{}, err
		}
		fy := fiscalStart(startMonth, end)
		pl, err := s.net(ctx, companyID, fy, end, true)
		if err != nil {
			return Ratios{}, err
		}
		b := RatioBasis{Month: end.MonthKey(), Days: end.DaysSince(fy) + 1}
		for id, v := range bal {
			a, ok := byID[id]
			if !ok {
				continue
			}
			switch {
			case a.Class == 1:
				if strings.HasPrefix(a.Code, "1-1") {
					b.CurrentAssets = b.CurrentAssets.Add(v)
				}
				if isCash[id] {
					b.Cash = b.Cash.Add(v)
				}
			case a.Class == 2:
				b.Liabilities = b.Liabilities.Sub(v)
				if strings.HasPrefix(a.Code, "2-1") {
					b.CurrentLiabilities = b.CurrentLiabilities.Sub(v)
				}
			default: // ekuitas = − (kelas 3 + laba rugi yang belum ditutup)
				b.Equity = b.Equity.Sub(v)
			}
		}
		if receivable != uuid.Nil {
			b.Receivable = bal[receivable]
		}
		if payable != uuid.Nil {
			b.Payable = bal[payable].Neg()
		}
		for id, v := range pl {
			a, ok := byID[id]
			if !ok || a.Class < 4 {
				continue
			}
			b.NetProfit = b.NetProfit.Sub(v)
			switch a.Class {
			case 4:
				b.Revenue = b.Revenue.Sub(v)
			case 5:
				b.COGS = b.COGS.Add(v)
			case 6:
				b.Opex = b.Opex.Add(v)
			}
		}
		out.Months = append(out.Months, b.Month)
		out.Basis = append(out.Basis, b)
	}

	row := func(key, name, formula, unit, guide string, f func(RatioBasis) *float64, status func(float64) string) RatioRow {
		r := RatioRow{Key: key, Name: name, Formula: formula, Unit: unit, Guide: guide, Values: make([]*float64, len(out.Basis))}
		for i, b := range out.Basis {
			r.Values[i] = f(b)
		}
		if last := r.Values[len(r.Values)-1]; last != nil && status != nil {
			r.Status = status(*last)
		}
		return r
	}
	band := func(good, watch float64, higherBetter bool) func(float64) string {
		return func(v float64) string {
			switch {
			case higherBetter && v >= good, !higherBetter && v <= good:
				return "good"
			case higherBetter && v >= watch, !higherBetter && v <= watch:
				return "watch"
			}
			return "bad"
		}
	}
	purchases := func(b RatioBasis) money.Amount {
		if b.COGS.Sign() > 0 {
			return b.COGS
		}
		return b.Opex
	}
	out.Rows = []RatioRow{
		row("current", "Rasio lancar", "Aset lancar ÷ liabilitas jangka pendek", "x",
			"Kemampuan membayar kewajiban jangka pendek. Umumnya sehat bila ≥ 1,5; di bawah 1 berarti aset lancar tidak cukup menutup utang jangka pendek.",
			func(b RatioBasis) *float64 { return div(b.CurrentAssets, b.CurrentLiabilities, 1) }, band(1.5, 1, true)),
		row("cash", "Rasio kas", "Kas dan bank ÷ liabilitas jangka pendek", "x",
			"Bagian utang jangka pendek yang bisa langsung dibayar dengan kas. Umumnya aman bila ≥ 0,5.",
			func(b RatioBasis) *float64 { return div(b.Cash, b.CurrentLiabilities, 1) }, band(0.5, 0.2, true)),
		row("der", "Utang terhadap ekuitas", "Total liabilitas ÷ ekuitas", "x",
			"Seberapa besar usaha dibiayai utang dibanding modal sendiri. Umumnya aman bila ≤ 1; di atas 2 berisiko.",
			func(b RatioBasis) *float64 { return div(b.Liabilities, b.Equity, 1) }, band(1, 2, false)),
		row("gross", "Margin laba kotor", "(Pendapatan usaha − beban pokok) ÷ pendapatan usaha", "%",
			"Sisa pendapatan setelah beban pokok, sejak awal tahun buku. Bandingkan antarbulan; usaha jasa tanpa beban pokok mendekati 100%.",
			func(b RatioBasis) *float64 { return div(b.Revenue.Sub(b.COGS), b.Revenue, 100) }, func(v float64) string {
				if v < 0 {
					return "bad"
				}
				return ""
			}),
		row("net", "Margin laba bersih", "Laba bersih ÷ pendapatan usaha", "%",
			"Laba bersih dari setiap rupiah pendapatan, sejak awal tahun buku. Positif berarti usaha untung; ≥ 10% umumnya baik.",
			func(b RatioBasis) *float64 { return div(b.NetProfit, b.Revenue, 100) }, band(10, 0, true)),
		row("dso", "Lama penagihan piutang (DSO)", "Piutang usaha ÷ pendapatan usaha × hari", "hari",
			"Rata-rata hari sampai faktur dibayar pelanggan. Makin kecil makin baik; bandingkan dengan termin pembayaran (mis. 30 hari).",
			func(b RatioBasis) *float64 {
				if receivable == uuid.Nil {
					return nil
				}
				return div(b.Receivable, b.Revenue, float64(b.Days))
			}, band(45, 90, false)),
		row("dpo", "Lama pembayaran utang (DPO)", "Utang usaha ÷ beban pokok (atau beban usaha) × hari", "hari",
			"Rata-rata hari sampai tagihan pemasok dibayar. Terlalu kecil membebani kas; terlalu besar bisa mengganggu hubungan dengan pemasok.",
			func(b RatioBasis) *float64 {
				if payable == uuid.Nil {
					return nil
				}
				return div(b.Payable, purchases(b), float64(b.Days))
			}, nil),
	}
	return out, nil
}
