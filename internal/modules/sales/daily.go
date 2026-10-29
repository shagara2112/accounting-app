package sales

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/sales/internal/store"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

// Rekap penjualan harian retail (IND-07): penjualan eceran satu hari per toko dicatat sebagai
// satu dokumen dan satu jurnal. Harga sudah termasuk PPN; PPN-nya PPN digunggung (TAX-12),
// tanpa faktur pajak per pembeli. Jurnal: debit kas toko, bank, atau piutang kartu/QRIS;
// kredit pendapatan per akun dan PPN keluaran. Barang persediaan keluar dengan HPP rata-rata.
const (
	SourceDaily = "sales.daily"
	seriesDaily = "RPH"

	maxDailyLines = 2000
)

type DailyLineInput struct {
	ProductID   *uuid.UUID   `json:"productId"`
	AccountID   uuid.UUID    `json:"accountId"` // kosong = akun pendapatan produk
	Description string       `json:"description"`
	Qty         money.Amount `json:"qty"`
	Amount      money.Amount `json:"amount"` // nilai jual termasuk PPN
	TaxCode     string       `json:"taxCode"`
}

type DailyPaymentInput struct {
	AccountID uuid.UUID    `json:"accountId"`
	Amount    money.Amount `json:"amount"`
}

type DailySaleInput struct {
	Date     civil.Date          `json:"date"`
	Store    string              `json:"store"`
	Memo     string              `json:"memo"`
	Lines    []DailyLineInput    `json:"lines"`
	Payments []DailyPaymentInput `json:"payments"`
	BranchID *uuid.UUID          `json:"branchId"` // dimensi cabang (GL-09)
}

type DailySale struct {
	ID       uuid.UUID    `json:"id"`
	Number   string       `json:"number"`
	Status   string       `json:"status"`
	Date     civil.Date   `json:"date"`
	Store    string       `json:"store"`
	Memo     string       `json:"memo"`
	DPP      money.Amount `json:"dpp"`
	OtherDPP money.Amount `json:"otherDpp"`
	VAT      money.Amount `json:"vat"`
	Total    money.Amount `json:"total"`
	BranchID *uuid.UUID   `json:"branchId,omitempty"`
}

type DailySaleLine struct {
	ProductID   *uuid.UUID   `json:"productId,omitempty"`
	Description string       `json:"description"`
	AccountID   uuid.UUID    `json:"accountId"`
	AccountCode string       `json:"accountCode"`
	AccountName string       `json:"accountName"`
	Qty         money.Amount `json:"qty"`
	Amount      money.Amount `json:"amount"`
	TaxCode     string       `json:"taxCode"`
	Net         money.Amount `json:"net"`
	VAT         money.Amount `json:"vat"`
}

type DailySalePayment struct {
	AccountID   uuid.UUID    `json:"accountId"`
	AccountCode string       `json:"accountCode"`
	AccountName string       `json:"accountName"`
	Amount      money.Amount `json:"amount"`
}

type DailySaleDetail struct {
	DailySale
	Lines          []DailySaleLine    `json:"lines"`
	Payments       []DailySalePayment `json:"payments"`
	JournalEntryID uuid.UUID          `json:"journalEntryId"`
	CreatedAt      time.Time          `json:"createdAt"`
	CreatedBy      *string            `json:"createdBy,omitempty"`
	VoidedBy       *string            `json:"voidedBy,omitempty"`
	VoidedAt       *time.Time         `json:"voidedAt,omitempty"`
	VoidReason     *string            `json:"voidReason,omitempty"`
}

