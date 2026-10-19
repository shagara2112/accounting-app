package coretax

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/tax/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/sheet"
)

// Faktur pajak masukan (TAX-07). Setiap tagihan ber-PPN punya status pajak masukan: nomor dan
// tanggal faktur pajak pemasok, dikreditkan atau tidak, dan masa pengkreditannya (paling lama
// tiga masa sesudah masa faktur, UU PPN Pasal 9 ayat 9). Tagihan tanpa status dianggap
// dikreditkan pada masa tagihannya, sehingga rekap PPN tidak berubah sampai staf pajak
// mengaturnya. PPN yang tidak dikreditkan dijurnal dari akun PPN masukan ke akun biaya baris
// tagihannya.

// SourceInputVAT menandai jurnal PPN masukan yang tidak dikreditkan (sumber = tagihan).
const SourceInputVAT = "tax.input_vat"

// Status kecocokan dengan daftar pajak masukan Coretax.
const (
	MatchUnchecked = "belum"
	MatchOK        = "cocok"
	MatchDiffers   = "selisih"
)

type InputInvoice struct {
	BillID            uuid.UUID     `json:"billId"`
	Number            string        `json:"number"`
	Date              civil.Date    `json:"date"`
	SupplierID        uuid.UUID     `json:"supplierId"`
	SupplierName      string        `json:"supplierName"`
	SupplierNPWP      string        `json:"supplierNpwp"`
	SupplierInvoiceNo string        `json:"supplierInvoiceNo"`
	DPP               money.Amount  `json:"dpp"`
	VAT               money.Amount  `json:"vat"`    // PPN tagihan
	NetVAT            money.Amount  `json:"netVat"` // sesudah nota debit
	TaxInvoiceNumber  *string       `json:"taxInvoiceNumber,omitempty"`
	TaxInvoiceDate    *civil.Date   `json:"taxInvoiceDate,omitempty"`
	Creditable        bool          `json:"creditable"`
	CreditPeriod      string        `json:"creditPeriod,omitempty"` // YYYY-MM; kosong bila tidak dikreditkan
	ReclassEntryID    *uuid.UUID    `json:"reclassEntryId,omitempty"`
	Match             string        `json:"match"`
	CoretaxVAT        *money.Amount `json:"coretaxVat,omitempty"`
	Note              string        `json:"note"`
	Problems          []string      `json:"problems"`
}

type InputList struct {
	Month         string         `json:"month"`
	PKP           bool           `json:"pkp"`
	Items         []InputInvoice `json:"items"`
	Credited      money.Amount   `json:"credited"`      // dikreditkan pada masa ini
	NotCreditable money.Amount   `json:"notCreditable"` // tagihan masa ini yang tidak dikreditkan
	Unrecorded    int            `json:"unrecorded"`    // dikreditkan tanpa nomor faktur pajak
}

type InputUpdate struct {
	TaxInvoiceNumber string     `json:"taxInvoiceNumber"`
	TaxInvoiceDate   civil.Date `json:"taxInvoiceDate"`
	CreditPeriod     string     `json:"creditPeriod"` // YYYY-MM; kosong = masa faktur atau tagihan
	Note             string     `json:"note"`
}

// defaultInput adalah status tagihan yang belum pernah diatur.
func defaultInput(billID, companyID uuid.UUID) store.TaxInputInvoice {
	return store.TaxInputInvoice{BillID: billID, CompanyID: companyID, Creditable: true}
}

func (s *service) inputStates(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]store.TaxInputInvoice, error) {
	rows, err := s.q(ctx).InputInvoiceStates(ctx, store.InputInvoiceStatesParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]store.TaxInputInvoice, len(rows))
	for _, r := range rows {
		out[r.BillID] = r
	}
	return out, nil
}

