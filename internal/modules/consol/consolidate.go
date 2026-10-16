package consol

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/modules/report"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// ElimLine adalah satu baris jurnal eliminasi dalam akun grup.
type ElimLine struct {
	Code   string       `json:"code"`
	Name   string       `json:"name"`
	Debit  money.Amount `json:"debit"`
	Credit money.Amount `json:"credit"`
}

// Elimination adalah satu jurnal eliminasi antar-PT. Jurnal ini tidak pernah masuk buku besar
// PT mana pun; ia hanya ada di laporan konsolidasi.
type Elimination struct {
	Kind  string     `json:"kind"` // piutang_utang atau pendapatan_beban
	Memo  string     `json:"memo"`
	Lines []ElimLine `json:"lines"`
}

// Difference adalah saldo antar-PT yang tidak berpasangan dan perlu dibereskan.
type Difference struct {
	Kind         string       `json:"kind"`
	Seller       string       `json:"seller"`
	Buyer        string       `json:"buyer"`
	SellerAmount money.Amount `json:"sellerAmount"`
	BuyerAmount  money.Amount `json:"buyerAmount"`
	Difference   money.Amount `json:"difference"`
	Note         string       `json:"note"`
}

// Consolidation adalah laporan grup (ME-04): satu kolom per PT, kolom eliminasi, dan kolom
// konsolidasi = jumlah semua PT + eliminasi.
type Consolidation struct {
	Companies    []CompanyRef     `json:"companies"`
	AsOf         civil.Date       `json:"asOf"`
	From         civil.Date       `json:"from"`
	To           civil.Date       `json:"to"`
	BalanceSheet report.Statement `json:"balanceSheet"`
	ProfitLoss   report.Statement `json:"profitLoss"`
	Eliminations []Elimination    `json:"eliminations"`
	Differences  []Difference     `json:"differences"`
	Problems     []string         `json:"problems"`
}

// Consolidate menyusun neraca per tanggal asOf dan laba rugi periode p untuk PT yang dipilih.
// Pengguna harus punya izin consol.view di setiap PT itu. Pemetaan akun harus lengkap (setiap
// akun detail PT masuk ke akun grup detail berkelas sama); bila belum, laporan tidak disusun
// dan Problems berisi daftar yang harus dibereskan.
func (s *service) Consolidate(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, asOf civil.Date, p report.Period) (Consolidation, error) {
	if len(ids) == 0 {
		return Consolidation{}, httpx.BadRequest("consol.companies", "Pilih minimal satu PT.")
	}
	if p.To.Before(p.From) {
		return Consolidation{}, httpx.BadRequest("consol.range", "Tanggal akhir periode sebelum tanggal awal.")
	}
	visible, err := s.visibleCompanies(ctx, userID)
	if err != nil {
		return Consolidation{}, err
	}
	allowed := map[uuid.UUID]core.Company{}
	for _, c := range visible {
		allowed[c.ID] = c
	}
	var companies []core.Company
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		c, ok := allowed[id]
		if !ok {
			return Consolidation{}, httpx.Forbidden("consol.denied", "Anda tidak punya izin konsolidasi di salah satu PT yang dipilih.")
		}
		if !seen[id] {
			seen[id] = true
			companies = append(companies, c)
		}
	}
	sort.Slice(companies, func(i, j int) bool { return companies[i].Name < companies[j].Name })

	d, err := s.chartFor(ctx, companies)
	if err != nil {
		return Consolidation{}, err
	}
	out := Consolidation{
		Companies: d.Companies, AsOf: asOf, From: p.From, To: p.To, Problems: d.Problems,
		Eliminations: []Elimination{}, Differences: []Difference{},
	}
	if len(d.Problems) > 0 {
		return out, nil
	}

	chart := report.NewChart(d.ledgerChart())
	var bs, pl []map[uuid.UUID]money.Amount
	var prior, current []money.Amount
	for _, c := range companies {
		local := report.NewChart(d.accounts[c.ID])
		all, err := s.Report.Net(ctx, c.ID, civil.New(2000, 1, 1), asOf, false)
		if err != nil {
			return Consolidation{}, err
		}
		fs, err := s.Report.FiscalStart(ctx, c.ID, asOf)
		if err != nil {
			return Consolidation{}, err
		}
		year, err := s.Report.Net(ctx, c.ID, fs, asOf, false)
		if err != nil {
			return Consolidation{}, err
		}
		period, err := s.Report.Net(ctx, c.ID, p.From, p.To, true)
		if err != nil {
			return Consolidation{}, err
		}
		cur := local.SumClass(year, 4, 5, 6, 7, 8, 9).Neg()
		current = append(current, cur)
		prior = append(prior, local.SumClass(all, 4, 5, 6, 7, 8, 9).Neg().Sub(cur))
		bs = append(bs, d.mapValues(c.ID, all))
		pl = append(pl, d.mapValues(c.ID, period))
	}

	elimBS, elimPL := map[uuid.UUID]money.Amount{}, map[uuid.UUID]money.Amount{}
	if err := s.eliminate(ctx, &out, d, companies, asOf, p, elimBS, elimPL); err != nil {
		return Consolidation{}, err
	}

	sum := func(cols []map[uuid.UUID]money.Amount, elim map[uuid.UUID]money.Amount) map[uuid.UUID]money.Amount {
		t := map[uuid.UUID]money.Amount{}
		for _, c := range append(cols, elim) {
			for k, v := range c {
				t[k] = t[k].Add(v)
			}
		}
		return t
	}
	var total, zero money.Amount
	for _, c := range current {
		total = total.Add(c)
	}
	var totalPrior money.Amount
	for _, c := range prior {
		totalPrior = totalPrior.Add(c)
	}

	columns := func(label func(c core.Company) string, from, to civil.Date) []report.Column {
		var cols []report.Column
		for _, c := range companies {
			cols = append(cols, report.Column{Label: label(c), From: from, To: to})
		}
		return append(cols, report.Column{Label: "Eliminasi", From: from, To: to}, report.Column{Label: "Konsolidasi", From: from, To: to})
	}
	short := func(c core.Company) string { return c.ShortName }

	bsCols := append(append([]map[uuid.UUID]money.Amount{}, bs...), elimBS, sum(bs, elimBS))
	priorCols := append(append([]money.Amount{}, prior...), zero, totalPrior)
	currentCols := append(append([]money.Amount{}, current...), zero, total)
	lines, balanced := chart.BalanceSheetLines(bsCols, priorCols, currentCols)
	out.BalanceSheet = report.Statement{
		Title: "Laporan posisi keuangan konsolidasi", Company: "Konsolidasi grup", Columns: columns(short, civil.New(2000, 1, 1), asOf),
		Lines: lines, Balanced: balanced,
	}
	plCols := append(append([]map[uuid.UUID]money.Amount{}, pl...), elimPL, sum(pl, elimPL))
	out.ProfitLoss = report.Statement{
		Title: "Laporan laba rugi konsolidasi", Company: "Konsolidasi grup", Columns: columns(short, p.From, p.To),
		Lines: chart.ProfitLossLines(plCols),
	}
	return out, nil
}