func (s *service) CreateDailySale(ctx context.Context, companyID uuid.UUID, in DailySaleInput, actor audit.Actor) (DailySaleDetail, error) {
	var out DailySaleDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		in.Store, in.Memo = strings.TrimSpace(in.Store), strings.TrimSpace(in.Memo)
		f := map[string]string{}
		switch {
		case in.Date.IsZero():
			f["date"] = "Tanggal penjualan wajib diisi."
		case in.Date.After(civil.Today(civil.Jakarta)):
			f["date"] = "Tanggal penjualan tidak boleh di masa depan."
		}
		if utf8.RuneCountInString(in.Store) > 100 {
			f["store"] = "Nama toko maksimal 100 karakter."
		}
		if utf8.RuneCountInString(in.Memo) > 500 {
			f["memo"] = "Catatan maksimal 500 karakter."
		}
		switch {
		case len(in.Lines) == 0:
			f["lines"] = "Isi minimal satu baris penjualan."
		case len(in.Lines) > maxDailyLines:
			f["lines"] = fmt.Sprintf("Maksimal %d baris per rekap.", maxDailyLines)
		}
		if len(in.Payments) == 0 {
			f["payments"] = "Isi rekening penerimaan."
		}
		if len(f) > 0 {
			return httpx.Unprocessable("daily.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		if prev, err := q.DailySaleOn(ctx, store.DailySaleOnParams{CompanyID: companyID, SaleDate: in.Date, Store: in.Store}); err == nil {
			msg := fmt.Sprintf("Rekap %s untuk tanggal %s sudah ada (%s). Void dulu bila ingin menggantinya.", storeLabel(in.Store), in.Date.Indonesian(), prev.Number)
			return httpx.Conflict("daily.exists", msg).WithFields(map[string]string{"date": msg})
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		company, err := s.Core.Company(ctx, companyID)
		if err != nil {
			return err
		}
		if err := rbac.CheckBranch(ctx, companyID, in.BranchID); err != nil {
			return err
		}
		if err := s.Core.CheckDimensions(ctx, companyID, in.BranchID, nil); err != nil {
			return err
		}
		table, err := s.Tax.Table(ctx)
		if err != nil {
			return err
		}
		accounts, err := s.accounts(ctx, companyID)
		if err != nil {
			return err
		}
		var productIDs []uuid.UUID
		for _, l := range in.Lines {
			if l.ProductID != nil {
				productIDs = append(productIDs, *l.ProductID)
			}
		}
		products, err := s.Inventory.ProductsByIDs(ctx, companyID, productIDs)
		if err != nil {
			return err
		}
		invAccounts, err := s.Inventory.InventoryAccounts(ctx, companyID)
		if err != nil {
			return err
		}

		type computed struct {
			DailyLineInput
			net, otherDPP, vat money.Amount
		}
		lines := make([]computed, len(in.Lines))
		var dpp, otherDPP, vat, total money.Amount
		for i, l := range in.Lines {
			key := fmt.Sprintf("lines.%d.", i)
			l.Description = strings.TrimSpace(l.Description)
			if l.ProductID != nil {
				p, ok := products[*l.ProductID]
				switch {
				case !ok:
					f[key+"productId"] = "Produk tidak ditemukan di PT ini."
					continue
				case !p.Active:
					f[key+"productId"] = fmt.Sprintf("Produk %s nonaktif.", p.Code)
					continue
				}
				if l.AccountID == uuid.Nil && p.RevenueAccountID != nil {
					l.AccountID = *p.RevenueAccountID
				}
				if l.Description == "" {
					l.Description = p.Name
				}
			}
			a, ok := accounts[l.AccountID]
			switch {
			case l.AccountID == uuid.Nil:
				f[key+"accountId"] = "Pilih akun pendapatan (atau isi akun pendapatan di produknya)."
			case !ok:
				f[key+"accountId"] = "Akun tidak ditemukan di PT ini."
			case !a.Postable || !a.Active:
				f[key+"accountId"] = fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name)
			case !revenueClasses[a.Class]:
				f[key+"accountId"] = fmt.Sprintf("%s %s bukan akun pendapatan; pilih akun berkepala 4 atau 7.", a.Code, a.Name)
			}
			if l.Qty.Sign() <= 0 {
				f[key+"qty"] = "Kuantitas harus lebih dari 0."
			}
			if l.Amount.Sign() <= 0 || !l.Amount.MulFrac(1, 1, 0, money.Down).Equal(l.Amount) {
				f[key+"amount"] = "Nilai penjualan harus rupiah bulat dan lebih dari 0."
			}
			if utf8.RuneCountInString(l.Description) > 300 {
				f[key+"description"] = "Keterangan maksimal 300 karakter."
			}
			code, err := table.Find(l.TaxCode, in.Date)
			switch {
			case err != nil:
				f[key+"taxCode"] = problemDetail(err)
				continue
			case code.Kind != tax.KindPPN:
				f[key+"taxCode"] = fmt.Sprintf("%s bukan kode PPN.", code.Code)
				continue
			case !company.PKP && code.RateBP > 0:
				f[key+"taxCode"] = "PT ini bukan PKP, sehingga tidak boleh memungut PPN. Pilih NON-PPN."
				continue
			}
			c := computed{DailyLineInput: l}
			c.net, c.otherDPP, c.vat = code.Inclusive(l.Amount)
			lines[i] = c
			dpp, otherDPP, vat, total = dpp.Add(c.net), otherDPP.Add(c.otherDPP), vat.Add(c.vat), total.Add(l.Amount)
		}
		// Rekening penerimaan: kas, bank, atau piutang kartu/QRIS (aset lancar, bukan persediaan).
		paid := map[uuid.UUID]money.Amount{}
		var order []uuid.UUID
		var paidTotal money.Amount
		for i, p := range in.Payments {
			key := fmt.Sprintf("payments.%d.", i)
			a, ok := accounts[p.AccountID]
			switch {
			case !ok:
				f[key+"accountId"] = "Akun tidak ditemukan di PT ini."
				continue
			case !a.Postable || !a.Active:
				f[key+"accountId"] = fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name)
				continue
			case a.Class != 1 || invAccounts[a.ID]:
				f[key+"accountId"] = fmt.Sprintf("%s %s bukan akun kas, bank, atau piutang penerimaan.", a.Code, a.Name)
				continue
			case p.Amount.Sign() <= 0:
				f[key+"amount"] = "Nominal harus lebih dari 0."
				continue
			}
			if _, ok := paid[p.AccountID]; !ok {
				order = append(order, p.AccountID)
			}
			paid[p.AccountID] = paid[p.AccountID].Add(p.Amount)
			paidTotal = paidTotal.Add(p.Amount)
		}
		if len(f) > 0 {
			return httpx.Unprocessable("daily.invalid", trade.FirstMessage(f)).WithFields(f)
		}
		if !paidTotal.Equal(total) {
			msg := fmt.Sprintf("Total penerimaan %s tidak sama dengan total penjualan %s.", paidTotal.IDR(), total.IDR())
			return httpx.Unprocessable("daily.unbalanced", msg).WithFields(map[string]string{"payments": msg})
		}

		number, err := s.Ledger.NextNumberFor(ctx, companyID, seriesDaily, in.Date, in.BranchID)
		if err != nil {
			return err
		}
		memo := "Penjualan harian " + storeLabel(in.Store) + " " + in.Date.Indonesian()
		if in.Memo != "" {
			memo += " · " + in.Memo
		}
		var drafts []ledger.LineDraft
		for _, id := range order {
			drafts = append(drafts, ledger.LineDraft{AccountID: id, Debit: paid[id], Memo: memo})
		}
		credits := map[uuid.UUID]int{}
		for _, l := range lines {
			if l.net.IsZero() {
				continue
			}
			i, ok := credits[l.AccountID]
			if !ok {
				credits[l.AccountID] = len(drafts)
				drafts = append(drafts, ledger.LineDraft{AccountID: l.AccountID, Memo: "Penjualan eceran " + number})
				i = len(drafts) - 1
			}
			drafts[i].Credit = drafts[i].Credit.Add(l.net)
		}
		if vat.Sign() > 0 {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OutputVATRetail)
			if err != nil {
				return err
			}
			drafts = append(drafts, ledger.LineDraft{AccountID: acc.ID, Credit: vat, Memo: "PPN digunggung " + number})
		}
		withDimensions(drafts, in.BranchID, nil)
		id := uuid.Must(uuid.NewV7())
		posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo,
			SourceType: SourceDaily, SourceID: &id, Number: number, Lines: drafts, Actor: actor,
		})
		if err != nil {
			return err
		}
		if err := q.InsertDailySale(ctx, store.InsertDailySaleParams{
			ID: id, CompanyID: companyID, Number: number, SaleDate: in.Date, Store: in.Store, Memo: in.Memo,
			Dpp: dpp, OtherDpp: otherDPP, Vat: vat, Total: total, JournalEntryID: posted.ID, CreatedBy: actor.UserRef(),
			BranchID: in.BranchID,
		}); err != nil {
			if db.IsUniqueViolation(err) {
				return httpx.Conflict("daily.exists", fmt.Sprintf("Rekap %s untuk tanggal %s sudah ada.", storeLabel(in.Store), in.Date.Indonesian()))
			}
			return err
		}
		var issues []inventory.Issue
		for i, l := range lines {
			if err := q.InsertDailySaleLine(ctx, store.InsertDailySaleLineParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, DailySaleID: id, LineNo: int16(i + 1),
				ProductID: l.ProductID, Description: l.Description, AccountID: l.AccountID, Qty: l.Qty,
				Amount: l.Amount, TaxCode: l.TaxCode, Net: l.net, OtherDpp: l.otherDPP, Vat: l.vat,
			}); err != nil {
				return err
			}
			if l.ProductID != nil && products[*l.ProductID].Goods() {
				issues = append(issues, inventory.Issue{ProductID: *l.ProductID, Qty: l.Qty, Memo: l.Description})
			}
		}
		for _, acc := range order {
			if err := q.InsertDailySalePayment(ctx, store.InsertDailySalePaymentParams{
				ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, DailySaleID: id, AccountID: acc, Amount: paid[acc],
			}); err != nil {
				return err
			}
		}
		if err := s.Inventory.IssueGoods(ctx, companyID, inventory.Doc{
			SourceType: SourceDaily, SourceID: id, Number: number, Date: in.Date, BranchID: in.BranchID,
		}, issues, actor); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.daily.post",
			ObjectType: "sales_daily", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "date": in.Date.String(), "store": in.Store, "total": total.String(), "vat": vat.String()},
		}); err != nil {
			return err
		}
		out, err = s.DailySale(ctx, companyID, id)
		return err
	})
	return out, err
}

