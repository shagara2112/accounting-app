package coretax

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/tax/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// VATMonth adalah satu masa PPN: keluaran dikurangi PPN yang dipungut pelanggan pemungut
// (kode 02/03, disetor oleh pelanggan), masukan, dan kompensasi lebih bayar masa sebelumnya.
// Hasil positif = kurang bayar (disetor); negatif = lebih bayar (dikompensasikan ke masa
// berikutnya).
type VATMonth struct {
	Month       string       `json:"month"`
	Invoices    int64        `json:"invoices"`
	Output      money.Amount `json:"output"`
	Collected   money.Amount `json:"collected"` // dipungut pelanggan pemungut (02/03)
	Bills       int64        `json:"bills"`
	Input       money.Amount `json:"input"`
	Compensated money.Amount `json:"compensated"`
	Payable     money.Amount `json:"payable"`  // kurang bayar
	Overpaid    money.Amount `json:"overpaid"` // lebih bayar, dibawa ke masa berikutnya
}

type VATRecap struct {
	Year   int        `json:"year"`
	PKP    bool       `json:"pkp"`
	Months []VATMonth `json:"months"`
	Totals VATMonth   `json:"totals"`
}

// VATRecap menghitung rekap PPN 12 masa tahun year (TAX-06). Lebih bayar otomatis
// dikompensasikan ke masa berikutnya, termasuk dari tahun sebelumnya.
func (s *service) VATRecap(ctx context.Context, companyID uuid.UUID, year int) (VATRecap, error) {
	if year < 2000 || year > 2100 {
		return VATRecap{}, httpx.BadRequest("request.query", "Tahun tidak valid.")
	}
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return VATRecap{}, err
	}
	since, end := civil.New(2000, 1, 1), civil.New(year+1, 1, 1)
	codes, err := s.trxCodeOverrides(ctx, companyID)
	if err != nil {
		return VATRecap{}, err
	}
	out, err := s.Sales.VATByMonth(ctx, companyID, since, end, codes)
	if err != nil {
		return VATRecap{}, err
	}
	months := map[string]*VATMonth{}
	get := func(k string) *VATMonth {
		if m, ok := months[k]; ok {
			return m
		}
		m := &VATMonth{Month: k}
		months[k] = m
		return m
	}
	for _, r := range out {
		m := get(r.Month.MonthKey())
		m.Invoices, m.Output, m.Collected = r.Documents, r.VAT, r.Collected
	}
	// PT non-PKP tidak bisa mengkreditkan PPN masukan.
	if company.PKP {
		if err := s.inputByMonth(ctx, companyID, since, end, get); err != nil {
			return VATRecap{}, err
		}
	}
	for i := 1; i <= 12; i++ {
		get(civil.New(year, time.Month(i), 1).MonthKey())
	}
	keys := make([]string, 0, len(months))
	for k := range months {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	recap := VATRecap{Year: year, PKP: company.PKP, Months: []VATMonth{}, Totals: VATMonth{Month: fmt.Sprint(year)}}
	var carry money.Amount
	for _, k := range keys {
		m := months[k]
		m.Compensated = carry
		net := m.Output.Sub(m.Collected).Sub(m.Input).Sub(carry)
		if net.Sign() >= 0 {
			m.Payable, carry = net, money.Amount{}
		} else {
			m.Overpaid, carry = net.Neg(), net.Neg()
		}
		if strings.HasPrefix(k, fmt.Sprint(year)) {
			recap.Months = append(recap.Months, *m)
			t := &recap.Totals
			t.Invoices += m.Invoices
			t.Bills += m.Bills
			t.Output, t.Input, t.Payable = t.Output.Add(m.Output), t.Input.Add(m.Input), t.Payable.Add(m.Payable)
			t.Collected = t.Collected.Add(m.Collected)
		}
	}
	if n := len(recap.Months); n > 0 {
		recap.Totals.Compensated = recap.Months[0].Compensated
		recap.Totals.Overpaid = recap.Months[n-1].Overpaid
	}
	return recap, nil
}