// creditMonth adalah masa pengkreditan efektif; nol bila tidak dikreditkan.
func creditMonth(r store.TaxInputInvoice, billDate civil.Date) civil.Date {
	if !r.Creditable {
		return civil.Date{}
	}
	if r.CreditPeriod != nil {
		return *r.CreditPeriod
	}
	return billDate.MonthStart()
}

// creditRange adalah masa pengkreditan yang dibolehkan: tidak sebelum masa tagihan (PPN-nya
// baru ada di buku besar) dan paling lama tiga masa sesudah masa faktur pajak.
func creditRange(billDate civil.Date, taxInvoiceDate *civil.Date) (lo, hi civil.Date) {
	lo = billDate.MonthStart()
	base := lo
	if taxInvoiceDate != nil {
		base = taxInvoiceDate.MonthStart()
		if base.After(lo) {
			lo = base
		}
	}
	return lo, base.AddMonths(3)
}

func toInput(b purchase.InputBill, r store.TaxInputInvoice) InputInvoice {
	out := InputInvoice{
		BillID: b.BillID, Number: b.Number, Date: b.Date, SupplierID: b.Supplier.ID, SupplierName: b.Supplier.Name,
		SupplierNPWP: buyerTIN(b.Supplier), SupplierInvoiceNo: b.SupplierInvoiceNo, DPP: b.DPP, VAT: b.VAT,
		NetVAT: b.VAT.Sub(b.NotedVAT), TaxInvoiceNumber: r.TaxInvoiceNumber, TaxInvoiceDate: r.TaxInvoiceDate,
		Creditable: r.Creditable, ReclassEntryID: r.ReclassEntryID, CoretaxVAT: r.CoretaxVat, Note: r.Note,
		Match: MatchUnchecked, Problems: []string{},
	}
	if m := creditMonth(r, b.Date); !m.IsZero() {
		out.CreditPeriod = m.MonthKey()
	}
	if r.CoretaxCheckedAt != nil && r.CoretaxVat != nil {
		out.Match = MatchOK
		if diff := r.CoretaxVat.Sub(out.NetVAT).Abs(); diff.Cmp(money.FromInt(1)) > 0 {
			out.Match = MatchDiffers
		}
	}
	if out.Creditable && r.TaxInvoiceNumber == nil {
		out.Problems = append(out.Problems, "Nomor faktur pajak pemasok belum dicatat.")
	}
	if cm := creditMonth(r, b.Date); !cm.IsZero() {
		if lo, hi := creditRange(b.Date, r.TaxInvoiceDate); cm.Before(lo) || cm.After(hi) {
			out.Problems = append(out.Problems, fmt.Sprintf("Masa pengkreditan %s di luar batas %s s.d. %s.", cm.MonthLabel(), lo.MonthLabel(), hi.MonthLabel()))
		}
	}
	if out.SupplierNPWP == "" {
		out.Problems = append(out.Problems, "Pemasok belum punya NPWP; PPN masukannya tidak bisa dikreditkan.")
	}
	return out
}

// Inputs: tagihan ber-PPN bertanggal masa month, ditambah tagihan masa sebelumnya yang
// dikreditkan pada masa month.
func (s *service) Inputs(ctx context.Context, companyID uuid.UUID, month civil.Date) (InputList, error) {
	month = month.MonthStart()
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return InputList{}, err
	}
	bills, err := s.Purchase.InputBills(ctx, companyID, month.AddMonths(-3), month.AddMonths(1))
	if err != nil {
		return InputList{}, err
	}
	ids := make([]uuid.UUID, len(bills))
	for i, b := range bills {
		ids[i] = b.BillID
	}
	states, err := s.inputStates(ctx, companyID, ids)
	if err != nil {
		return InputList{}, err
	}
	out := InputList{Month: month.MonthKey(), PKP: company.PKP, Items: []InputInvoice{}}
	for _, b := range bills {
		r, ok := states[b.BillID]
		if !ok {
			r = defaultInput(b.BillID, companyID)
		}
		cm := creditMonth(r, b.Date)
		if !b.Date.SameMonth(month) && cm != month {
			continue
		}
		item := toInput(b, r)
		out.Items = append(out.Items, item)
		switch {
		case cm == month:
			out.Credited = out.Credited.Add(item.NetVAT)
			if item.TaxInvoiceNumber == nil {
				out.Unrecorded++
			}
		case !item.Creditable && b.Date.SameMonth(month):
			out.NotCreditable = out.NotCreditable.Add(item.NetVAT)
		}
	}
	return out, nil
}