func storeLabel(store string) string {
	if store == "" {
		return "toko"
	}
	return "toko " + store
}

func problemDetail(err error) string {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p.Detail
	}
	return err.Error()
}

// VoidDailySale membalik jurnal rekap pada tanggal void dan mengembalikan barangnya ke stok.
func (s *service) VoidDailySale(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (DailySaleDetail, error) {
	var out DailySaleDetail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		d, err := q.GetDailySaleForUpdate(ctx, store.GetDailySaleForUpdateParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("daily.not_found", "Rekap penjualan harian tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if d.Status == "void" {
			return httpx.Conflict("daily.already_void", fmt.Sprintf("Rekap %s sudah di-void.", d.Number))
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		v, err := s.Ledger.VoidSource(ctx, companyID, d.JournalEntryID, SourceDaily, date, reason, actor)
		if err != nil {
			return err
		}
		if err := s.Inventory.ReverseSource(ctx, companyID, SourceDaily, id, date, actor); err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if n, err := q.MarkDailySaleVoid(ctx, store.MarkDailySaleVoidParams{CompanyID: companyID, ID: id, VoidedBy: actor.UserRef(), VoidReason: &reason}); err != nil || n != 1 {
			return errOr(err, "rekap gagal ditandai void")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "sales.daily.void",
			ObjectType: "sales_daily", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": d.Number, "status": "posted"},
			After:  map[string]any{"number": d.Number, "status": "void", "reason": reason, "reversal": v.Reversal.Number},
		}); err != nil {
			return err
		}
		out, err = s.DailySale(ctx, companyID, id)
		return err
	})
	return out, err
}

