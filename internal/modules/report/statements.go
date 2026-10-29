package report

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const maxColumns = 4

// builder menyusun baris laporan dari pohon akun. sign +1 menampilkan debit − kredit (akun
// bersaldo normal debit), −1 menampilkan kredit − debit.
type builder struct {
	Chart
	cols []map[uuid.UUID]money.Amount
}

func (b builder) zeros() []money.Amount { return make([]money.Amount, len(b.cols)) }

func (b builder) values(id uuid.UUID, sign int) []money.Amount {
	out := b.zeros()
	for i, c := range b.cols {
		v := c[id]
		if sign < 0 {
			v = v.Neg()
		}
		out[i] = v
	}
	return out
}

func allZero(v []money.Amount) bool {
	for _, a := range v {
		if !a.IsZero() {
			return false
		}
	}
	return true
}

func add(a, b []money.Amount) []money.Amount {
	out := make([]money.Amount, len(a))
	for i := range a {
		out[i] = a[i].Add(b[i])
	}
	return out
}

func sub(a, b []money.Amount) []money.Amount {
	out := make([]money.Amount, len(a))
	for i := range a {
		out[i] = a[i].Sub(b[i])
	}
	return out
}

// node menyusun satu akun beserta anak-anaknya. Akun yang saldonya nol di semua kolom tidak
// ditampilkan; akun header dengan satu anak saja langsung diganti anaknya, supaya laporan ringkas.
func (b builder) node(a ledger.Account, depth, sign int) ([]Line, []money.Amount, bool) {
	if a.Postable {
		v := b.values(a.ID, sign)
		if allZero(v) {
			return nil, v, false
		}
		id := a.ID
		return []Line{{Kind: LineAccount, Code: a.Code, Name: a.Name, Depth: depth, AccountID: &id, Values: v}}, v, true
	}
	var shown []ledger.Account
	for _, c := range b.children[a.ID] {
		if _, _, ok := b.node(c, depth+1, sign); ok {
			shown = append(shown, c)
		}
	}
	switch len(shown) {
	case 0:
		return nil, b.zeros(), false
	case 1:
		return b.node(shown[0], depth, sign)
	}
	lines := []Line{{Kind: LineGroup, Code: a.Code, Name: a.Name, Depth: depth}}
	total := b.zeros()
	for _, c := range shown {
		l, v, _ := b.node(c, depth+1, sign)
		lines = append(lines, l...)
		total = add(total, v)
	}
	lines = append(lines, Line{Kind: LineSubtotal, Name: "Jumlah " + lowerFirst(a.Name), Depth: depth, Values: total})
	return lines, total, true
}

// class menyusun semua akun di bawah satu kelas.
func (b builder) class(class, sign int) ([]Line, []money.Amount) {
	root, ok := b.roots[class]
	if !ok {
		return nil, b.zeros()
	}
	var lines []Line
	total := b.zeros()
	for _, c := range b.children[root.ID] {
		l, v, shown := b.node(c, 0, sign)
		if shown {
			lines = append(lines, l...)
			total = add(total, v)
		}
	}
	return lines, total
}

func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) > 1 && r[1] >= 'a' && r[1] <= 'z' && r[0] >= 'A' && r[0] <= 'Z' {
		r[0] += 'a' - 'A'
	}
	return string(r)
}

func checkColumns(n int) error {
	if n == 0 || n > maxColumns {
		return httpx.BadRequest("report.columns", fmt.Sprintf("Pilih 1–%d periode.", maxColumns))
	}
	return nil
}

// BalanceSheet menyusun laporan posisi keuangan per tanggal (RPT-01). Laba yang belum ditutup
// ke laba ditahan ditampilkan sebagai dua baris ekuitas: tahun-tahun lalu dan tahun berjalan.
func (s *service) BalanceSheet(ctx context.Context, companyID uuid.UUID, dates []civil.Date) (Statement, error) {
	if err := checkColumns(len(dates)); err != nil {
		return Statement{}, err
	}
	c, err := s.chart(ctx, companyID)
	if err != nil {
		return Statement{}, err
	}
	start, err := s.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return Statement{}, err
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return Statement{}, err
	}
	b := builder{Chart: c}
	st := Statement{Title: "Laporan posisi keuangan", Company: name}
	var prior, current []money.Amount
	for _, d := range dates {
		all, year, err := s.position(ctx, companyID, fiscalStart(start, d), d)
		if err != nil {
			return Statement{}, err
		}
		b.cols = append(b.cols, all)
		cur := c.sumClass(year, 4, 5, 6, 7, 8, 9).Neg()
		current = append(current, cur)
		prior = append(prior, c.sumClass(all, 4, 5, 6, 7, 8, 9).Neg().Sub(cur))
		st.Columns = append(st.Columns, Column{Label: "Per " + d.Indonesian(), From: beginning, To: d})
	}

	st.Lines, st.Balanced = c.BalanceSheetLines(b.cols, prior, current)
	return st, nil
}