func (s *service) inputBill(ctx context.Context, companyID, billID uuid.UUID) (purchase.InputBill, error) {
	b, err := s.Purchase.Bill(ctx, companyID, billID)
	if err != nil {
		return purchase.InputBill{}, err
	}
	if b.Status != "posted" || b.Totals.VAT.Sign() <= 0 {
		return purchase.InputBill{}, httpx.Unprocessable("tax.input_bill", "Hanya tagihan ber-PPN yang sudah diposting yang punya faktur pajak masukan.")
	}
	rows, err := s.Purchase.InputBills(ctx, companyID, b.Date, b.Date.AddDays(1))
	if err != nil {
		return purchase.InputBill{}, err
	}
	for _, r := range rows {
		if r.BillID == billID {
			return r, nil
		}
	}
	return purchase.InputBill{}, httpx.NotFound("bill.not_found", "Tagihan tidak ditemukan.")
}

func (s *service) lockInput(ctx context.Context, companyID, billID uuid.UUID) (store.TaxInputInvoice, error) {
	r, err := s.q(ctx).GetInputInvoiceForUpdate(ctx, store.GetInputInvoiceForUpdateParams{CompanyID: companyID, BillID: billID})
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultInput(billID, companyID), nil
	}
	return r, err
}

func (s *service) saveInput(ctx context.Context, r store.TaxInputInvoice, actor audit.Actor) (store.TaxInputInvoice, error) {
	return s.q(ctx).UpsertInputInvoice(ctx, store.UpsertInputInvoiceParams{
		BillID: r.BillID, CompanyID: r.CompanyID, TaxInvoiceNumber: r.TaxInvoiceNumber, TaxInvoiceDate: r.TaxInvoiceDate,
		Creditable: r.Creditable, CreditPeriod: r.CreditPeriod, ReclassEntryID: r.ReclassEntryID, CoretaxVat: r.CoretaxVat,
		CoretaxCheckedAt: r.CoretaxCheckedAt, Note: r.Note, UpdatedBy: actor.UserRef(),
	})
}

