package report

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// CashFlow adalah laporan arus kas metode tidak langsung (RPT-02) untuk satu periode.
type CashFlow struct {
	Statement
	Opening    money.Amount `json:"opening"`
	NetChange  money.Amount `json:"netChange"`
	Closing    money.Amount `json:"closing"`
	Reconciled bool         `json:"reconciled"` // awal + kenaikan bersih = akhir
	NoCash     bool         `json:"noCash"`     // belum ada rekening kas/bank terdaftar
}

// CashFlow mulai dari laba bersih, lalu membalik setiap perubahan akun neraca selain kas:
//
//	operasi    : penyusutan (akun akumulasi), aset lancar dan liabilitas jangka pendek
//	investasi  : aset tidak lancar
//	pendanaan  : liabilitas jangka panjang dan ekuitas
//
// Akun lancar = akun di bawah kelompok X-1000 bagan akun. Karena setiap jurnal seimbang, kas awal
// ditambah kenaikan bersih selalu sama dengan kas akhir; Reconciled memastikannya.
func (s *service) CashFlow(ctx context.Context, companyID uuid.UUID, p Period) (CashFlow, error) {
	if p.To.Before(p.From) {
		return CashFlow{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
	}
	c, err := s.chart(ctx, companyID)
	if err != nil {
		return CashFlow{}, err
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return CashFlow{}, err
	}
	cashList, err := s.Cash.Accounts(ctx, companyID)
	if err != nil {
		return CashFlow{}, err
	}
	isCash := map[uuid.UUID]bool{}
	for _, a := range cashList {
		isCash[a.AccountID] = true
	}
	moves, err := s.net(ctx, companyID, p.From, p.To, true)
	if err != nil {
		return CashFlow{}, err
	}
	before, err := s.net(ctx, companyID, beginning, p.From.AddDays(-1), false)
	if err != nil {
		return CashFlow{}, err
	}

	out := CashFlow{
		Statement: Statement{Title: "Laporan arus kas", Company: name, Columns: []Column{{Label: periodLabel(p), From: p.From, To: p.To}}},
		NoCash:    len(cashList) == 0,
	}
	var accounts []ledger.Account
	for _, a := range c.byID {
		if a.Postable {
			accounts = append(accounts, a)
		}
	}
	sortByCode(accounts)

	type bucket struct{ lines []Line }
	var depreciation, working, investing, financing bucket
	var cashMove money.Amount
	for _, a := range accounts {
		v := moves[a.ID]
		if isCash[a.ID] {
			cashMove = cashMove.Add(v)
			out.Opening = out.Opening.Add(before[a.ID])
			continue
		}
		if a.Class > 3 || v.IsZero() {
			continue
		}
		id := a.ID
		line := Line{Kind: LineAccount, Code: a.Code, Name: a.Name, Depth: 1, AccountID: &id, Values: []money.Amount{v.Neg()}}
		switch {
		case a.Class == 1 && c.accumulated(a):
			depreciation.lines = append(depreciation.lines, line)
		case a.Class == 1 && c.current(a), a.Class == 2 && c.current(a):
			working.lines = append(working.lines, line)
		case a.Class == 1:
			investing.lines = append(investing.lines, line)
		default:
			financing.lines = append(financing.lines, line)
		}
	}

	profit := c.sumClass(moves, 4, 5, 6, 7, 8, 9).Neg()
	sum := func(lines []Line) money.Amount {
		var t money.Amount
		for _, l := range lines {
			t = t.Add(l.Values[0])
		}
		return t
	}
	one := func(a money.Amount) []money.Amount { return []money.Amount{a} }
	st := &out.Statement

	st.Lines = append(st.Lines, Line{Kind: LineSection, Name: "Arus kas dari aktivitas operasi"},
		Line{Kind: LineComputed, Name: "Laba (rugi) bersih", Values: one(profit)})
	if len(depreciation.lines) > 0 {
		st.Lines = append(st.Lines, Line{Kind: LineGroup, Name: "Penyesuaian untuk pos nonkas: penyusutan dan amortisasi"})
		st.Lines = append(st.Lines, depreciation.lines...)
	}
	if len(working.lines) > 0 {
		st.Lines = append(st.Lines, Line{Kind: LineGroup, Name: "Perubahan aset lancar dan liabilitas jangka pendek"})
		st.Lines = append(st.Lines, working.lines...)
	}
	operating := profit.Add(sum(depreciation.lines)).Add(sum(working.lines))
	st.Lines = append(st.Lines, Line{Kind: LineTotal, Name: "Kas bersih dari aktivitas operasi", Values: one(operating)})

	st.Lines = append(st.Lines, Line{Kind: LineSection, Name: "Arus kas dari aktivitas investasi"})
	st.Lines = append(st.Lines, investing.lines...)
	inv := sum(investing.lines)
	st.Lines = append(st.Lines, Line{Kind: LineTotal, Name: "Kas bersih dari aktivitas investasi", Values: one(inv)})

	st.Lines = append(st.Lines, Line{Kind: LineSection, Name: "Arus kas dari aktivitas pendanaan"})
	st.Lines = append(st.Lines, financing.lines...)
	fin := sum(financing.lines)
	st.Lines = append(st.Lines, Line{Kind: LineTotal, Name: "Kas bersih dari aktivitas pendanaan", Values: one(fin)})

	out.NetChange = operating.Add(inv).Add(fin)
	out.Closing = out.Opening.Add(cashMove)
	out.Reconciled = out.Opening.Add(out.NetChange).Equal(out.Closing)
	st.Lines = append(st.Lines,
		Line{Kind: LineResult, Name: "Kenaikan (penurunan) bersih kas dan setara kas", Values: one(out.NetChange)},
		Line{Kind: LineComputed, Name: "Kas dan setara kas awal periode", Values: one(out.Opening)},
		Line{Kind: LineResult, Name: "Kas dan setara kas akhir periode", Values: one(out.Closing)},
	)
	return out, nil
}

// current: akun berada di kelompok lancar (X-1000) bagan akun.
func (c Chart) current(a ledger.Account) bool {
	g, ok := c.ancestor(a, 2)
	if !ok {
		g = a
	}
	_, rest, found := strings.Cut(g.Code, "-")
	return found && strings.HasPrefix(rest, "1")
}

// accumulated: akun akumulasi penyusutan atau amortisasi (kontra aset), dikenali dari namanya
// atau nama akun induknya.
func (c Chart) accumulated(a ledger.Account) bool {
	for {
		if strings.HasPrefix(strings.ToLower(a.Name), "akumulasi") {
			return true
		}
		if a.ParentID == nil || a.Level <= 2 {
			return false
		}
		a = c.byID[*a.ParentID]
	}
}
