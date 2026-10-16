// Package coretax adalah bagian modul pajak yang mengurus faktur pajak keluaran: kode
// transaksi (TAX-04), ekspor XML untuk diunggah manual ke Coretax dan pencatatan nomor faktur
// yang terbit (TAX-03), rekap PPN per masa (TAX-06), dan rekap PPh yang dipotong.
// Data faktur dibaca lewat service penjualan; modul ini hanya menyimpan status faktur pajak.
package coretax

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/modules/core"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/tax/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Status faktur pajak keluaran.
const (
	Ready     = "ready"     // siap diekspor
	Exported  = "exported"  // sudah masuk file XML, menunggu terbit di Coretax
	Approved  = "approved"  // terbit; nomor faktur pajak tercatat
	Cancelled = "cancelled" // dibatalkan di Coretax
	Replaced  = "replaced"  // diganti faktur pajak pengganti
)

// TrxCodes adalah kode transaksi faktur pajak (TAX-04).
var TrxCodes = []struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}{
	{"01", "01 · Kepada pihak lain (bukan pemungut PPN)"},
	{"02", "02 · Kepada pemungut instansi pemerintah"},
	{"03", "03 · Kepada pemungut selain instansi pemerintah"},
	{"04", "04 · DPP nilai lain"},
	{"05", "05 · Besaran tertentu"},
	{"06", "06 · Penyerahan lainnya (mis. turis asing)"},
	{"07", "07 · PPN tidak dipungut atau ditanggung pemerintah"},
	{"08", "08 · Dibebaskan dari PPN"},
	{"09", "09 · Penyerahan aktiva (Pasal 16D)"},
	{"10", "10 · Penyerahan lainnya"},
}

func validTrxCode(c string) bool {
	for _, t := range TrxCodes {
		if t.Code == c {
			return true
		}
	}
	return false
}

// OutputInvoice adalah satu faktur penjualan ber-PPN beserta status faktur pajaknya.
type OutputInvoice struct {
	InvoiceID        uuid.UUID    `json:"invoiceId"`
	Number           string       `json:"number"`
	Date             civil.Date   `json:"date"`
	InvoiceStatus    string       `json:"invoiceStatus"` // posted atau void (penjualan)
	BuyerID          uuid.UUID    `json:"buyerId"`
	BuyerName        string       `json:"buyerName"`
	BuyerNPWP        string       `json:"buyerNpwp"`
	DPP              money.Amount `json:"dpp"`
	OtherDPP         money.Amount `json:"otherDpp"`
	VAT              money.Amount `json:"vat"`
	TrxCode          string       `json:"trxCode"`
	Status           string       `json:"status"`
	TaxInvoiceNumber *string      `json:"taxInvoiceNumber,omitempty"`
	ApprovedDate     *civil.Date  `json:"approvedDate,omitempty"`
	ReplacedBy       *uuid.UUID   `json:"replacedBy,omitempty"`
	Note             string       `json:"note"`
	Problems         []string     `json:"problems"`
}

type OutputList struct {
	Month    string          `json:"month"`
	PKP      bool            `json:"pkp"`
	Problems []string        `json:"problems"` // masalah tingkat PT, mis. NPWP PT kosong
	Items    []OutputInvoice `json:"items"`
}

type Settings struct {
	GoodsOpt  string `json:"goodsOpt"`
	GoodsCode string `json:"goodsCode"`
	UnitCode  string `json:"unitCode"`
}

type Batch struct {
	ID           uuid.UUID `json:"id"`
	Period       string    `json:"period"`
	Format       string    `json:"format"`
	InvoiceCount int       `json:"invoiceCount"`
	FileName     string    `json:"fileName"`
	CreatedAt    time.Time `json:"createdAt"`
	CreatedBy    *string   `json:"createdBy,omitempty"`
	Content      []byte    `json:"-"`
}

type ImportResult struct {
	Recorded []string `json:"recorded"` // nomor faktur Lajur yang tercatat
	Skipped  []string `json:"skipped"`  // baris yang dilewati beserta alasannya
}

