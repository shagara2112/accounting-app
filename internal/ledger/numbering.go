package ledger

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
)

// Penomoran dokumen (GL-07). Setiap seri punya awalan, siklus ulang (bulanan, tahunan, atau
// tidak pernah), jumlah digit, dan pilihan nomor terpisah per cabang. Tanpa pengaturan, formatnya
// SERI/YYYY/MM/0001 yang diulang tiap bulan. Urutan nomor disimpan per seri (dan cabang) dan
// periode, sehingga mengganti format tidak pernah menghasilkan nomor ganda.

// Series adalah seri nomor yang dipakai modul; label untuk layar pengaturan.
var Series = []struct{ Code, Label string }{
	{"FJ", "Faktur penjualan"}, {"NK", "Nota kredit"}, {"BKM", "Bukti kas masuk (pelunasan, penerimaan)"},
	{"RPH", "Rekap penjualan harian"}, {"FB", "Tagihan pembelian"}, {"ND", "Nota debit"},
	{"BKK", "Bukti kas keluar (pembayaran, pengeluaran)"}, {"TRF", "Transfer kas"},
	{"JU", "Jurnal umum"}, {"JP", "Jurnal penyesuaian"}, {"JB", "Jurnal balik otomatis"}, {"JV", "Jurnal pembatalan"},
	{"HPP", "Jurnal HPP"}, {"PS", "Dokumen persediaan"},
	{"PNY", "Penyusutan aset tetap"}, {"PAT", "Pelepasan aset tetap"},
	{"SA", "Saldo awal"}, {"SAP", "Saldo awal piutang"}, {"SAU", "Saldo awal utang"},
}

const (
	ResetMonth = "month"
	ResetYear  = "year"
	ResetNever = "never"
)

type NumberFormat struct {
	Series    string `json:"series"`
	Label     string `json:"label"`
	Prefix    string `json:"prefix"`
	Reset     string `json:"reset"`
	Digits    int    `json:"digits"`
	PerBranch bool   `json:"perBranch"`
	Custom    bool   `json:"custom"`  // sudah diatur; false = format bawaan
	Example   string `json:"example"` // contoh nomor untuk hari ini
}

func defaultFormat(series string) NumberFormat {
	return NumberFormat{Series: series, Prefix: series, Reset: ResetMonth, Digits: 4}
}

// render menyusun nomor dari format, kode cabang (boleh kosong), tanggal, dan urutan.
func (f NumberFormat) render(branch string, date civil.Date, n int64) string {
	parts := []string{f.Prefix}
	if f.PerBranch && branch != "" {
		parts = append(parts, branch)
	}
	switch f.Reset {
	case ResetMonth:
		parts = append(parts, fmt.Sprintf("%04d", date.Year()), fmt.Sprintf("%02d", int(date.Month())))
	case ResetYear:
		parts = append(parts, fmt.Sprintf("%04d", date.Year()))
	}
	return strings.Join(append(parts, fmt.Sprintf("%0*d", f.Digits, n)), "/")
}

// period adalah kunci periode urutan nomor sesuai siklus ulangnya.
func (f NumberFormat) period(date civil.Date) civil.Date {
	switch f.Reset {
	case ResetYear:
		return civil.New(date.Year(), 1, 1)
	case ResetNever:
		return civil.New(2000, 1, 1)
	default:
		return date.MonthStart()
	}
}

func loadFormat(ctx context.Context, q *store.Queries, companyID uuid.UUID, series string) (NumberFormat, error) {
	r, err := q.GetNumberFormat(ctx, store.GetNumberFormatParams{CompanyID: companyID, Series: series})
	if isNoRows(err) {
		return defaultFormat(series), nil
	}
	if err != nil {
		return NumberFormat{}, err
	}
	return NumberFormat{Series: series, Prefix: r.Prefix, Reset: r.Reset, Digits: int(r.Digits), PerBranch: r.PerBranch, Custom: true}, nil
}

func nextNumber(ctx context.Context, q *store.Queries, companyID uuid.UUID, series string, date civil.Date, branch *uuid.UUID) (string, error) {
	if !seriesPattern.MatchString(series) {
		return "", fmt.Errorf("seri nomor %q tidak valid", series)
	}
	f, err := loadFormat(ctx, q, companyID, series)
	if err != nil {
		return "", err
	}
	key, code := series, ""
	if f.PerBranch && branch != nil {
		if code, err = q.BranchCode(ctx, store.BranchCodeParams{CompanyID: companyID, ID: *branch}); err != nil {
			return "", err
		}
		key = series + "@" + code
	}
	n, err := q.NextNumber(ctx, store.NextNumberParams{CompanyID: companyID, Series: key, Month: f.period(date)})
	if err != nil {
		return "", err
	}
	return f.render(code, date, int64(n)), nil
}