// taxInvoiceDigits membuang pemisah dari nomor faktur pajak, mis. 040.025-00.00000123.
func taxInvoiceDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// UpdateInput mencatat nomor dan tanggal faktur pajak pemasok, masa pengkreditan, dan catatan.
func (s *service) UpdateInput(ctx context.Context, companyID, billID uuid.UUID, in InputUpdate, actor audit.Actor) (InputInvoice, error) {
	var out InputInvoice
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		b, err := s.inputBill(ctx, companyID, billID)
		if err != nil {
			return err
		}
		r, err := s.lockInput(ctx, companyID, billID)
		if err != nil {
			return err
		}
		f := map[string]string{}
		number := taxInvoiceDigits(in.TaxInvoiceNumber)
		switch {
		case strings.TrimSpace(in.TaxInvoiceNumber) == "":
			r.TaxInvoiceNumber, r.TaxInvoiceDate = nil, nil
		case len(number) < 13 || len(number) > 20:
			f["taxInvoiceNumber"] = "Nomor faktur pajak 13–20 digit, mis. 17 digit dari Coretax."
		case in.TaxInvoiceDate.IsZero():
			f["taxInvoiceDate"] = "Isi tanggal faktur pajak."
		default:
			if other, err := s.q(ctx).InputInvoiceByNumber(ctx, store.InputInvoiceByNumberParams{CompanyID: companyID, TaxInvoiceNumber: &number}); err == nil && other != billID {
				f["taxInvoiceNumber"] = "Nomor faktur pajak ini sudah dicatat di tagihan lain."
			} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			d := in.TaxInvoiceDate
			r.TaxInvoiceNumber, r.TaxInvoiceDate = &number, &d
		}
		in.Note = strings.TrimSpace(in.Note)
		if utf8.RuneCountInString(in.Note) > 300 {
			f["note"] = "Catatan maksimal 300 karakter."
		}
		r.Note = in.Note
		if r.Creditable {
			lo, hi := creditRange(b.Date, r.TaxInvoiceDate)
			switch strings.TrimSpace(in.CreditPeriod) {
			case "":
				r.CreditPeriod = nil
			default:
				m, err := civil.ParseMonth(in.CreditPeriod)
				switch {
				case err != nil:
					f["creditPeriod"] = "Format masa YYYY-MM."
				case hi.Before(lo):
					f["creditPeriod"] = "Faktur pajak ini sudah lewat tiga masa sebelum tagihannya dicatat; PPN masukannya tidak bisa dikreditkan lagi."
				case m.Before(lo) || m.After(hi):
					f["creditPeriod"] = fmt.Sprintf("Masa pengkreditan %s s.d. %s (masa tagihan, paling lama tiga masa sesudah masa faktur).", lo.MonthLabel(), hi.MonthLabel())
				default:
					r.CreditPeriod = &m
				}
			}
		}
		if len(f) > 0 {
			return httpx.Unprocessable("tax.input_invalid", firstField(f)).WithFields(f)
		}
		saved, err := s.saveInput(ctx, r, actor)
		if err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.input.update", ObjectType: "purchase_bill", ObjectID: billID.String(), IP: actor.IP,
			After: map[string]any{"taxInvoiceNumber": deref(saved.TaxInvoiceNumber), "creditPeriod": monthKey(saved.CreditPeriod)},
		}); err != nil {
			return err
		}
		out = toInput(b, saved)
		return nil
	})
	return out, err
}

func monthKey(d *civil.Date) string {
	if d == nil {
		return ""
	}
	return d.MonthKey()
}

func firstField(f map[string]string) string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return f[keys[0]]
}

// SetCreditable menandai PPN masukan sebuah tagihan dikreditkan atau tidak. Tidak dikreditkan:
// PPN-nya dijurnal dari PPN masukan ke akun biaya baris tagihan (barang persediaan ke beban PPN
// tidak dapat dikreditkan). Dikreditkan lagi: jurnal itu di-void.
func (s *service) SetCreditable(ctx context.Context, companyID, billID uuid.UUID, creditable bool, date civil.Date, reason string, actor audit.Actor) (InputInvoice, error) {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 5 || n > 300 {
		msg := "Tulis alasan (5–300 karakter)."
		return InputInvoice{}, httpx.Unprocessable("tax.input_reason", msg).WithFields(map[string]string{"reason": msg})
	}
	var out InputInvoice
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		company, err := s.Core.Company(ctx, companyID)
		if err != nil {
			return err
		}
		if !company.PKP {
			return httpx.Unprocessable("tax.input_non_pkp", "PT non-PKP tidak mengkreditkan PPN masukan; PPN-nya sudah menjadi biaya di tagihan.")
		}
		b, err := s.inputBill(ctx, companyID, billID)
		if err != nil {
			return err
		}
		r, err := s.lockInput(ctx, companyID, billID)
		if err != nil {
			return err
		}
		if r.Creditable == creditable {
			return nil
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		if date.Before(b.Date) {
			date = b.Date
		}
		if creditable {
			if r.ReclassEntryID != nil {
				if _, err := s.Ledger.VoidSource(ctx, companyID, *r.ReclassEntryID, SourceInputVAT, date, reason, actor); err != nil {
					return err
				}
			}
			r.Creditable, r.ReclassEntryID = true, nil
		} else {
			entry, err := s.postNonCreditable(ctx, companyID, b, date, reason, actor)
			if err != nil {
				return err
			}
			r.Creditable, r.CreditPeriod, r.ReclassEntryID = false, nil, &entry
		}
		if r.Note == "" {
			r.Note = reason
		}
		saved, err := s.saveInput(ctx, r, actor)
		if err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.input.creditable", ObjectType: "purchase_bill", ObjectID: billID.String(), IP: actor.IP,
			After: map[string]any{"creditable": creditable, "reason": reason, "bill": b.Number},
		}); err != nil {
			return err
		}
		out = toInput(b, saved)
		return nil
	})
	return out, err
}