// ProfitLoss menyusun laporan laba rugi untuk satu atau beberapa periode (RPT-01), tanpa jurnal
// penutup: pendapatan usaha − beban pokok = laba kotor; − beban usaha = laba usaha; ± pendapatan
// dan beban lain-lain = laba sebelum pajak; − pajak penghasilan = laba bersih.
func (s *service) ProfitLoss(ctx context.Context, companyID uuid.UUID, periods []Period) (Statement, error) {
	if err := checkColumns(len(periods)); err != nil {
		return Statement{}, err
	}
	c, err := s.chart(ctx, companyID)
	if err != nil {
		return Statement{}, err
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return Statement{}, err
	}
	b := builder{Chart: c}
	st := Statement{Title: "Laporan laba rugi", Company: name}
	for _, p := range periods {
		if p.To.Before(p.From) {
			return Statement{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
		}
		v, err := s.net(ctx, companyID, p.From, p.To, true)
		if err != nil {
			return Statement{}, err
		}
		b.cols = append(b.cols, v)
		st.Columns = append(st.Columns, Column{Label: periodLabel(p), From: p.From, To: p.To})
	}

	st.Lines = c.ProfitLossLines(b.cols)
	return st, nil
}

// periodLabel: "Oktober 2026", "Jan – Okt 2026", atau rentang tanggal lengkap.
func periodLabel(p Period) string {
	switch {
	case p.From.Day() == 1 && p.To == p.From.MonthEnd():
		return p.From.MonthLabel()
	case p.From.Day() == 1 && p.To == p.To.MonthEnd() && p.From.Year() == p.To.Year():
		return civil.MonthName(p.From.Month())[:3] + " – " + civil.MonthName(p.To.Month())[:3] + " " + fmt.Sprint(p.To.Year())
	default:
		return p.From.Indonesian() + " – " + p.To.Indonesian()
	}
}

type TrialRow struct {
	AccountID *uuid.UUID   `json:"accountId,omitempty"`
	Code      string       `json:"code"`
	Name      string       `json:"name"`
	Class     int          `json:"class"`
	Debit     money.Amount `json:"debit"`
	Credit    money.Amount `json:"credit"`
}

// TrialBalance adalah neraca saldo per tanggal (RPT-03): akun neraca sejak awal, akun laba rugi
// sejak awal tahun buku, dan laba tahun-tahun lalu yang belum ditutup sebagai satu baris.
type TrialBalance struct {
	Company     string       `json:"company"`
	Date        civil.Date   `json:"date"`
	FiscalStart civil.Date   `json:"fiscalStart"`
	Rows        []TrialRow   `json:"rows"`
	TotalDebit  money.Amount `json:"totalDebit"`
	TotalCredit money.Amount `json:"totalCredit"`
	Balanced    bool         `json:"balanced"`
}

func (s *service) TrialBalance(ctx context.Context, companyID uuid.UUID, date civil.Date) (TrialBalance, error) {
	c, err := s.chart(ctx, companyID)
	if err != nil {
		return TrialBalance{}, err
	}
	start, err := s.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return TrialBalance{}, err
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return TrialBalance{}, err
	}
	fs := fiscalStart(start, date)
	all, year, err := s.position(ctx, companyID, fs, date)
	if err != nil {
		return TrialBalance{}, err
	}
	out := TrialBalance{Company: name, Date: date, FiscalStart: fs, Rows: []TrialRow{}}
	var accounts []ledger.Account
	for _, a := range c.byID {
		if a.Postable {
			accounts = append(accounts, a)
		}
	}
	sortByCode(accounts)
	put := func(r TrialRow, net money.Amount) {
		if net.IsZero() {
			return
		}
		if net.Sign() > 0 {
			r.Debit = net
		} else {
			r.Credit = net.Neg()
		}
		out.Rows = append(out.Rows, r)
		out.TotalDebit = out.TotalDebit.Add(r.Debit)
		out.TotalCredit = out.TotalCredit.Add(r.Credit)
	}
	for _, a := range accounts {
		net := all[a.ID]
		if a.Class >= 4 {
			net = year[a.ID]
		}
		id := a.ID
		put(TrialRow{AccountID: &id, Code: a.Code, Name: a.Name, Class: a.Class}, net)
	}
	prior := c.sumClass(all, 4, 5, 6, 7, 8, 9).Sub(c.sumClass(year, 4, 5, 6, 7, 8, 9))
	put(TrialRow{Code: "", Name: "Laba (rugi) tahun-tahun lalu yang belum ditutup", Class: 3}, prior)
	out.Balanced = out.TotalDebit.Equal(out.TotalCredit)
	return out, nil
}

