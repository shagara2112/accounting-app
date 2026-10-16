// Package tax adalah modul pajak: kode pajak bertanggal berlaku (TAX-01) dan perhitungan
// PPN serta PPh sebagai fungsi murni (TAX-02), sehingga mudah diuji.
package tax

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/modules/tax/internal/store"
	"lajur/internal/platform/api"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

const (
	KindPPN = "ppn"
	KindPPh = "pph"

	ArticlePPh23 = "23"
	ArticleFinal = "4(2)"
)

// Code adalah satu versi kode pajak yang berlaku pada rentang tanggal tertentu.
type Code struct {
	Code      string      `json:"code"`
	Kind      string      `json:"kind"`
	Name      string      `json:"name"`
	RateBP    int         `json:"rateBp"` // basis poin: 1200 = 12%
	DPPNum    int         `json:"dppNum"`
	DPPDen    int         `json:"dppDen"`
	Article   string      `json:"article,omitempty"`
	ValidFrom civil.Date  `json:"validFrom"`
	ValidTo   *civil.Date `json:"validTo,omitempty"`
}

func (c Code) validOn(d civil.Date) bool {
	return !d.Before(c.ValidFrom) && (c.ValidTo == nil || !d.After(*c.ValidTo))
}

// Table adalah semua kode pajak, termasuk versi lama, supaya transaksi lama tetap dihitung
// dengan aturan pada tanggal transaksinya.
type Table []Code

// Find mengembalikan versi kode yang berlaku pada tanggal d.
func (t Table) Find(code string, d civil.Date) (Code, error) {
	known := false
	for _, c := range t {
		if c.Code != code {
			continue
		}
		known = true
		if c.validOn(d) {
			return c, nil
		}
	}
	if !known {
		return Code{}, httpx.Unprocessable("tax.unknown_code", fmt.Sprintf("Kode pajak %s tidak dikenal.", code))
	}
	return Code{}, httpx.Unprocessable("tax.not_valid", fmt.Sprintf("Kode pajak %s tidak berlaku pada %s.", code, d.Indonesian()))
}

// On mengembalikan kode yang berlaku pada tanggal d; kind kosong berarti semua jenis.
func (t Table) On(d civil.Date, kind string) []Code {
	out := []Code{}
	for _, c := range t {
		if c.validOn(d) && (kind == "" || c.Kind == kind) {
			out = append(out, c)
		}
	}
	return out
}

// VAT menghitung PPN satu baris: DPP nilai lain = DPP × num/den (2 desimal, hanya untuk
// ditampilkan dan Coretax), PPN = DPP × num/den × tarif, dibulatkan ke bawah ke rupiah penuh.
// Untuk PPN 12% non-mewah hasilnya tepat 11% dari DPP.
func (c Code) VAT(dpp money.Amount) (otherDPP, vat money.Amount) {
	if c.Kind != KindPPN {
		panic("tax: VAT dipanggil untuk kode " + c.Code)
	}
	if c.RateBP == 0 {
		return dpp, money.Amount{}
	}
	otherDPP = dpp.MulFrac(int64(c.DPPNum), int64(c.DPPDen), 2, money.HalfUp)
	vat = dpp.MulFrac(int64(c.DPPNum)*int64(c.RateBP), int64(c.DPPDen)*10_000, 0, money.Down)
	return otherDPP, vat
}

// Inclusive memecah harga jual yang sudah termasuk PPN (penjualan eceran, PPN digunggung):
// DPP = harga / (1 + num/den × tarif), dibulatkan ke rupiah; PPN = harga − DPP, sehingga uang
// yang diterima tetap sama persis dengan harga.
func (c Code) Inclusive(amount money.Amount) (dpp, otherDPP, vat money.Amount) {
	if c.Kind != KindPPN {
		panic("tax: Inclusive dipanggil untuk kode " + c.Code)
	}
	if c.RateBP == 0 {
		return amount, amount, money.Amount{}
	}
	den := int64(c.DPPDen) * 10_000
	dpp = amount.MulFrac(den, den+int64(c.DPPNum)*int64(c.RateBP), 0, money.HalfUp)
	return dpp, dpp.MulFrac(int64(c.DPPNum), int64(c.DPPDen), 2, money.HalfUp), amount.Sub(dpp)
}

// Withholding menghitung PPh yang dipotong: tarif × dasar, dibulatkan ke bawah ke rupiah.
func (c Code) Withholding(base money.Amount) money.Amount {
	if c.Kind != KindPPh {
		panic("tax: Withholding dipanggil untuk kode " + c.Code)
	}
	return base.MulFrac(int64(c.RateBP), 10_000, 0, money.Down)
}

// Service memuat tabel kode pajak.
type Service interface {
	Table(ctx context.Context) (Table, error)
}

func New(pool *pgxpool.Pool) Service { return &service{pool: pool} }

type service struct{ pool *pgxpool.Pool }

func (s *service) Table(ctx context.Context) (Table, error) {
	rows, err := store.New(db.Conn(ctx, s.pool)).ListTaxCodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make(Table, len(rows))
	for i, r := range rows {
		out[i] = Code{
			Code: r.Code, Kind: r.Kind, Name: r.Name, RateBP: int(r.RateBp), DPPNum: int(r.DppNum),
			DPPDen: int(r.DppDen), ValidFrom: r.ValidFrom, ValidTo: r.ValidTo,
		}
		if r.Article != nil {
			out[i].Article = *r.Article
		}
	}
	return out, nil
}

// Routes: daftar kode pajak yang berlaku pada tanggal tertentu, untuk formulir faktur dan tagihan.
func Routes(svc Service) []api.Route {
	return []api.Route{{
		Method: http.MethodGet, Path: "/api/v1/tax/codes", Access: api.Permission, Perm: rbac.CompanyView,
		License: license.ModeRead,
		Handle: func(w http.ResponseWriter, r *http.Request) error {
			d := civil.Today(civil.Jakarta)
			if v := r.URL.Query().Get("date"); v != "" {
				p, err := civil.Parse(v)
				if err != nil {
					return httpx.BadRequest("request.query", err.Error())
				}
				d = p
			}
			t, err := svc.Table(r.Context())
			if err != nil {
				return err
			}
			return httpx.WriteJSON(w, http.StatusOK, map[string]any{"date": d, "items": t.On(d, r.URL.Query().Get("kind"))})
		},
	}}
}