type Service interface {
	Settings(ctx context.Context, companyID uuid.UUID) (Settings, error)
	SetSettings(ctx context.Context, companyID uuid.UUID, s Settings, actor audit.Actor) (Settings, error)
	Outputs(ctx context.Context, companyID uuid.UUID, month civil.Date) (OutputList, error)
	SetTrxCode(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID, code string, actor audit.Actor) error
	Export(ctx context.Context, companyID uuid.UUID, month civil.Date, ids []uuid.UUID, actor audit.Actor) (Batch, error)
	Batches(ctx context.Context, companyID uuid.UUID, month civil.Date) ([]Batch, error)
	Batch(ctx context.Context, companyID, id uuid.UUID) (Batch, error)
	Approve(ctx context.Context, companyID, invoiceID uuid.UUID, number string, date civil.Date, actor audit.Actor) error
	Cancel(ctx context.Context, companyID, invoiceID uuid.UUID, note string, actor audit.Actor) error
	Replace(ctx context.Context, companyID, invoiceID, replacementID uuid.UUID, note string, actor audit.Actor) error
	ImportNumbers(ctx context.Context, companyID uuid.UUID, csv string, actor audit.Actor) (ImportResult, error)
	// CanVoidInvoice menolak void faktur penjualan yang faktur pajaknya sudah terbit (PRD: faktur
	// yang disetujui Coretax terkunci; koreksi lewat pembatalan atau penggantian).
	CanVoidInvoice(ctx context.Context, companyID, invoiceID uuid.UUID) error

	VATRecap(ctx context.Context, companyID uuid.UUID, year int) (VATRecap, error)
	Withholding(ctx context.Context, companyID uuid.UUID, month civil.Date) (WithholdingRecap, error)
	// Digunggung adalah rekap PPN penjualan eceran satu masa (TAX-12).
	Digunggung(ctx context.Context, companyID uuid.UUID, month civil.Date) (Digunggung, error)
}

type Deps struct {
	Pool     *pgxpool.Pool
	Core     core.Service
	Tax      tax.Service
	Sales    sales.Service
	Purchase purchase.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

// defaultSettings: PT jasa dan konstruksi menyerahkan jasa (B), retail menyerahkan barang (A).
func defaultSettings(industry *string) Settings {
	opt := "B"
	if industry != nil && *industry == "retail" {
		opt = "A"
	}
	return Settings{GoodsOpt: opt, GoodsCode: "000000", UnitCode: "UM.0021"}
}

func (s *service) Settings(ctx context.Context, companyID uuid.UUID) (Settings, error) {
	r, err := s.q(ctx).GetTaxSettings(ctx, companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		c, err := s.Core.Company(ctx, companyID)
		if err != nil {
			return Settings{}, err
		}
		return defaultSettings(c.Industry), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return Settings{GoodsOpt: r.GoodsOpt, GoodsCode: r.GoodsCode, UnitCode: r.UnitCode}, nil
}

var (
	goodsCodePattern = regexp.MustCompile(`^[0-9]{6}$`)
	unitCodePattern  = regexp.MustCompile(`^UM\.[0-9]{4}$`)
	nsfpPattern      = regexp.MustCompile(`^[0-9]{13,20}$`)
	nonDigits        = regexp.MustCompile(`[^0-9]`)
)

func (s *service) SetSettings(ctx context.Context, companyID uuid.UUID, in Settings, actor audit.Actor) (Settings, error) {
	in.GoodsCode, in.UnitCode = strings.TrimSpace(in.GoodsCode), strings.ToUpper(strings.TrimSpace(in.UnitCode))
	f := map[string]string{}
	if in.GoodsOpt != "A" && in.GoodsOpt != "B" {
		f["goodsOpt"] = "Pilih barang (A) atau jasa (B)."
	}
	if !goodsCodePattern.MatchString(in.GoodsCode) {
		f["goodsCode"] = "Kode barang/jasa 6 digit; 000000 bila tidak dipakai."
	}
	if !unitCodePattern.MatchString(in.UnitCode) {
		f["unitCode"] = "Kode satuan dari referensi DJP, mis. UM.0021."
	}
	if len(f) > 0 {
		return Settings{}, httpx.Unprocessable("tax.settings_invalid", firstOf(f)).WithFields(f)
	}
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if _, err := s.q(ctx).UpsertTaxSettings(ctx, store.UpsertTaxSettingsParams{
			CompanyID: companyID, GoodsOpt: in.GoodsOpt, GoodsCode: in.GoodsCode, UnitCode: in.UnitCode, UpdatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		_, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.settings.update", ObjectType: "company",
			ObjectID: companyID.String(), IP: actor.IP,
			After: map[string]any{"goodsOpt": in.GoodsOpt, "goodsCode": in.GoodsCode, "unitCode": in.UnitCode},
		})
		return err
	})
	return in, err
}