func (s *service) postNonCreditable(ctx context.Context, companyID uuid.UUID, b purchase.InputBill, date civil.Date, reason string, actor audit.Actor) (uuid.UUID, error) {
	vat, err := s.Purchase.BillVAT(ctx, companyID, b.BillID)
	if err != nil {
		return uuid.Nil, err
	}
	inputAcc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.InputVAT)
	if err != nil {
		return uuid.Nil, err
	}
	memo := "PPN masukan tidak dikreditkan " + b.Number
	var lines []ledger.LineDraft
	var total money.Amount
	for _, l := range vat.Lines {
		account := l.AccountID
		if l.Inventory {
			acc, err := s.Ledger.RoleAccount(ctx, companyID, ledger.NonCreditableVAT)
			if err != nil {
				return uuid.Nil, err
			}
			account = acc.ID
		}
		lines = append(lines, ledger.LineDraft{AccountID: account, Debit: l.VAT, Memo: memo, ProjectID: l.ProjectID,
			BranchID: vat.BranchID, DepartmentID: vat.DepartmentID})
		total = total.Add(l.VAT)
	}
	if total.Sign() <= 0 {
		return uuid.Nil, httpx.Unprocessable("tax.input_zero", fmt.Sprintf("PPN %s sudah habis dibatalkan nota debit.", b.Number))
	}
	lines = append(lines, ledger.LineDraft{AccountID: inputAcc.ID, Credit: total, Memo: memo + ": " + reason,
		BranchID: vat.BranchID, DepartmentID: vat.DepartmentID})
	posted, err := s.Ledger.Post(ctx, ledger.EntryDraft{
		CompanyID: companyID, Kind: ledger.General, Date: date, Memo: memo + " (" + b.Supplier.Name + ")",
		SourceType: SourceInputVAT, SourceID: &b.BillID, Lines: lines, Actor: actor,
	})
	return posted.ID, err
}

// InputMatch adalah hasil pencocokan daftar pajak masukan Coretax dengan tagihan.
type InputMatch struct {
	Rows        int               `json:"rows"`
	Matched     []string          `json:"matched"`     // nomor tagihan yang cocok
	Differences []InputDifference `json:"differences"` // cocok nomornya, PPN-nya berbeda
	CoretaxOnly []CoretaxInput    `json:"coretaxOnly"` // ada di Coretax, tidak ada tagihannya
	LajurOnly   []string          `json:"lajurOnly"`   // tagihan di masa yang sama tanpa faktur di Coretax
}

type InputDifference struct {
	Bill       string       `json:"bill"`
	Number     string       `json:"number"`
	LajurVAT   money.Amount `json:"lajurVat"`
	CoretaxVAT money.Amount `json:"coretaxVat"`
}

type CoretaxInput struct {
	Line     int          `json:"line"`
	Supplier string       `json:"supplier"`
	NPWP     string       `json:"npwp"`
	Number   string       `json:"number"`
	Date     civil.Date   `json:"date"`
	VAT      money.Amount `json:"vat"`
}