// mapValues memindahkan mutasi akun PT ke akun grup.
func (d chartData) mapValues(companyID uuid.UUID, values map[uuid.UUID]money.Amount) map[uuid.UUID]money.Amount {
	out := map[uuid.UUID]money.Amount{}
	for id, v := range values {
		code, ok := d.mapping[companyID][id]
		if !ok {
			continue // akun header tidak pernah punya mutasi
		}
		g := groupID(code)
		out[g] = out[g].Add(v)
	}
	return out
}

// eliminate menyusun eliminasi untuk setiap pasangan PT yang saling bertaut: piutang penjual
// dengan utang pembeli per tanggal asOf, dan pendapatan penjual dengan beban pembeli dalam
// periode. Bagian yang berpasangan dieliminasi; selisihnya dicatat sebagai Difference.
func (s *service) eliminate(ctx context.Context, out *Consolidation, d chartData, companies []core.Company, asOf civil.Date, p report.Period,
	elimBS, elimPL map[uuid.UUID]money.Amount) error {
	name := func(code string) string {
		if g := d.byCode[code]; g != nil {
			return g.Name
		}
		return code
	}
	post := func(target map[uuid.UUID]money.Amount, e *Elimination, code string, debit, credit money.Amount) {
		target[groupID(code)] = target[groupID(code)].Add(debit).Sub(credit)
		e.Lines = append(e.Lines, ElimLine{Code: code, Name: name(code), Debit: debit, Credit: credit})
	}
	roleCode := func(companyID uuid.UUID, role ledger.Role) (string, error) {
		a, err := s.Ledger.RoleAccount(ctx, companyID, role)
		if err != nil {
			return "", err
		}
		return d.mapping[companyID][a.ID], nil
	}

	for _, seller := range companies {
		for _, buyer := range companies {
			if seller.ID == buyer.ID {
				continue
			}
			cs, errS := s.Core.LinkedContact(ctx, seller.ID, buyer.ID) // pelanggan di PT penjual
			cb, errB := s.Core.LinkedContact(ctx, buyer.ID, seller.ID) // pemasok di PT pembeli
			if errS != nil && errB != nil {
				continue
			}
			if errS != nil || errB != nil || !cs.IsCustomer || !cb.IsSupplier {
				// Hanya satu sisi yang bertaut; saldo penjual dari sisi ini tidak bisa dicocokkan.
				if errS == nil && cs.IsCustomer && errB != nil {
					out.Differences = append(out.Differences, Difference{
						Kind: "tautan", Seller: seller.ShortName, Buyer: buyer.ShortName,
						Note: fmt.Sprintf("Kontak %s di %s sudah ditautkan, tetapi %s belum punya kontak pemasok yang ditautkan ke %s.", cs.Name, seller.ShortName, buyer.ShortName, seller.ShortName),
					})
				}
				continue
			}

			// Piutang penjual dan utang pembeli per tanggal asOf.
			ar, err := s.Sales.Aging(ctx, seller.ID, asOf)
			if err != nil {
				return err
			}
			ap, err := s.Purchase.Aging(ctx, buyer.ID, asOf)
			if err != nil {
				return err
			}
			var receivable, payable money.Amount
			for _, r := range ar.Rows {
				if r.ContactID == cs.ID {
					receivable = r.Total
				}
			}
			for _, r := range ap.Rows {
				if r.ContactID == cb.ID {
					payable = r.Total
				}
			}
			if m := minAmount(receivable, payable); m.Sign() > 0 {
				recCode, err := roleCode(seller.ID, ledger.Receivable)
				if err != nil {
					return err
				}
				payCode, err := roleCode(buyer.ID, ledger.Payable)
				if err != nil {
					return err
				}
				e := Elimination{Kind: "piutang_utang", Memo: fmt.Sprintf("Piutang %s ke %s dengan utang %s ke %s", seller.ShortName, buyer.ShortName, buyer.ShortName, seller.ShortName)}
				post(elimBS, &e, payCode, m, money.Amount{})
				post(elimBS, &e, recCode, money.Amount{}, m)
				out.Eliminations = append(out.Eliminations, e)
			}
			if !receivable.Equal(payable) {
				out.Differences = append(out.Differences, Difference{
					Kind: "piutang_utang", Seller: seller.ShortName, Buyer: buyer.ShortName, SellerAmount: receivable, BuyerAmount: payable,
					Difference: receivable.Sub(payable),
					Note:       "Piutang dan utang antar-PT tidak sama. Biasanya karena PPh yang dipotong pembeli belum dilunasi, atau dokumen yang dicatat di satu sisi saja.",
				})
			}

			// Pendapatan penjual dan beban pembeli dalam periode.
			revenue, err := s.Sales.RevenueByAccount(ctx, seller.ID, cs.ID, p.From, p.To)
			if err != nil {
				return err
			}
			cost, err := s.Purchase.CostByAccount(ctx, buyer.ID, cb.ID, p.From, p.To)
			if err != nil {
				return err
			}
			rev := d.groupAmounts(seller.ID, revenue, nil)
			exp := d.groupAmounts(buyer.ID, cost, func(class int) bool { return class >= 5 })
			capitalized := d.groupAmounts(buyer.ID, cost, func(class int) bool { return class <= 3 })
			R, C, A := total(rev), total(exp), total(capitalized)
			if m := minAmount(R, C); m.Sign() > 0 {
				e := Elimination{Kind: "pendapatan_beban", Memo: fmt.Sprintf("Penjualan %s ke %s dengan pembelian %s dari %s", seller.ShortName, buyer.ShortName, buyer.ShortName, seller.ShortName)}
				allocate(rev, m, func(code string, a money.Amount) { post(elimPL, &e, code, a, money.Amount{}) })
				allocate(exp, m, func(code string, a money.Amount) { post(elimPL, &e, code, money.Amount{}, a) })
				out.Eliminations = append(out.Eliminations, e)
			}
			if !R.Equal(C) {
				note := "Pendapatan penjual dan beban pembeli antar-PT tidak sama; periksa dokumen yang dicatat di satu sisi saja."
				if A.Sign() > 0 {
					note = fmt.Sprintf("Pembelian senilai %s dicatat %s sebagai aset (mis. persediaan atau aset tetap); laba antar-PT di dalamnya belum dieliminasi.", A.IDR(), buyer.ShortName)
				}
				out.Differences = append(out.Differences, Difference{
					Kind: "pendapatan_beban", Seller: seller.ShortName, Buyer: buyer.ShortName, SellerAmount: R, BuyerAmount: C,
					Difference: R.Sub(C), Note: note,
				})
			}
		}
	}
	return nil
}