func dailyOf(d store.SalesDailySale) DailySale {
	return DailySale{
		ID: d.ID, Number: d.Number, Status: d.Status, Date: d.SaleDate, Store: d.Store, Memo: d.Memo,
		DPP: d.Dpp, OtherDPP: d.OtherDpp, VAT: d.Vat, Total: d.Total, BranchID: d.BranchID,
	}
}

func (s *service) DailySales(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]DailySale, error) {
	rows, err := s.q(ctx).ListDailySales(ctx, store.ListDailySalesParams{CompanyID: companyID, FromDate: from, ToDate: to, Branches: rbac.BranchesFor(ctx, companyID)})
	if err != nil {
		return nil, err
	}
	out := make([]DailySale, len(rows))
	for i, r := range rows {
		out[i] = dailyOf(r)
	}
	return out, nil
}

func (s *service) DailySale(ctx context.Context, companyID, id uuid.UUID) (DailySaleDetail, error) {
	q := s.q(ctx)
	r, err := q.GetDailySale(ctx, store.GetDailySaleParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return DailySaleDetail{}, httpx.NotFound("daily.not_found", "Rekap penjualan harian tidak ditemukan.")
	}
	if err != nil {
		return DailySaleDetail{}, err
	}
	out := DailySaleDetail{
		DailySale: dailyOf(store.SalesDailySale{
			ID: r.ID, Number: r.Number, Status: r.Status, SaleDate: r.SaleDate, Store: r.Store, Memo: r.Memo,
			Dpp: r.Dpp, OtherDpp: r.OtherDpp, Vat: r.Vat, Total: r.Total, BranchID: r.BranchID,
		}),
		JournalEntryID: r.JournalEntryID, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedByName,
		VoidedBy: r.VoidedByName, VoidedAt: r.VoidedAt, VoidReason: r.VoidReason,
	}
	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return DailySaleDetail{}, err
	}
	lines, err := q.DailySaleLines(ctx, store.DailySaleLinesParams{CompanyID: companyID, DailySaleID: id})
	if err != nil {
		return DailySaleDetail{}, err
	}
	for _, l := range lines {
		a := accounts[l.AccountID]
		out.Lines = append(out.Lines, DailySaleLine{
			ProductID: l.ProductID, Description: l.Description, AccountID: l.AccountID, AccountCode: a.Code, AccountName: a.Name,
			Qty: l.Qty, Amount: l.Amount, TaxCode: l.TaxCode, Net: l.Net, VAT: l.Vat,
		})
	}
	pays, err := q.DailySalePayments(ctx, store.DailySalePaymentsParams{CompanyID: companyID, DailySaleID: id})
	if err != nil {
		return DailySaleDetail{}, err
	}
	for _, p := range pays {
		a := accounts[p.AccountID]
		out.Payments = append(out.Payments, DailySalePayment{AccountID: p.AccountID, AccountCode: a.Code, AccountName: a.Name, Amount: p.Amount})
	}
	return out, nil
}

// DailyTotals menjumlahkan rekap harian di rentang tanggal, untuk ringkasan penjualan.
func (s *service) dailyTotals(ctx context.Context, companyID uuid.UUID, from, to civil.Date) (store.DailyTotalsRow, error) {
	return s.q(ctx).DailyTotals(ctx, store.DailyTotalsParams{CompanyID: companyID, FromDate: from, ToDate: to})
}