// trxCodeOverrides mengelompokkan faktur yang kode transaksinya sudah ditetapkan di modul pajak:
// 02/03 dipungut pelanggan pemungut, kode lain tidak.
func (s *service) trxCodeOverrides(ctx context.Context, companyID uuid.UUID) (sales.VATCodeOverrides, error) {
	rows, err := s.q(ctx).OutputTrxCodes(ctx, companyID)
	if err != nil {
		return sales.VATCodeOverrides{}, err
	}
	var out sales.VATCodeOverrides
	for _, r := range rows {
		if collectedCode(r.TrxCode) {
			out.Collected = append(out.Collected, r.InvoiceID)
		} else {
			out.NotCollected = append(out.NotCollected, r.InvoiceID)
		}
	}
	return out, nil
}

// collectedCode: kode transaksi penyerahan kepada pemungut PPN, yang PPN-nya disetor pemungut.
func collectedCode(code string) bool { return code == "02" || code == "03" }

// inputByMonth menjumlahkan PPN masukan per masa pengkreditannya (TAX-07): tagihan yang tidak
// dikreditkan dilewati beserta nota debitnya, dan nota debit mengurangi PPN masukan pada masa
// notanya, atau masa pengkreditan tagihannya bila lebih akhir.
func (s *service) inputByMonth(ctx context.Context, companyID uuid.UUID, since, end civil.Date, get func(string) *VATMonth) error {
	bills, err := s.Purchase.InputBills(ctx, companyID, since, end)
	if err != nil {
		return err
	}
	credits, err := s.Purchase.InputCredits(ctx, companyID, since, end)
	if err != nil {
		return err
	}
	rows, err := s.q(ctx).AllInputInvoiceStates(ctx, companyID)
	if err != nil {
		return err
	}
	states := make(map[uuid.UUID]store.TaxInputInvoice, len(rows))
	for _, r := range rows {
		states[r.BillID] = store.TaxInputInvoice{BillID: r.BillID, Creditable: r.Creditable, CreditPeriod: r.CreditPeriod}
	}
	credited := make(map[uuid.UUID]civil.Date, len(bills))
	for _, b := range bills {
		r, ok := states[b.BillID]
		if !ok {
			r = defaultInput(b.BillID, companyID)
		}
		cm := creditMonth(r, b.Date)
		if cm.IsZero() {
			continue
		}
		credited[b.BillID] = cm
		m := get(cm.MonthKey())
		m.Bills++
		m.Input = m.Input.Add(b.VAT)
	}
	for _, c := range credits {
		cm, ok := credited[c.BillID]
		if !ok {
			continue
		}
		if month := c.Date.MonthStart(); month.After(cm) {
			cm = month
		}
		m := get(cm.MonthKey())
		m.Input = m.Input.Sub(c.VAT)
	}
	return nil
}

type WithholdingTotal struct {
	Code   string       `json:"code"`
	Count  int          `json:"count"`
	Amount money.Amount `json:"amount"`
}

// WithholdingRecap: PPh yang dipotong pelanggan (kredit pajak atau beban final) dan PPh yang
// kita potong dari pemasok (disetor dan dilaporkan di SPT Masa PPh Unifikasi), per masa.
type WithholdingRecap struct {
	Month        string              `json:"month"`
	ByCustomers  []sales.Withheld    `json:"byCustomers"`
	ByUs         []purchase.Withheld `json:"byUs"`
	CustomerSums []WithholdingTotal  `json:"customerSums"`
	OurSums      []WithholdingTotal  `json:"ourSums"`
}

func (s *service) Withholding(ctx context.Context, companyID uuid.UUID, month civil.Date) (WithholdingRecap, error) {
	month = month.MonthStart()
	end := month.AddMonths(1)
	byCustomers, err := s.Sales.Withheld(ctx, companyID, month, end)
	if err != nil {
		return WithholdingRecap{}, err
	}
	byUs, err := s.Purchase.Withheld(ctx, companyID, month, end)
	if err != nil {
		return WithholdingRecap{}, err
	}
	out := WithholdingRecap{Month: month.MonthKey(), ByCustomers: byCustomers, ByUs: byUs}
	cs := map[string]*WithholdingTotal{}
	for _, w := range byCustomers {
		add(cs, w.Code, w.Amount)
	}
	ours := map[string]*WithholdingTotal{}
	for _, w := range byUs {
		add(ours, w.Code, w.Amount)
	}
	out.CustomerSums, out.OurSums = totals(cs), totals(ours)
	return out, nil
}

