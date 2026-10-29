package report

import (
	"context"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/report/internal/store"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// EquityChanges adalah laporan perubahan ekuitas (RPT-08) untuk satu periode.
type EquityChanges struct {
	Statement
	Reconciled bool `json:"reconciled"` // saldo awal + semua perubahan = saldo akhir di setiap kolom
}

// EquityChanges menyusun laporan perubahan ekuitas dengan satu kolom per akun ekuitas (kelas 3) yang
// punya saldo atau mutasi, satu kolom laba (rugi) yang belum direklas ke laba ditahan, dan kolom
// jumlah, sehingga kolomnya mengikuti bagan akun setiap PT (mis. simpanan pokok, simpanan wajib,
// cadangan, dan SHU untuk koperasi). Saldo awal dan akhir memakai aturan neraca (position), jadi
// saldo akhir sama dengan ekuitas di neraca per tanggal akhir periode. Perubahannya:
//
//	saldo awal diinput : jurnal saldo awal (impor neraca saldo)
//	reklas laba        : jurnal penutup yang mulai berlaku di periode ini (laba → laba ditahan)
//	penambahan         : jurnal lain yang menambah ekuitas, mis. setoran modal atau simpanan
//	pengurangan        : jurnal lain yang mengurangi ekuitas, mis. prive, penarikan, pembagian SHU
//	laba (rugi)        : laba rugi periode ini, tanpa jurnal penutup
func (s *service) EquityChanges(ctx context.Context, companyID uuid.UUID, p Period) (EquityChanges, error) {
	if p.To.Before(p.From) {
		return EquityChanges{}, httpx.BadRequest("report.range", "Tanggal akhir periode sebelum tanggal awal.")
	}
	c, err := s.chart(ctx, companyID)
	if err != nil {
		return EquityChanges{}, err
	}
	start, err := s.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return EquityChanges{}, err
	}
	name, err := s.companyName(ctx, companyID)
	if err != nil {
		return EquityChanges{}, err
	}
	before := p.From.AddDays(-1)
	open, _, err := s.position(ctx, companyID, fiscalStart(start, before), before)
	if err != nil {
		return EquityChanges{}, err
	}
	end, _, err := s.position(ctx, companyID, fiscalStart(start, p.To), p.To)
	if err != nil {
		return EquityChanges{}, err
	}
	result, err := s.net(ctx, companyID, p.From, p.To, true)
	if err != nil {
		return EquityChanges{}, err
	}
	q := s.q(ctx)
	// Jurnal penutup yang masuk saldo akhir tetapi belum masuk saldo awal: reklas tahun buku yang
	// berakhir sebelum tahun buku tanggal akhir, dan tidak sebelum tahun buku tanggal awal.
	closingRows, err := q.ClosingNet(ctx, store.ClosingNetParams{
		CompanyID: companyID, FromDate: fiscalStart(start, before), BeforeDate: fiscalStart(start, p.To),
	})
	if err != nil {
		return EquityChanges{}, err
	}
	closing := map[uuid.UUID]money.Amount{}
	for _, r := range closingRows {
		closing[r.AccountID] = r.Net
	}
	moves, err := q.EquityMovements(ctx, store.EquityMovementsParams{CompanyID: companyID, FromDate: p.From, ToDate: p.To})
	if err != nil {
		return EquityChanges{}, err
	}
	opening, added, reduced := map[uuid.UUID]money.Amount{}, map[uuid.UUID]money.Amount{}, map[uuid.UUID]money.Amount{}
	for _, m := range moves {
		v := m.Net.Neg() // ekuitas bersaldo normal kredit
		switch {
		case m.SourceType == ledger.SourceOpening:
			opening[m.AccountID] = opening[m.AccountID].Add(v)
		case v.Sign() > 0:
			added[m.AccountID] = added[m.AccountID].Add(v)
		default:
			reduced[m.AccountID] = reduced[m.AccountID].Add(v)
		}
	}

	// Kolom: akun ekuitas yang punya angka, laba (rugi) belum direklas, dan jumlah.
	var accounts []ledger.Account
	for _, a := range c.byID {
		if a.Class != 3 || !a.Postable {
			continue
		}
		id := a.ID
		if open[id].IsZero() && end[id].IsZero() && closing[id].IsZero() && opening[id].IsZero() && added[id].IsZero() && reduced[id].IsZero() {
			continue
		}
		accounts = append(accounts, a)
	}
	sortByCode(accounts)
	out := EquityChanges{Statement: Statement{Title: "Laporan perubahan ekuitas", Company: name}}
	for _, a := range accounts {
		out.Columns = append(out.Columns, Column{Label: a.Code + " " + a.Name, From: p.From, To: p.To})
	}
	out.Columns = append(out.Columns,
		Column{Label: "Laba (rugi) belum direklas", From: p.From, To: p.To},
		Column{Label: "Jumlah ekuitas", From: p.From, To: p.To})

	pl := func(m map[uuid.UUID]money.Amount) money.Amount { return c.sumClass(m, 4, 5, 6, 7, 8, 9).Neg() }
	// row menyusun satu baris: nilai per akun ekuitas (menurut saldo normal kredit), laba, jumlah.
	row := func(perAccount func(uuid.UUID) money.Amount, profit money.Amount) []money.Amount {
		values := make([]money.Amount, 0, len(accounts)+2)
		var total money.Amount
		for _, a := range accounts {
			v := perAccount(a.ID)
			values = append(values, v)
			total = total.Add(v)
		}
		return append(values, profit, total.Add(profit))
	}
	nonZero := func(v []money.Amount) bool {
		for _, x := range v {
			if !x.IsZero() {
				return true
			}
		}
		return false
	}
	balanceOf := func(m map[uuid.UUID]money.Amount) func(uuid.UUID) money.Amount {
		return func(id uuid.UUID) money.Amount { return m[id].Neg() }
	}
	none := func(uuid.UUID) money.Amount { return money.Amount{} }

	first := row(balanceOf(open), pl(open))
	last := row(balanceOf(end), pl(end))
	steps := []struct {
		name   string
		values []money.Amount
	}{
		{"Saldo awal yang diinput", row(func(id uuid.UUID) money.Amount { return opening[id] }, money.Amount{})},
		{"Reklas laba (rugi) ke laba ditahan", row(balanceOf(closing), pl(closing))},
		{"Penambahan (setoran, alokasi, dan lainnya)", row(func(id uuid.UUID) money.Amount { return added[id] }, money.Amount{})},
		{"Pengurangan (penarikan, pembagian, dan lainnya)", row(func(id uuid.UUID) money.Amount { return reduced[id] }, money.Amount{})},
		{"Laba (rugi) periode ini", row(none, pl(result))},
	}

	out.Lines = append(out.Lines, Line{Kind: LineSubtotal, Name: "Saldo awal per " + before.Indonesian(), Values: first})
	sum := append([]money.Amount(nil), first...)
	for _, st := range steps {
		if !nonZero(st.values) && st.name != "Laba (rugi) periode ini" {
			continue
		}
		out.Lines = append(out.Lines, Line{Kind: LineComputed, Name: st.name, Values: st.values})
		for i, v := range st.values {
			sum[i] = sum[i].Add(v)
		}
	}
	out.Lines = append(out.Lines, Line{Kind: LineResult, Name: "Saldo akhir per " + p.To.Indonesian(), Values: last})
	out.Reconciled = true
	for i := range sum {
		if !sum[i].Equal(last[i]) {
			out.Reconciled = false
		}
	}
	return out, nil
}