// normalNPWP menyamakan NPWP 15 digit lama dengan format 16 digit (diawali 0).
func normalNPWP(s string) string {
	d := taxInvoiceDigits(s)
	if len(d) == 15 {
		return "0" + d
	}
	return d
}

// readCoretaxInputs membaca berkas unduhan daftar pajak masukan Coretax (Excel atau CSV). Kolom
// dikenali dari judulnya: NPWP penjual, nama penjual, nomor faktur, tanggal faktur, dan PPN.
func readCoretaxInputs(name string, data []byte) ([]CoretaxInput, error) {
	rows, err := sheet.Read(name, data)
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	header := -1
	for i := 0; i < len(rows) && i < 30 && header < 0; i++ {
		c := map[string]int{}
		for j, h := range rows[i] {
			h = strings.ToLower(strings.TrimSpace(h))
			switch {
			case strings.Contains(h, "npwp") && c["npwp"] == 0:
				c["npwp"] = j + 1
			case strings.Contains(h, "nama") && c["nama"] == 0:
				c["nama"] = j + 1
			case (strings.Contains(h, "nomor faktur") || strings.Contains(h, "no faktur") || strings.Contains(h, "nomor fp") || h == "nsfp") && c["nomor"] == 0:
				c["nomor"] = j + 1
			case strings.Contains(h, "tanggal") && c["tanggal"] == 0:
				c["tanggal"] = j + 1
			case (h == "ppn" || strings.HasPrefix(h, "ppn ") || strings.HasPrefix(h, "jumlah ppn") || strings.HasPrefix(h, "total ppn")) && !strings.Contains(h, "ppnbm") && c["ppn"] == 0:
				c["ppn"] = j + 1
			}
		}
		if c["npwp"] > 0 && c["nomor"] > 0 && c["ppn"] > 0 {
			col, header = c, i
		}
	}
	if header < 0 {
		return nil, httpx.Unprocessable("tax.input_import", "Kolom NPWP penjual, nomor faktur pajak, dan PPN tidak ditemukan. Unggah berkas unduhan daftar pajak masukan dari Coretax.")
	}
	get := func(r []string, key string) string {
		if i := col[key] - 1; i >= 0 && i < len(r) {
			return strings.TrimSpace(r[i])
		}
		return ""
	}
	var out []CoretaxInput
	for i := header + 1; i < len(rows); i++ {
		r := rows[i]
		number := taxInvoiceDigits(get(r, "nomor"))
		if number == "" {
			continue
		}
		vat, err := sheet.Amount(get(r, "ppn"))
		if err != nil {
			return nil, httpx.Unprocessable("tax.input_import", fmt.Sprintf("Baris %d: PPN tidak terbaca (%s).", i+1, get(r, "ppn")))
		}
		in := CoretaxInput{Line: i + 1, Supplier: get(r, "nama"), NPWP: normalNPWP(get(r, "npwp")), Number: number, VAT: vat}
		if v := get(r, "tanggal"); v != "" {
			if d, err := sheet.Date(v); err == nil {
				in.Date = d
			}
		}
		out = append(out, in)
	}
	if len(out) == 0 {
		return nil, httpx.Unprocessable("tax.input_import", "Berkas tidak berisi faktur pajak masukan.")
	}
	return out, nil
}