func add(m map[string]*WithholdingTotal, code string, a money.Amount) {
	t, ok := m[code]
	if !ok {
		t = &WithholdingTotal{Code: code}
		m[code] = t
	}
	t.Count++
	t.Amount = t.Amount.Add(a)
}

func totals(m map[string]*WithholdingTotal) []WithholdingTotal {
	out := make([]WithholdingTotal, 0, len(m))
	for _, t := range m {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// ImportNumbers mencatat nomor faktur pajak dari CSV: hasil unduhan daftar faktur Coretax yang
// disimpan sebagai CSV, atau tempelan manual. Kolom dikenali dari judulnya: referensi (nomor
// faktur Lajur), nomor faktur pajak, dan tanggal (opsional). Pemisah koma atau titik koma.
func (s *service) ImportNumbers(ctx context.Context, companyID uuid.UUID, text string, actor audit.Actor) (ImportResult, error) {
	rows, err := parseCSV(text)
	if err != nil {
		return ImportResult{}, httpx.Unprocessable("tax.import_format", err.Error())
	}
	out := ImportResult{Recorded: []string{}, Skipped: []string{}}
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		for _, r := range rows {
			invs, err := s.Sales.Invoices(ctx, companyID, sales.InvoiceFilter{
				From: civil.New(2000, 1, 1), To: civil.New(2100, 12, 31), Search: r.reference,
			})
			if err != nil {
				return err
			}
			var match *sales.Invoice
			for i := range invs {
				if invs[i].Number != nil && strings.EqualFold(*invs[i].Number, r.reference) {
					match = &invs[i]
				}
			}
			if match == nil {
				out.Skipped = append(out.Skipped, fmt.Sprintf("Baris %d: faktur %s tidak ditemukan.", r.line, r.reference))
				continue
			}
			if err := s.Approve(ctx, companyID, match.ID, r.number, r.date, actor); err != nil {
				var p *httpx.Problem
				if errors.As(err, &p) {
					out.Skipped = append(out.Skipped, fmt.Sprintf("Baris %d (%s): %s", r.line, r.reference, p.Detail))
					continue
				}
				return err
			}
			out.Recorded = append(out.Recorded, r.reference)
		}
		return nil
	})
	return out, err
}

type csvRow struct {
	line      int
	reference string
	number    string
	date      civil.Date
}

// parseCSV mencari kolom referensi, nomor faktur pajak, dan tanggal dari baris judul.
func parseCSV(text string) ([]csvRow, error) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "\ufeff")
	if text == "" {
		return nil, errors.New("isi CSV kosong")
	}
	first, _, _ := strings.Cut(text, "\n")
	r := csv.NewReader(bytes.NewBufferString(text))
	if strings.Count(first, ";") > strings.Count(first, ",") {
		r.Comma = ';'
	}
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, errors.New("baris judul CSV tidak terbaca")
	}
	ref, num, date := -1, -1, -1
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case strings.Contains(h, "referensi") || strings.Contains(h, "reference") || h == "refdesc":
			ref = i
		case strings.Contains(h, "nomor faktur") || strings.Contains(h, "no faktur") || strings.Contains(h, "nsfp") || strings.Contains(h, "tax invoice number"):
			num = i
		case strings.Contains(h, "tanggal") || strings.Contains(h, "date"):
			if date < 0 {
				date = i
			}
		}
	}
	if ref < 0 || num < 0 {
		return nil, errors.New("CSV harus punya kolom Referensi (nomor faktur Lajur) dan Nomor Faktur Pajak")
	}
	var out []csvRow
	for line := 2; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("baris %d tidak terbaca: %v", line, err)
		}
		if ref >= len(rec) || num >= len(rec) || strings.TrimSpace(rec[ref]) == "" {
			continue
		}
		row := csvRow{line: line, reference: strings.TrimSpace(rec[ref]), number: strings.TrimSpace(rec[num])}
		if date >= 0 && date < len(rec) {
			row.date = parseDate(strings.TrimSpace(rec[date]))
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, errors.New("CSV tidak berisi baris data")
	}
	if len(out) > 1000 {
		return nil, errors.New("maksimal 1.000 baris per impor")
	}
	return out, nil
}

// parseDate menerima 2026-10-05, 05/10/2026, atau 05-10-2026; selain itu dianggap kosong.
func parseDate(s string) civil.Date {
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006", "2/1/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return civil.Of(t)
		}
	}
	return civil.Date{}
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}
