package report

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/report/internal/store"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Kelompok arus kas metode langsung (RPT-08).
type flowGroup int

const (
	flowCustomers flowGroup = iota // penerimaan dari pelanggan
	flowSuppliers                  // pembayaran kepada pemasok dan karyawan
	flowTaxes                      // pembayaran pajak
	flowOtherOps                   // penerimaan dan pembayaran operasi lainnya
	flowInvesting
	flowFinancing
)

var operatingGroups = []struct {
	group flowGroup
	name  string
}{
	{flowCustomers, "Penerimaan dari pelanggan"},
	{flowSuppliers, "Pembayaran kepada pemasok dan karyawan"},
	{flowTaxes, "Penerimaan dan pembayaran pajak"},
	{flowOtherOps, "Penerimaan dan pembayaran operasi lainnya"},
}

// CashFlowDirect menyusun laporan arus kas metode langsung (RPT-08). Untuk setiap jurnal yang
// menyentuh rekening kas/bank terdaftar, setiap baris akun lainnya (kredit − debit) adalah kas yang
// diterima atau dibayar karena akun itu; karena jurnal seimbang, jumlahnya persis sama dengan
// perubahan kas, dan transfer antarrekening kas tidak muncul. Akun lawan dikelompokkan dengan aturan
// lancar/tidak lancar yang sama dengan metode tidak langsung, sehingga total investasi dan
// pendanaan kedua metode sama untuk transaksi kas biasa:
//
//	pelanggan : pendapatan usaha, piutang usaha dan akun sekelompoknya, uang muka pelanggan
//	pemasok   : beban pokok dan usaha, utang usaha dan akun sekelompoknya, uang muka pembelian,
//	            serta akun lancar lain yang terkait karyawan atau gaji
//	pajak     : akun sistem PPN dan PPh, pajak penghasilan, dan akun lancar bernama pajak/PPh/PPN
//	lainnya   : pendapatan dan beban lain-lain serta akun lancar lainnya
//	investasi : aset tidak lancar, serta laba/rugi pelepasan aset tetap
//	pendanaan : liabilitas jangka panjang dan ekuitas
func (s *service) CashFlowDirect(ctx context.Context, companyID uuid.UUID, p Period) (CashFlow, error) {
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
	cashIDs := make([]uuid.UUID, 0, len(cashList))
	isCash := map[uuid.UUID]bool{}
	for _, a := range cashList {
		cashIDs = append(cashIDs, a.AccountID)
		isCash[a.AccountID] = true
	}
	before, err := s.net(ctx, companyID, beginning, p.From.AddDays(-1), false)
	if err != nil {
		return CashFlow{}, err
	}
	moves, err := s.net(ctx, companyID, p.From, p.To, true)
	if err != nil {
		return CashFlow{}, err
	}
	rows, err := s.q(ctx).CashCounterparts(ctx, store.CashCounterpartsParams{CompanyID: companyID, FromDate: p.From, ToDate: p.To, CashAccounts: cashIDs})
	if err != nil {
		return CashFlow{}, err
	}
	classify, err := s.flowClassifier(ctx, companyID, c)
	if err != nil {
		return CashFlow{}, err
	}

	out := CashFlow{
		Statement: Statement{Title: "Laporan arus kas (metode langsung)", Company: name, Columns: []Column{{Label: periodLabel(p), From: p.From, To: p.To}}},
		NoCash:    len(cashList) == 0,
	}
	var cashMove money.Amount
	for id, v := range moves {
		if isCash[id] {
			cashMove = cashMove.Add(v)
		}
	}
	for id, v := range before {
		if isCash[id] {
			out.Opening = out.Opening.Add(v)
		}
	}

	byGroup := map[flowGroup][]Line{}
	var accounts []ledger.Account
	amount := map[uuid.UUID]money.Amount{}
	for _, r := range rows {
		if a, ok := c.byID[r.AccountID]; ok {
			accounts = append(accounts, a)
			amount[a.ID] = r.Amount
		}
	}
	sortByCode(accounts)
	for _, a := range accounts {
		id := a.ID
		g := classify(a)
		byGroup[g] = append(byGroup[g], Line{Kind: LineAccount, Code: a.Code, Name: a.Name, Depth: 1, AccountID: &id, Values: []money.Amount{amount[id]}})
	}
	sum := func(lines []Line) money.Amount {
		var t money.Amount
		for _, l := range lines {
			t = t.Add(l.Values[0])
		}
		return t
	}
	one := func(a money.Amount) []money.Amount { return []money.Amount{a} }
	st := &out.Statement

	st.Lines = append(st.Lines, Line{Kind: LineSection, Name: "Arus kas dari aktivitas operasi"})
	var operating money.Amount
	for _, og := range operatingGroups {
		lines := byGroup[og.group]
		if len(lines) == 0 {
			continue
		}
		total := sum(lines)
		operating = operating.Add(total)
		st.Lines = append(st.Lines, Line{Kind: LineGroup, Name: og.name, Values: one(total)})
		st.Lines = append(st.Lines, lines...)
	}
	st.Lines = append(st.Lines, Line{Kind: LineTotal, Name: "Kas bersih dari aktivitas operasi", Values: one(operating)})

	inv, fin := sum(byGroup[flowInvesting]), sum(byGroup[flowFinancing])
	st.Lines = append(st.Lines, Line{Kind: LineSection, Name: "Arus kas dari aktivitas investasi"})
	st.Lines = append(st.Lines, byGroup[flowInvesting]...)
	st.Lines = append(st.Lines, Line{Kind: LineTotal, Name: "Kas bersih dari aktivitas investasi", Values: one(inv)})
	st.Lines = append(st.Lines, Line{Kind: LineSection, Name: "Arus kas dari aktivitas pendanaan"})
	st.Lines = append(st.Lines, byGroup[flowFinancing]...)
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

// flowClassifier mengembalikan pengelompok akun lawan untuk arus kas metode langsung.
func (s *service) flowClassifier(ctx context.Context, companyID uuid.UUID, c Chart) (func(ledger.Account) flowGroup, error) {
	roleSet := func(roles ...ledger.Role) (map[uuid.UUID]bool, error) {
		out := map[uuid.UUID]bool{}
		for _, r := range roles {
			a, err := s.Ledger.RoleAccount(ctx, companyID, r)
			var p *httpx.Problem
			switch {
			case err == nil:
				out[a.ID] = true
			case errors.As(err, &p):
				// akun sistem belum dipilih: dikelompokkan menurut aturan umum
			default:
				return nil, err
			}
		}
		return out, nil
	}
	customers, err := roleSet(ledger.Receivable, ledger.RetentionReceivable, ledger.CustomerAdvance, ledger.ProjectAdvance)
	if err != nil {
		return nil, err
	}
	suppliers, err := roleSet(ledger.Payable, ledger.SupplierAdvance)
	if err != nil {
		return nil, err
	}
	taxes, err := roleSet(ledger.OutputVAT, ledger.OutputVATCollected, ledger.OutputVATOtherDPP, ledger.OutputVATRetail, ledger.InputVAT,
		ledger.PrepaidPPh23, ledger.WithholdingPPh23, ledger.WithholdingPPhFinal, ledger.WithholdingPPh26, ledger.FinalTaxExpense)
	if err != nil {
		return nil, err
	}
	investing, err := roleSet(ledger.AssetGain, ledger.AssetLoss)
	if err != nil {
		return nil, err
	}
	// Akun sekelompok (induk yang sama) dengan piutang usaha atau utang usaha, mis. jenis-jenis
	// piutang anggota yang dirinci di bagan akun.
	parents := func(set map[uuid.UUID]bool, class int) map[uuid.UUID]bool {
		out := map[uuid.UUID]bool{}
		for id := range set {
			if a, ok := c.byID[id]; ok && a.Class == class && a.ParentID != nil {
				out[*a.ParentID] = true
			}
		}
		return out
	}
	receivableParents, payableParents := parents(customers, 1), parents(suppliers, 2)
	has := func(a ledger.Account, words ...string) bool {
		n := strings.ToLower(a.Name)
		for _, w := range words {
			if strings.Contains(n, w) {
				return true
			}
		}
		return false
	}
	underOf := func(a ledger.Account, set map[uuid.UUID]bool) bool { return a.ParentID != nil && set[*a.ParentID] }

	return func(a ledger.Account) flowGroup {
		switch {
		case customers[a.ID]:
			return flowCustomers
		case suppliers[a.ID]:
			return flowSuppliers
		case taxes[a.ID]:
			return flowTaxes
		case investing[a.ID]:
			return flowInvesting
		}
		switch a.Class {
		case 4:
			return flowCustomers
		case 5, 6:
			return flowSuppliers
		case 7, 8:
			return flowOtherOps
		case 9:
			return flowTaxes
		case 3:
			return flowFinancing
		}
		if !c.current(a) {
			if a.Class == 1 {
				return flowInvesting
			}
			return flowFinancing
		}
		switch {
		case has(a, "pajak", "pph", "ppn"):
			return flowTaxes
		case has(a, "karyawan", "gaji", "upah"):
			return flowSuppliers
		case a.Class == 1 && underOf(a, receivableParents):
			return flowCustomers
		case a.Class == 2 && underOf(a, payableParents):
			return flowSuppliers
		}
		return flowOtherOps
	}, nil
}