// ImportCoretaxInputs mencocokkan daftar pajak masukan Coretax dengan tagihan: lewat nomor
// faktur yang sudah dicatat, atau NPWP pemasok dan PPN yang sama. Nomor dan tanggal faktur
// yang cocok dicatat otomatis, dan PPN menurut Coretax disimpan untuk penanda selisih.
func (s *service) ImportCoretaxInputs(ctx context.Context, companyID uuid.UUID, name string, data []byte, actor audit.Actor) (InputMatch, error) {
	rows, err := readCoretaxInputs(name, data)
	if err != nil {
		return InputMatch{}, err
	}
	out := InputMatch{Rows: len(rows), Matched: []string{}, Differences: []InputDifference{}, CoretaxOnly: []CoretaxInput{}, LajurOnly: []string{}}
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		from, to := civil.Date{}, civil.Date{}
		months := map[civil.Date]bool{}
		for _, r := range rows {
			if r.Date.IsZero() {
				continue
			}
			months[r.Date.MonthStart()] = true
			if from.IsZero() || r.Date.Before(from) {
				from = r.Date
			}
			if to.IsZero() || r.Date.After(to) {
				to = r.Date
			}
		}
		if from.IsZero() {
			from, to = civil.Today(civil.Jakarta).AddMonths(-12), civil.Today(civil.Jakarta)
		}
		bills, err := s.Purchase.InputBills(ctx, companyID, from.MonthStart().AddMonths(-3), to.MonthStart().AddMonths(4))
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(bills))
		for i, b := range bills {
			ids[i] = b.BillID
		}
		states, err := s.inputStates(ctx, companyID, ids)
		if err != nil {
			return err
		}
		used := map[uuid.UUID]bool{}
		now := time.Now()
		for _, row := range rows {
			var match *purchase.InputBill
			for i := range bills {
				if st, ok := states[bills[i].BillID]; ok && st.TaxInvoiceNumber != nil && *st.TaxInvoiceNumber == row.Number {
					match = &bills[i]
					break
				}
			}
			if match == nil {
				var best *purchase.InputBill
				for i := range bills {
					b := &bills[i]
					st, ok := states[b.BillID]
					if used[b.BillID] || (ok && st.TaxInvoiceNumber != nil) || normalNPWP(buyerTIN(b.Supplier)) != row.NPWP {
						continue
					}
					if b.VAT.Sub(b.NotedVAT).Sub(row.VAT).Abs().Cmp(money.FromInt(1)) > 0 {
						continue
					}
					if best == nil || (!row.Date.IsZero() && absDays(b.Date, row.Date) < absDays(best.Date, row.Date)) {
						best = b
					}
				}
				match = best
			}
			if match == nil {
				out.CoretaxOnly = append(out.CoretaxOnly, row)
				continue
			}
			used[match.BillID] = true
			st, ok := states[match.BillID]
			if !ok {
				st = defaultInput(match.BillID, companyID)
			}
			if st.TaxInvoiceNumber == nil {
				number := row.Number
				st.TaxInvoiceNumber = &number
				d := row.Date
				if d.IsZero() {
					d = match.Date
				}
				st.TaxInvoiceDate = &d
			}
			vat := row.VAT
			st.CoretaxVat, st.CoretaxCheckedAt = &vat, &now
			saved, err := s.saveInput(ctx, st, actor)
			if err != nil {
				return err
			}
			states[match.BillID] = saved
			net := match.VAT.Sub(match.NotedVAT)
			if net.Sub(row.VAT).Abs().Cmp(money.FromInt(1)) > 0 {
				out.Differences = append(out.Differences, InputDifference{Bill: match.Number, Number: row.Number, LajurVAT: net, CoretaxVAT: row.VAT})
			} else {
				out.Matched = append(out.Matched, match.Number)
			}
		}
		for _, b := range bills {
			st, ok := states[b.BillID]
			if !ok {
				st = defaultInput(b.BillID, companyID)
			}
			if used[b.BillID] || !st.Creditable || !months[creditMonth(st, b.Date)] {
				continue
			}
			out.LajurOnly = append(out.LajurOnly, b.Number)
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.input.import", ObjectType: "company", ObjectID: companyID.String(), IP: actor.IP,
			After: map[string]any{"rows": out.Rows, "matched": len(out.Matched), "differences": len(out.Differences), "coretaxOnly": len(out.CoretaxOnly)},
		})
		return err
	})
	return out, err
}

func absDays(a, b civil.Date) int {
	if d := a.DaysSince(b); d >= 0 {
		return d
	}
	return b.DaysSince(a)
}