// defaultTrxCode: 04 bila DPP nilai lain dipakai (PPN 12% non-mewah sejak 2025), selain itu 01.
func defaultTrxCode(inv sales.Invoice) string {
	if !inv.OtherDPP.Equal(inv.DPP) {
		return "04"
	}
	return "01"
}

// Outputs mengembalikan faktur penjualan ber-PPN pada masa month beserta status faktur pajaknya.
// Faktur yang sudah di-void tetap tampil bila faktur pajaknya pernah diekspor atau terbit.
func (s *service) Outputs(ctx context.Context, companyID uuid.UUID, month civil.Date) (OutputList, error) {
	month = month.MonthStart()
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return OutputList{}, err
	}
	out := OutputList{Month: month.MonthKey(), PKP: company.PKP, Problems: []string{}, Items: []OutputInvoice{}}
	if !company.PKP {
		out.Problems = append(out.Problems, "PT ini bukan PKP, sehingga tidak menerbitkan faktur pajak.")
		return out, nil
	}
	if company.NPWP == "" {
		out.Problems = append(out.Problems, "NPWP PT belum diisi; lengkapi di Pengaturan sebelum mengekspor.")
	}
	invs, err := s.Sales.Invoices(ctx, companyID, sales.InvoiceFilter{From: month, To: month.MonthEnd()})
	if err != nil {
		return OutputList{}, err
	}
	ids := make([]uuid.UUID, 0, len(invs))
	for _, inv := range invs {
		ids = append(ids, inv.ID)
	}
	states, err := s.states(ctx, companyID, ids)
	if err != nil {
		return OutputList{}, err
	}
	buyers := map[uuid.UUID]core.Contact{}
	for _, inv := range invs {
		st, has := states[inv.ID]
		if inv.Status == "draft" || (!has && (inv.Status != "posted" || inv.VAT.IsZero())) {
			continue
		}
		b, ok := buyers[inv.CustomerID]
		if !ok {
			if b, err = s.Core.Contact(ctx, companyID, inv.CustomerID); err != nil {
				return OutputList{}, err
			}
			buyers[inv.CustomerID] = b
		}
		item := OutputInvoice{
			InvoiceID: inv.ID, Number: deref(inv.Number), Date: inv.Date, InvoiceStatus: inv.Status, BuyerID: b.ID,
			BuyerName: b.Name, BuyerNPWP: buyerTIN(b), DPP: inv.DPP, OtherDPP: inv.OtherDPP, VAT: inv.VAT,
			TrxCode: defaultTrxCode(inv), Status: Ready, Problems: []string{},
		}
		if has {
			item.TrxCode, item.Status, item.TaxInvoiceNumber, item.Note = st.TrxCode, st.Status, st.TaxInvoiceNumber, st.Note
			item.ApprovedDate, item.ReplacedBy = st.ApprovedDate, st.ReplacedBy
		}
		if item.BuyerNPWP == "" {
			item.Problems = append(item.Problems, "Pembeli belum punya NPWP atau NIK.")
		}
		if inv.Status == "void" && (item.Status == Ready || item.Status == Exported) {
			item.Problems = append(item.Problems, "Faktur penjualan sudah di-void; jangan diunggah.")
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// buyerTIN: NPWP, atau NIK yang berfungsi sebagai NPWP orang pribadi.
func buyerTIN(c core.Contact) string {
	if c.NPWP != "" {
		return c.NPWP
	}
	return c.NIK
}

func (s *service) states(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]store.TaxOutputInvoice, error) {
	rows, err := s.q(ctx).OutputInvoiceStates(ctx, store.OutputInvoiceStatesParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]store.TaxOutputInvoice, len(rows))
	for _, r := range rows {
		m[r.InvoiceID] = r
	}
	return m, nil
}