func (l *ledger) NextNumber(ctx context.Context, companyID uuid.UUID, series string, date civil.Date) (string, error) {
	return nextNumber(ctx, l.q(ctx), companyID, series, date, nil)
}

func (l *ledger) NextNumberFor(ctx context.Context, companyID uuid.UUID, series string, date civil.Date, branch *uuid.UUID) (string, error) {
	return nextNumber(ctx, l.q(ctx), companyID, series, date, branch)
}

func (l *ledger) NumberFormats(ctx context.Context, companyID uuid.UUID, today civil.Date) ([]NumberFormat, error) {
	q := l.q(ctx)
	out := make([]NumberFormat, 0, len(Series))
	for _, s := range Series {
		f, err := loadFormat(ctx, q, companyID, s.Code)
		if err != nil {
			return nil, err
		}
		f.Label = s.Label
		f.Example = f.render("JKT", today, 1)
		if !f.PerBranch {
			f.Example = f.render("", today, 1)
		}
		out = append(out, f)
	}
	return out, nil
}

var prefixPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,12}$`)

func (l *ledger) SaveNumberFormat(ctx context.Context, companyID uuid.UUID, in NumberFormat, actor Actor) (NumberFormat, error) {
	label := ""
	for _, s := range Series {
		if s.Code == in.Series {
			label = s.Label
		}
	}
	if label == "" {
		return NumberFormat{}, httpx.NotFound("number.series", "Seri nomor tidak dikenal.")
	}
	in.Prefix = strings.TrimSpace(in.Prefix)
	f := map[string]string{}
	if !prefixPattern.MatchString(in.Prefix) {
		f["prefix"] = "Awalan 1–12 huruf, angka, atau tanda hubung."
	}
	switch in.Reset {
	case ResetMonth, ResetYear, ResetNever:
	default:
		f["reset"] = "Pilih ulang bulanan, tahunan, atau tidak pernah."
	}
	if in.Digits < 3 || in.Digits > 8 {
		f["digits"] = "Jumlah digit 3–8."
	}
	if len(f) > 0 {
		return NumberFormat{}, httpx.Unprocessable("number.invalid", firstMessage(f)).WithFields(f)
	}
	var out NumberFormat
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		// Awalan tidak boleh sama dengan kode atau awalan seri lain, supaya nomor dua seri tidak
		// pernah bertabrakan.
		for _, s := range Series {
			if s.Code == in.Series {
				continue
			}
			other, err := loadFormat(ctx, q, companyID, s.Code)
			if err != nil {
				return err
			}
			if strings.EqualFold(other.Prefix, in.Prefix) || strings.EqualFold(s.Code, in.Prefix) {
				msg := fmt.Sprintf("Awalan %s sudah dipakai seri %s (%s).", in.Prefix, s.Code, s.Label)
				return httpx.Conflict("number.prefix_taken", msg).WithFields(map[string]string{"prefix": msg})
			}
		}
		before, err := loadFormat(ctx, q, companyID, in.Series)
		if err != nil {
			return err
		}
		if err := q.UpsertNumberFormat(ctx, store.UpsertNumberFormatParams{
			CompanyID: companyID, Series: in.Series, Prefix: in.Prefix, Reset: in.Reset, Digits: int16(in.Digits),
			PerBranch: in.PerBranch, UpdatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "ledger.number_format.save", ObjectType: "number_format", ObjectID: in.Series, IP: actor.IP,
			Before: map[string]any{"prefix": before.Prefix, "reset": before.Reset, "digits": before.Digits, "perBranch": before.PerBranch},
			After:  map[string]any{"prefix": in.Prefix, "reset": in.Reset, "digits": in.Digits, "perBranch": in.PerBranch},
		}); err != nil {
			return err
		}
		out, err = loadFormat(ctx, q, companyID, in.Series)
		out.Label = label
		out.Example = out.render("JKT", l.today(), 1)
		if !out.PerBranch {
			out.Example = out.render("", l.today(), 1)
		}
		return err
	})
	return out, err
}