func sortByCode(list []ledger.Account) {
	sort.Slice(list, func(i, j int) bool { return list[i].Code < list[j].Code })
}

// BalanceSheetLines menyusun baris laporan posisi keuangan dari mutasi sejak awal per kolom,
// ditambah laba tahun-tahun lalu yang belum ditutup dan laba tahun berjalan per kolom.
// Balanced: aset = liabilitas + ekuitas per kolom.
func (c Chart) BalanceSheetLines(cols []map[uuid.UUID]money.Amount, prior, current []money.Amount) ([]Line, []bool) {
	b := builder{Chart: c, cols: cols}
	var lines []Line
	assetLines, assets := b.class(1, 1)
	lines = append(lines, Line{Kind: LineSection, Name: "Aset"})
	lines = append(lines, assetLines...)
	lines = append(lines, Line{Kind: LineTotal, Name: "Jumlah aset", Values: assets})

	liabLines, liabilities := b.class(2, -1)
	lines = append(lines, Line{Kind: LineSection, Name: "Liabilitas"})
	lines = append(lines, liabLines...)
	lines = append(lines, Line{Kind: LineTotal, Name: "Jumlah liabilitas", Values: liabilities})

	eqLines, equity := b.class(3, -1)
	lines = append(lines, Line{Kind: LineSection, Name: "Ekuitas"})
	lines = append(lines, eqLines...)
	if !allZero(prior) {
		lines = append(lines, Line{Kind: LineComputed, Name: "Laba (rugi) tahun-tahun lalu yang belum ditutup", Values: prior})
		equity = add(equity, prior)
	}
	lines = append(lines, Line{Kind: LineComputed, Name: "Laba (rugi) tahun berjalan", Values: current})
	equity = add(equity, current)
	lines = append(lines, Line{Kind: LineTotal, Name: "Jumlah ekuitas", Values: equity})
	both := add(liabilities, equity)
	lines = append(lines, Line{Kind: LineResult, Name: "Jumlah liabilitas dan ekuitas", Values: both})
	balanced := make([]bool, len(cols))
	for i := range cols {
		balanced[i] = assets[i].Equal(both[i])
	}
	return lines, balanced
}

// ProfitLossLines menyusun laba rugi bertingkat dari mutasi per kolom: pendapatan usaha − beban
// pokok = laba kotor; − beban usaha = laba usaha; ± lain-lain = laba sebelum pajak; − pajak
// penghasilan = laba bersih.
func (c Chart) ProfitLossLines(cols []map[uuid.UUID]money.Amount) []Line {
	b := builder{Chart: c, cols: cols}
	var lines []Line
	section := func(title, total string, class, sign int) []money.Amount {
		ls, sum := b.class(class, sign)
		if len(ls) == 0 {
			return sum
		}
		lines = append(lines, Line{Kind: LineSection, Name: title})
		lines = append(lines, ls...)
		lines = append(lines, Line{Kind: LineTotal, Name: total, Values: sum})
		return sum
	}
	result := func(name string, v []money.Amount) {
		lines = append(lines, Line{Kind: LineResult, Name: name, Values: v})
	}
	revenue := section("Pendapatan usaha", "Jumlah pendapatan usaha", 4, -1)
	cogs := section("Beban pokok", "Jumlah beban pokok", 5, 1)
	gross := sub(revenue, cogs)
	result("Laba kotor", gross)
	opex := section("Beban usaha", "Jumlah beban usaha", 6, 1)
	operating := sub(gross, opex)
	result("Laba usaha", operating)
	other := section("Pendapatan lain-lain", "Jumlah pendapatan lain-lain", 7, -1)
	otherExp := section("Beban lain-lain", "Jumlah beban lain-lain", 8, 1)
	beforeTax := sub(add(operating, other), otherExp)
	result("Laba sebelum pajak penghasilan", beforeTax)
	incomeTax := section("Pajak penghasilan", "Jumlah pajak penghasilan", 9, 1)
	result("Laba (rugi) bersih", sub(beforeTax, incomeTax))
	return lines
}
