// Package inventory adalah modul produk dan persediaan dasar: master produk dan jasa (INV-01),
// stok satu gudang dengan HPP rata-rata tertimbang (INV-03), serta penyesuaian, pemakaian
// barang, dan stock opname (INV-04).
//
// Setiap pergerakan stok dicatat di kartu stok. Penjualan dan pemakaian keluar dengan biaya
// rata-rata saat itu, dan HPP-nya dijurnal modul ini sendiri. Transaksi bertanggal mundur
// menghitung ulang semua pergerakan sesudahnya; bila HPP-nya berubah, selisihnya dijurnal
// sebagai koreksi HPP pada tanggal pergerakan itu. Nilai persediaan di buku besar karena itu
// selalu sama dengan kartu stok.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/inventory/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	KindGoods   = "barang"
	KindService = "jasa"

	// SourceCost menandai jurnal HPP dan nilai persediaan yang dibuat modul ini.
	SourceCost       = "inventory.cost"
	SourceReturnCost = "inventory.return" // pembalikan HPP retur penjualan, bersumber nota kredit
	SourceAdjustment = "inventory.adjustment"
	sourceCreditNote = "sales.credit_note"
	SourceDailyCost  = "inventory.daily" // HPP rekap penjualan harian, bersumber rekapnya
	sourceDaily      = "sales.daily"

	seriesCost       = "HPP" // jurnal HPP dan koreksinya
	seriesAdjustment = "PS"  // penyesuaian, pemakaian, dan stock opname
)

type Product struct {
	ID                 uuid.UUID    `json:"id"`
	Code               string       `json:"code"`
	Name               string       `json:"name"`
	Kind               string       `json:"kind"`
	Unit               string       `json:"unit"`
	SalePrice          money.Amount `json:"salePrice"`
	PurchasePrice      money.Amount `json:"purchasePrice"`
	RevenueAccountID   *uuid.UUID   `json:"revenueAccountId"`
	ExpenseAccountID   *uuid.UUID   `json:"expenseAccountId"`
	InventoryAccountID *uuid.UUID   `json:"inventoryAccountId"`
	Active             bool         `json:"active"`
	Version            int          `json:"version"`
	StockQty           money.Amount `json:"stockQty"`
	StockValue         money.Amount `json:"stockValue"`
	AvgCost            money.Amount `json:"avgCost"`
}

func (p Product) Goods() bool { return p.Kind == KindGoods }

type ProductInput struct {
	Code               string       `json:"code"`
	Name               string       `json:"name"`
	Kind               string       `json:"kind"`
	Unit               string       `json:"unit"`
	SalePrice          money.Amount `json:"salePrice"`
	PurchasePrice      money.Amount `json:"purchasePrice"`
	RevenueAccountID   *uuid.UUID   `json:"revenueAccountId"`
	ExpenseAccountID   *uuid.UUID   `json:"expenseAccountId"`
	InventoryAccountID *uuid.UUID   `json:"inventoryAccountId"`
	Active             bool         `json:"active"`
	Version            int          `json:"version"`
}

type ProductFilter struct {
	Kind            string
	Search          string
	IncludeInactive bool
}

// Doc adalah dokumen sumber pergerakan stok, mis. faktur penjualan atau tagihan pembelian.
type Doc struct {
	SourceType string
	SourceID   uuid.UUID
	Number     string
	Date       civil.Date
	ProjectID  *uuid.UUID
	// Dimensi cabang dan departemen (GL-09) untuk jurnal HPP-nya.
	BranchID     *uuid.UUID
	DepartmentID *uuid.UUID
}

// Issue adalah barang keluar dengan biaya rata-rata. HPP-nya dijurnal modul ini.
type Issue struct {
	ProductID uuid.UUID
	Qty       money.Amount // positif
	Memo      string
}

// Receipt adalah barang masuk dengan nilai tetap yang sudah dijurnal dokumen sumbernya
// (debit akun persediaan produk), mis. baris tagihan pembelian.
type Receipt struct {
	ProductID uuid.UUID
	Qty       money.Amount // positif
	Value     money.Amount
	Memo      string
}