// current mengembalikan status tersimpan atau status awal faktur yang belum pernah disentuh.
// Baris dikunci FOR UPDATE supaya dua perubahan status tidak saling menimpa.
func (s *service) current(ctx context.Context, companyID uuid.UUID, inv sales.InvoiceDetail) (store.TaxOutputInvoice, error) {
	r, err := s.q(ctx).GetOutputInvoiceForUpdate(ctx, store.GetOutputInvoiceForUpdateParams{CompanyID: companyID, InvoiceID: inv.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.TaxOutputInvoice{InvoiceID: inv.ID, CompanyID: companyID, TrxCode: defaultTrxCode(inv.Invoice), Status: Ready}, nil
	}
	return r, err
}

func (s *service) save(ctx context.Context, r store.TaxOutputInvoice, actor audit.Actor, action string, after map[string]any) error {
	if _, err := s.q(ctx).UpsertOutputInvoice(ctx, store.UpsertOutputInvoiceParams{
		InvoiceID: r.InvoiceID, CompanyID: r.CompanyID, TrxCode: r.TrxCode, Status: r.Status,
		TaxInvoiceNumber: r.TaxInvoiceNumber, ApprovedDate: r.ApprovedDate, LastBatchID: r.LastBatchID,
		ReplacedBy: r.ReplacedBy, Note: r.Note, UpdatedBy: actor.UserRef(),
	}); err != nil {
		return err
	}
	_, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &r.CompanyID, Action: action, ObjectType: "tax_output_invoice",
		ObjectID: r.InvoiceID.String(), IP: actor.IP, After: after,
	})
	return err
}

// invoice memastikan faktur penjualan milik PT ini, diposting, dan ber-PPN.
func (s *service) invoice(ctx context.Context, companyID, id uuid.UUID) (sales.InvoiceDetail, error) {
	inv, err := s.Sales.Invoice(ctx, companyID, id)
	if err != nil {
		return inv, err
	}
	if inv.Status == "draft" || inv.Number == nil {
		return inv, httpx.Unprocessable("tax.not_posted", "Faktur draf belum punya faktur pajak; posting dulu.")
	}
	return inv, nil
}