type codeAmount struct {
	code   string
	amount money.Amount
}

// groupAmounts memetakan jumlah per akun PT ke akun grup, opsional hanya kelas tertentu.
func (d chartData) groupAmounts(companyID uuid.UUID, values map[uuid.UUID]money.Amount, keep func(class int) bool) []codeAmount {
	sums := map[string]money.Amount{}
	for id, v := range values {
		code, ok := d.mapping[companyID][id]
		if !ok {
			continue
		}
		if keep != nil && !keep(d.byCode[code].Class) {
			continue
		}
		sums[code] = sums[code].Add(v)
	}
	out := make([]codeAmount, 0, len(sums))
	for code, a := range sums {
		if !a.IsZero() {
			out = append(out, codeAmount{code, a})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].code < out[j].code })
	return out
}

func total(list []codeAmount) money.Amount {
	var t money.Amount
	for _, c := range list {
		t = t.Add(c.amount)
	}
	return t
}

// allocate membagi m ke akun-akun urut kode sampai habis.
func allocate(list []codeAmount, m money.Amount, fn func(code string, a money.Amount)) {
	for _, c := range list {
		if m.Sign() <= 0 {
			return
		}
		a := minAmount(c.amount, m)
		fn(c.code, a)
		m = m.Sub(a)
	}
}

func minAmount(a, b money.Amount) money.Amount {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}