type Service interface {
	Products(ctx context.Context, companyID uuid.UUID, f ProductFilter) ([]Product, error)
	Product(ctx context.Context, companyID, id uuid.UUID) (Product, error)
	ProductsByIDs(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Product, error)
	CreateProduct(ctx context.Context, companyID uuid.UUID, in ProductInput, actor audit.Actor) (Product, error)
	UpdateProduct(ctx context.Context, companyID, id uuid.UUID, in ProductInput, actor audit.Actor) (Product, error)
	// InventoryAccounts adalah akun persediaan milik produk barang. Akun ini hanya boleh
	// bertambah lewat produk, supaya buku besar sama dengan kartu stok.
	InventoryAccounts(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]bool, error)

	// IssueGoods, ReceiveGoods, ReturnGoods, SendBack, dan ReverseSource dipanggil modul
	// penjualan dan pembelian di transaksi posting atau void dokumennya.
	IssueGoods(ctx context.Context, companyID uuid.UUID, doc Doc, lines []Issue, actor audit.Actor) error
	ReceiveGoods(ctx context.Context, companyID uuid.UUID, doc Doc, lines []Receipt, actor audit.Actor) error
	ReturnGoods(ctx context.Context, companyID uuid.UUID, doc Doc, origType string, origID uuid.UUID, lines []Issue, actor audit.Actor) error
	SendBack(ctx context.Context, companyID uuid.UUID, doc Doc, lines []Receipt, actor audit.Actor) error
	ReverseSource(ctx context.Context, companyID uuid.UUID, sourceType string, sourceID uuid.UUID, date civil.Date, actor audit.Actor) error

	Stock(ctx context.Context, companyID uuid.UUID, asOf civil.Date) (StockReport, error)
	Card(ctx context.Context, companyID, productID uuid.UUID, from, to civil.Date) (StockCard, error)

	Adjustments(ctx context.Context, companyID uuid.UUID, from, to civil.Date) ([]Adjustment, error)
	Adjustment(ctx context.Context, companyID, id uuid.UUID) (AdjustmentDetail, error)
	CreateAdjustment(ctx context.Context, companyID uuid.UUID, in AdjustmentInput, actor audit.Actor) (AdjustmentDetail, error)
	VoidAdjustment(ctx context.Context, companyID, id uuid.UUID, date civil.Date, reason string, actor audit.Actor) (AdjustmentDetail, error)
}

type Deps struct {
	Pool   *pgxpool.Pool
	Ledger ledger.Service
}

func New(d Deps) Service { return &service{d} }

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

func (s *service) accounts(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]ledger.Account, error) {
	list, err := s.Ledger.Accounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]ledger.Account, len(list))
	for _, a := range list {
		m[a.ID] = a
	}
	return m, nil
}

var codePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,39}$`)

// Kelas akun yang boleh dipakai di produk.
var (
	revenueClasses = map[int]bool{4: true, 7: true}
	cogsClasses    = map[int]bool{5: true, 6: true, 8: true}
	costClasses    = map[int]bool{1: true, 5: true, 6: true, 8: true}
)

func (s *service) validateProduct(ctx context.Context, companyID, id uuid.UUID, in *ProductInput) error {
	in.Code, in.Name, in.Unit = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.Unit)
	f := map[string]string{}
	if !codePattern.MatchString(in.Code) {
		f["code"] = "Kode 1–40 karakter: huruf, angka, titik, garis miring, atau tanda hubung."
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		f["name"] = "Nama wajib diisi (maksimal 200 karakter)."
	}
	if n := utf8.RuneCountInString(in.Unit); n < 1 || n > 20 {
		f["unit"] = "Satuan wajib diisi, mis. pcs, kg, m³ (maksimal 20 karakter)."
	}
	if in.Kind != KindGoods && in.Kind != KindService {
		f["kind"] = "Pilih barang atau jasa."
	}
	for key, a := range map[string]money.Amount{"salePrice": in.SalePrice, "purchasePrice": in.PurchasePrice} {
		if a.Sign() < 0 {
			f[key] = "Harga tidak boleh negatif."
		}
	}
	if in.Kind == KindService {
		in.InventoryAccountID = nil
	}
	if in.Kind == KindGoods {
		if in.InventoryAccountID == nil {
			f["inventoryAccountId"] = "Barang wajib punya akun persediaan."
		}
		if in.ExpenseAccountID == nil {
			f["expenseAccountId"] = "Barang wajib punya akun HPP."
		}
	}
	if len(f) > 0 {
		return httpx.Unprocessable("product.invalid", firstOf(f)).WithFields(f)
	}

	taken, err := s.q(ctx).ProductCodeTaken(ctx, store.ProductCodeTakenParams{CompanyID: companyID, Code: in.Code, ID: id})
	if err != nil {
		return err
	}
	if taken {
		msg := fmt.Sprintf("Kode %s sudah dipakai produk lain.", in.Code)
		return httpx.Conflict("product.code_taken", msg).WithFields(map[string]string{"code": msg})
	}

	accounts, err := s.accounts(ctx, companyID)
	if err != nil {
		return err
	}
	check := func(key string, id *uuid.UUID, classes map[int]bool, what string) {
		if id == nil {
			return
		}
		a, ok := accounts[*id]
		switch {
		case !ok:
			f[key] = "Akun tidak ditemukan di PT ini."
		case !a.Postable || !a.Active:
			f[key] = fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name)
		case !classes[a.Class]:
			f[key] = fmt.Sprintf("%s %s tidak bisa dipakai sebagai %s.", a.Code, a.Name, what)
		}
	}
	check("revenueAccountId", in.RevenueAccountID, revenueClasses, "akun pendapatan (pilih akun berkepala 4 atau 7)")
	if in.Kind == KindGoods {
		check("expenseAccountId", in.ExpenseAccountID, cogsClasses, "akun HPP (pilih akun berkepala 5, 6, atau 8)")
		check("inventoryAccountId", in.InventoryAccountID, map[int]bool{1: true}, "akun persediaan (pilih akun aset berkepala 1)")
	} else {
		check("expenseAccountId", in.ExpenseAccountID, costClasses, "akun beban")
	}
	if len(f) > 0 {
		return httpx.Unprocessable("product.invalid", firstOf(f)).WithFields(f)
	}
	return nil
}

func firstOf(f map[string]string) string {
	for _, k := range []string{"code", "name", "kind", "unit", "salePrice", "purchasePrice", "revenueAccountId", "expenseAccountId", "inventoryAccountId"} {
		if m, ok := f[k]; ok {
			return m
		}
	}
	for _, m := range f {
		return m
	}
	return ""
}

func toProduct(p store.InventoryProduct, qty, value money.Amount) Product {
	out := Product{
		ID: p.ID, Code: p.Code, Name: p.Name, Kind: p.Kind, Unit: p.Unit, SalePrice: p.SalePrice,
		PurchasePrice: p.PurchasePrice, RevenueAccountID: p.RevenueAccountID, ExpenseAccountID: p.ExpenseAccountID,
		InventoryAccountID: p.InventoryAccountID, Active: p.Active, Version: int(p.Version),
		StockQty: qty, StockValue: value,
	}
	out.AvgCost = avgCost(qty, value)
	return out
}

// avgCost adalah biaya rata-rata per unit dengan 4 desimal; 0 bila stok habis.
func avgCost(qty, value money.Amount) money.Amount {
	if qty.Sign() <= 0 {
		return money.Amount{}
	}
	return value.MulDiv(money.FromInt(1), qty, 4, money.HalfUp)
}

func (s *service) Products(ctx context.Context, companyID uuid.UUID, f ProductFilter) ([]Product, error) {
	p := store.ListProductsParams{CompanyID: companyID}
	if f.Kind != "" {
		p.Kind = &f.Kind
	}
	if !f.IncludeInactive {
		active := true
		p.Active = &active
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		p.Search = &q
	}
	rows, err := s.q(ctx).ListProducts(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]Product, len(rows))
	for i, r := range rows {
		out[i] = toProduct(store.InventoryProduct{
			ID: r.ID, CompanyID: r.CompanyID, Code: r.Code, Name: r.Name, Kind: r.Kind, Unit: r.Unit,
			SalePrice: r.SalePrice, PurchasePrice: r.PurchasePrice, RevenueAccountID: r.RevenueAccountID,
			ExpenseAccountID: r.ExpenseAccountID, InventoryAccountID: r.InventoryAccountID, Active: r.Active,
			Version: r.Version,
		}, r.StockQty, r.StockValue)
	}
	return out, nil
}

func (s *service) Product(ctx context.Context, companyID, id uuid.UUID) (Product, error) {
	m, err := s.ProductsByIDs(ctx, companyID, []uuid.UUID{id})
	if err != nil {
		return Product{}, err
	}
	p, ok := m[id]
	if !ok {
		return Product{}, httpx.NotFound("product.not_found", "Produk tidak ditemukan.")
	}
	return p, nil
}

// ProductsByIDs mengembalikan produk beserta saldo stok terkininya.
func (s *service) ProductsByIDs(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Product, error) {
	out := map[uuid.UUID]Product{}
	if len(ids) == 0 {
		return out, nil
	}
	q := s.q(ctx)
	rows, err := q.ProductsByIDs(ctx, store.ProductsByIDsParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	stock, err := q.LockFreeStock(ctx, store.LockFreeStockParams{CompanyID: companyID, ProductIds: ids})
	if err != nil {
		return nil, err
	}
	levels := map[uuid.UUID]store.InventoryStock{}
	for _, st := range stock {
		levels[st.ProductID] = st
	}
	for _, r := range rows {
		st := levels[r.ID]
		out[r.ID] = toProduct(r, st.Qty, st.Value)
	}
	return out, nil
}

func (s *service) CreateProduct(ctx context.Context, companyID uuid.UUID, in ProductInput, actor audit.Actor) (Product, error) {
	var out Product
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		id := uuid.Must(uuid.NewV7())
		if err := s.validateProduct(ctx, companyID, id, &in); err != nil {
			return err
		}
		if err := s.q(ctx).InsertProduct(ctx, store.InsertProductParams{
			ID: id, CompanyID: companyID, Code: in.Code, Name: in.Name, Kind: in.Kind, Unit: in.Unit,
			SalePrice: in.SalePrice, PurchasePrice: in.PurchasePrice, RevenueAccountID: in.RevenueAccountID,
			ExpenseAccountID: in.ExpenseAccountID, InventoryAccountID: in.InventoryAccountID, Active: true,
			CreatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "inventory.product.create",
			ObjectType: "product", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"code": in.Code, "name": in.Name, "kind": in.Kind},
		}); err != nil {
			return err
		}
		var err error
		out, err = s.Product(ctx, companyID, id)
		return err
	})
	return out, err
}

func (s *service) UpdateProduct(ctx context.Context, companyID, id uuid.UUID, in ProductInput, actor audit.Actor) (Product, error) {
	var out Product
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		before, err := s.q(ctx).GetProduct(ctx, store.GetProductParams{CompanyID: companyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("product.not_found", "Produk tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if err := s.validateProduct(ctx, companyID, id, &in); err != nil {
			return err
		}
		if in.Kind != before.Kind || !sameID(in.InventoryAccountID, before.InventoryAccountID) {
			moved, err := s.q(ctx).HasMovements(ctx, store.HasMovementsParams{CompanyID: companyID, ProductID: id})
			if err != nil {
				return err
			}
			if moved {
				return httpx.Unprocessable("product.has_movements",
					"Produk ini sudah punya pergerakan stok, sehingga jenis dan akun persediaannya tidak bisa diubah. Buat produk baru bila perlu.")
			}
		}
		n, err := s.q(ctx).UpdateProduct(ctx, store.UpdateProductParams{
			CompanyID: companyID, ID: id, Version: int32(in.Version), Code: in.Code, Name: in.Name, Kind: in.Kind,
			Unit: in.Unit, SalePrice: in.SalePrice, PurchasePrice: in.PurchasePrice,
			RevenueAccountID: in.RevenueAccountID, ExpenseAccountID: in.ExpenseAccountID,
			InventoryAccountID: in.InventoryAccountID, Active: in.Active,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return httpx.Conflict("conflict.version", "Produk ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "inventory.product.update",
			ObjectType: "product", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"code": before.Code, "name": before.Name, "active": before.Active, "salePrice": before.SalePrice.String()},
			After:  map[string]any{"code": in.Code, "name": in.Name, "active": in.Active, "salePrice": in.SalePrice.String()},
		}); err != nil {
			return err
		}
		out, err = s.Product(ctx, companyID, id)
		return err
	})
	return out, err
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *service) InventoryAccounts(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]bool, error) {
	ids, err := s.q(ctx).InventoryAccountIDs(ctx, companyID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// FormatQty menulis kuantitas tanpa nol di belakang koma, dengan koma desimal: 2,5.
func FormatQty(a money.Amount) string {
	s := a.String()
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	whole, frac, _ := strings.Cut(s, ".")
	neg := strings.HasPrefix(whole, "-")
	whole = strings.TrimPrefix(whole, "-")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "," + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}