func (s *service) SetTrxCode(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID, code string, actor audit.Actor) error {
	if !validTrxCode(code) {
		return httpx.Unprocessable("tax.trx_code", "Kode transaksi harus 01–10.")
	}
	if len(ids) == 0 || len(ids) > 1000 {
		return httpx.Unprocessable("tax.selection", "Pilih 1–1.000 faktur.")
	}
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		for _, id := range ids {
			inv, err := s.invoice(ctx, companyID, id)
			if err != nil {
				return err
			}
			r, err := s.current(ctx, companyID, inv)
			if err != nil {
				return err
			}
			if r.Status != Ready && r.Status != Exported {
				return httpx.Conflict("tax.locked", fmt.Sprintf("Faktur pajak %s sudah terbit atau dibatalkan; kode transaksi tidak bisa diubah.", deref(inv.Number)))
			}
			r.TrxCode = code
			if err := s.save(ctx, r, actor, "tax.output.trx_code", map[string]any{"number": deref(inv.Number), "trxCode": code}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Export menyusun XML Coretax untuk faktur terpilih dan menandainya "diekspor". Ekspor ulang
// faktur yang sama diperbolehkan (mis. file pertama hilang), tetapi faktur yang sudah terbit tidak.
func (s *service) Export(ctx context.Context, companyID uuid.UUID, month civil.Date, ids []uuid.UUID, actor audit.Actor) (Batch, error) {
	month = month.MonthStart()
	if len(ids) == 0 || len(ids) > 1000 {
		return Batch{}, httpx.Unprocessable("tax.selection", "Pilih 1–1.000 faktur; Coretax menerima maksimal 1.000 faktur per file.")
	}
	var out Batch
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		company, err := s.Core.Company(ctx, companyID)
		if err != nil {
			return err
		}
		if !company.PKP || company.NPWP == "" {
			return httpx.Unprocessable("tax.company_profile", "Lengkapi NPWP PT dan pastikan PT berstatus PKP sebelum mengekspor.")
		}
		settings, err := s.Settings(ctx, companyID)
		if err != nil {
			return err
		}
		table, err := s.Tax.Table(ctx)
		if err != nil {
			return err
		}
		var invoices []XMLInvoice
		var rows []store.TaxOutputInvoice
		var numbers []string
		problems := map[string]string{}
		for _, id := range ids {
			inv, err := s.invoice(ctx, companyID, id)
			if err != nil {
				return err
			}
			number := deref(inv.Number)
			if !inv.Date.SameMonth(month) {
				problems[number] = "bukan masa " + month.MonthLabel()
				continue
			}
			if inv.Status != "posted" {
				problems[number] = "faktur penjualan sudah di-void"
				continue
			}
			r, err := s.current(ctx, companyID, inv)
			if err != nil {
				return err
			}
			if r.Status != Ready && r.Status != Exported {
				problems[number] = "faktur pajak sudah terbit atau dibatalkan"
				continue
			}
			buyer, err := s.Core.Contact(ctx, companyID, inv.CustomerID)
			if err != nil {
				return err
			}
			if buyerTIN(buyer) == "" {
				problems[number] = "pembeli belum punya NPWP atau NIK"
				continue
			}
			x := XMLInvoice{
				Date: inv.Date, TrxCode: r.TrxCode, Reference: number,
				Buyer: XMLBuyer{Name: buyer.Name, NPWP: buyerTIN(buyer), NITKU: buyer.NITKU, Address: buyer.Address, Email: buyer.Email},
			}
			ratios := map[string]bool{}
			for _, l := range inv.Lines {
				code, err := table.Find(l.TaxCode, inv.Date)
				if err != nil {
					return err
				}
				ratios[fmt.Sprintf("%d/%d", code.DPPNum, code.DPPDen)] = true
				name := strings.TrimSpace(l.Description)
				if name == "" {
					name = l.AccountName
				}
				x.Lines = append(x.Lines, XMLLine{
					Opt: settings.GoodsOpt, Code: settings.GoodsCode, Name: name, Unit: settings.UnitCode,
					Price: l.UnitPrice, Qty: l.Qty, Discount: l.Discount, TaxBase: l.Net, OtherDPP: l.OtherDPP,
					RateBP: code.RateBP, VAT: l.VAT,
				})
			}
			if len(ratios) > 1 {
				problems[number] = "baris memakai skema DPP berbeda (DPP nilai lain dan DPP penuh); pisahkan menjadi dua faktur"
				continue
			}
			invoices = append(invoices, x)
			r.Status = Exported
			rows = append(rows, r)
			numbers = append(numbers, number)
		}
		if len(problems) > 0 {
			keys := make([]string, 0, len(problems))
			for n := range problems {
				keys = append(keys, n)
			}
			sort.Strings(keys)
			var parts []string
			for _, n := range keys {
				parts = append(parts, n+": "+problems[n])
			}
			f := problems
			return httpx.Unprocessable("tax.export_blocked", "Belum bisa diekspor — "+strings.Join(parts, "; ")+".").WithFields(f)
		}

		content, err := EncodeV1(XMLSeller{NPWP: company.NPWP, NITKU: company.NITKU}, invoices)
		if err != nil {
			return err
		}
		batchID := uuid.Must(uuid.NewV7())
		out = Batch{
			ID: batchID, Period: month.MonthKey(), Format: FormatV1, InvoiceCount: len(invoices), Content: content,
			FileName:  fmt.Sprintf("faktur-keluaran-%s-%s-%s.xml", strings.ToLower(company.ShortName), month.MonthKey(), batchID.String()[len(batchID.String())-6:]),
			CreatedAt: time.Now(),
		}
		if err := s.q(ctx).InsertExportBatch(ctx, store.InsertExportBatchParams{
			ID: batchID, CompanyID: companyID, Period: month, Format: FormatV1, InvoiceCount: int32(len(invoices)),
			FileName: out.FileName, Content: content, CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		for _, r := range rows {
			r.LastBatchID = &batchID
			if _, err := s.q(ctx).UpsertOutputInvoice(ctx, store.UpsertOutputInvoiceParams{
				InvoiceID: r.InvoiceID, CompanyID: r.CompanyID, TrxCode: r.TrxCode, Status: r.Status,
				TaxInvoiceNumber: r.TaxInvoiceNumber, ApprovedDate: r.ApprovedDate, LastBatchID: r.LastBatchID,
				ReplacedBy: r.ReplacedBy, Note: r.Note, UpdatedBy: actor.UserRef(),
			}); err != nil {
				return err
			}
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.output.export", ObjectType: "tax_export_batch",
			ObjectID: batchID.String(), IP: actor.IP,
			After: map[string]any{"period": month.MonthKey(), "invoices": numbers, "format": FormatV1},
		})
		return err
	})
	return out, err
}

func (s *service) Batches(ctx context.Context, companyID uuid.UUID, month civil.Date) ([]Batch, error) {
	rows, err := s.q(ctx).ListExportBatches(ctx, store.ListExportBatchesParams{CompanyID: companyID, Period: month.MonthStart()})
	if err != nil {
		return nil, err
	}
	out := make([]Batch, len(rows))
	for i, r := range rows {
		out[i] = Batch{ID: r.ID, Period: r.Period.MonthKey(), Format: r.Format, InvoiceCount: int(r.InvoiceCount),
			FileName: r.FileName, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedByName}
	}
	return out, nil
}

func (s *service) Batch(ctx context.Context, companyID, id uuid.UUID) (Batch, error) {
	r, err := s.q(ctx).GetExportBatch(ctx, store.GetExportBatchParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Batch{}, httpx.NotFound("tax.batch_not_found", "File ekspor tidak ditemukan.")
	}
	if err != nil {
		return Batch{}, err
	}
	return Batch{ID: r.ID, Period: r.Period.MonthKey(), Format: r.Format, InvoiceCount: int(r.InvoiceCount),
		FileName: r.FileName, CreatedAt: r.CreatedAt, Content: r.Content}, nil
}

// Approve mencatat nomor faktur pajak yang terbit di Coretax (TAX-03).
func (s *service) Approve(ctx context.Context, companyID, invoiceID uuid.UUID, number string, date civil.Date, actor audit.Actor) error {
	number = nonDigits.ReplaceAllString(number, "")
	if !nsfpPattern.MatchString(number) {
		msg := "Nomor faktur pajak berupa 13–20 digit angka, sesuai yang tertera di Coretax."
		return httpx.Unprocessable("tax.nsfp", msg).WithFields(map[string]string{"number": msg})
	}
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		inv, err := s.invoice(ctx, companyID, invoiceID)
		if err != nil {
			return err
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		if date.Before(inv.Date) {
			msg := "Tanggal terbit tidak boleh sebelum tanggal faktur."
			return httpx.Unprocessable("tax.nsfp", msg).WithFields(map[string]string{"date": msg})
		}
		r, err := s.current(ctx, companyID, inv)
		if err != nil {
			return err
		}
		if r.Status != Ready && r.Status != Exported {
			return httpx.Conflict("tax.locked", fmt.Sprintf("Faktur pajak %s sudah tercatat (%s).", deref(inv.Number), deref(r.TaxInvoiceNumber)))
		}
		if inv.Status != "posted" {
			return httpx.Unprocessable("tax.invoice_void", "Faktur penjualan sudah di-void; nomor faktur pajak tidak bisa dicatat.")
		}
		r.Status, r.TaxInvoiceNumber, r.ApprovedDate = Approved, &number, &date
		err = s.save(ctx, r, actor, "tax.output.approve", map[string]any{"number": deref(inv.Number), "taxInvoiceNumber": number, "date": date.String()})
		if isUniqueViolation(err) {
			msg := "Nomor faktur pajak ini sudah tercatat di faktur lain."
			return httpx.Conflict("tax.nsfp_taken", msg).WithFields(map[string]string{"number": msg})
		}
		return err
	})
}

// Cancel mencatat bahwa faktur pajak sudah dibatalkan di Coretax; setelah itu faktur
// penjualannya boleh di-void.
func (s *service) Cancel(ctx context.Context, companyID, invoiceID uuid.UUID, note string, actor audit.Actor) error {
	note = strings.TrimSpace(note)
	if n := utf8.RuneCountInString(note); n < 5 || n > 300 {
		msg := "Tulis alasan pembatalan (5–300 karakter)."
		return httpx.Unprocessable("tax.note", msg).WithFields(map[string]string{"note": msg})
	}
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		inv, err := s.invoice(ctx, companyID, invoiceID)
		if err != nil {
			return err
		}
		r, err := s.current(ctx, companyID, inv)
		if err != nil {
			return err
		}
		switch r.Status {
		case Ready, Exported:
			// Belum terbit: cukup kembali ke status siap, tanpa nomor.
			r.Status, r.LastBatchID = Ready, nil
		case Approved:
			r.Status = Cancelled
		default:
			return httpx.Conflict("tax.locked", "Faktur pajak ini sudah dibatalkan atau diganti.")
		}
		r.Note = note
		return s.save(ctx, r, actor, "tax.output.cancel", map[string]any{"number": deref(inv.Number), "status": r.Status, "note": note})
	})
}

// Replace mencatat faktur pajak pengganti (TAX-04): faktur pajak lama ditandai diganti oleh
// faktur penjualan pengganti milik pembeli yang sama.
func (s *service) Replace(ctx context.Context, companyID, invoiceID, replacementID uuid.UUID, note string, actor audit.Actor) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 300 {
		return httpx.Unprocessable("tax.note", "Catatan maksimal 300 karakter.")
	}
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		inv, err := s.invoice(ctx, companyID, invoiceID)
		if err != nil {
			return err
		}
		repl, err := s.invoice(ctx, companyID, replacementID)
		if err != nil {
			return err
		}
		if repl.ID == inv.ID || repl.CustomerID != inv.CustomerID || repl.Status != "posted" {
			msg := "Faktur pengganti harus faktur lain yang diposting untuk pembeli yang sama."
			return httpx.Unprocessable("tax.replacement", msg).WithFields(map[string]string{"replacementId": msg})
		}
		r, err := s.current(ctx, companyID, inv)
		if err != nil {
			return err
		}
		if r.Status != Approved {
			return httpx.Conflict("tax.not_approved", "Hanya faktur pajak yang sudah terbit yang bisa diganti.")
		}
		r.Status, r.ReplacedBy, r.Note = Replaced, &repl.ID, note
		return s.save(ctx, r, actor, "tax.output.replace", map[string]any{"number": deref(inv.Number), "replacement": deref(repl.Number), "note": note})
	})
}

func (s *service) CanVoidInvoice(ctx context.Context, companyID, invoiceID uuid.UUID) error {
	rows, err := s.q(ctx).OutputInvoiceStates(ctx, store.OutputInvoiceStatesParams{CompanyID: companyID, Ids: []uuid.UUID{invoiceID}})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Status == Approved {
			return httpx.Unprocessable("tax.invoice_locked", fmt.Sprintf(
				"Faktur pajak %s sudah terbit di Coretax. Batalkan dulu di Coretax, tandai dibatalkan di menu Pajak, lalu void fakturnya.",
				deref(r.TaxInvoiceNumber)))
		}
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstOf(f map[string]string) string {
	for _, k := range []string{"goodsOpt", "goodsCode", "unitCode"} {
		if m, ok := f[k]; ok {
			return m
		}
	}
	return "Periksa kembali isian."
}
