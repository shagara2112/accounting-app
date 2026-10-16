package imports

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/sheet"
)

func col(key string, required bool, names ...string) sheet.Column {
	return sheet.Column{Key: key, Names: names, Required: required}
}

// Kontak ---------------------------------------------------------------------------------

var contactKind = kind{
	KindInfo: KindInfo{Kind: "kontak", Title: "Kontak", Perm: rbac.ContactCreate},
	notes:    []string{"Jenis: pelanggan, pemasok, atau keduanya. PKP: ya atau tidak. Kontak dengan nama yang sudah ada dilewati."},
	columns: []sheet.Column{
		col("name", true, "Nama*", "Nama", "Nama kontak"),
		col("role", true, "Jenis*", "Jenis", "Peran"),
		col("npwp", false, "NPWP"),
		col("nik", false, "NIK"),
		col("nitku", false, "NITKU"),
		col("pkp", false, "PKP"),
		col("address", false, "Alamat"),
		col("email", false, "Email"),
		col("phone", false, "Telepon", "No. telepon", "HP"),
		col("terms", false, "Termin (hari)", "Termin"),
		col("bank", false, "Bank", "Nama bank"),
		col("account", false, "Nomor rekening", "No. rekening", "Rekening"),
	},
	example: func(context.Context, *service, uuid.UUID) [][]string {
		return [][]string{{"PT Contoh Pelanggan", "pelanggan", "0123456789012345", "", "", "ya", "Jl. Contoh No. 1, Jakarta", "keuangan@contoh.co.id", "021-5550000", "30", "", ""}}
	},
	apply: applyContacts,
}

var contactFields = map[string]string{
	"name": "name", "npwp": "npwp", "nik": "nik", "nitku": "nitku", "address": "address", "email": "email",
	"phone": "phone", "paymentTermsDays": "terms", "bankName": "bank", "bankAccount": "account",
}

func applyContacts(ctx context.Context, s *service, j *job) error {
	existing, err := s.Core.Contacts(ctx, j.companyID, core.ContactFilter{IncludeInactive: true})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range existing {
		seen[strings.ToLower(c.Name)] = true
	}
	created := 0
	for _, r := range j.table.Rows {
		name, ok := j.required(r, "name")
		if !ok {
			continue
		}
		if seen[strings.ToLower(name)] {
			j.warn(r.Line, "name", fmt.Sprintf("Kontak %q sudah ada; baris dilewati.", name))
			continue
		}
		in := core.ContactInput{
			Name: name, NPWP: r.Get("npwp"), NIK: r.Get("nik"), NITKU: r.Get("nitku"), Address: r.Get("address"),
			Email: r.Get("email"), Phone: r.Get("phone"), BankName: r.Get("bank"), BankAccount: r.Get("account"), Active: true,
		}
		switch strings.ToLower(r.Get("role")) {
		case "pelanggan", "customer":
			in.IsCustomer = true
		case "pemasok", "supplier", "vendor":
			in.IsSupplier = true
		case "keduanya", "pelanggan dan pemasok", "both":
			in.IsCustomer, in.IsSupplier = true, true
		default:
			j.fail(r.Line, "role", "Isi pelanggan, pemasok, atau keduanya.")
			continue
		}
		var err error
		if in.PKP, err = sheet.Bool(r.Get("pkp"), false); err != nil {
			j.fail(r.Line, "pkp", err.Error())
			continue
		}
		if in.PaymentTermsDays, err = sheet.Int(r.Get("terms"), 30); err != nil {
			j.fail(r.Line, "terms", err.Error())
			continue
		}
		if err := s.savepoint(ctx, func() error {
			_, err := s.Core.CreateContact(ctx, j.companyID, in, j.actor)
			return err
		}); err != nil {
			j.failErr(err, []int{r.Line}, contactFields)
			continue
		}
		seen[strings.ToLower(name)] = true
		created++
	}
	j.summary = append(j.summary, plural(created, "kontak baru"))
	return nil
}

// Produk ---------------------------------------------------------------------------------

var productKind = kind{
	KindInfo: KindInfo{Kind: "produk", Title: "Produk dan jasa", Perm: rbac.InventoryCreate},
	notes: []string{
		"Jenis: barang atau jasa. Akun diisi dengan kode akun di bagan akun PT ini.",
		"Barang wajib punya akun persediaan dan akun HPP. Produk dengan kode yang sudah ada dilewati.",
	},
	columns: []sheet.Column{
		col("code", true, "Kode*", "Kode", "Kode produk"),
		col("name", true, "Nama*", "Nama", "Nama produk"),
		col("kind", true, "Jenis*", "Jenis"),
		col("unit", true, "Satuan*", "Satuan"),
		col("sale", false, "Harga jual"),
		col("purchase", false, "Harga beli"),
		col("revenue", false, "Akun pendapatan"),
		col("expense", false, "Akun HPP/beban", "Akun HPP", "Akun beban"),
		col("inventory", false, "Akun persediaan"),
	},
	example: func(ctx context.Context, s *service, companyID uuid.UUID) [][]string {
		inv, hpp, rev := codeOf(ctx, s, companyID, "1-1302", "1-1301"), codeOf(ctx, s, companyID, "5-1001"), codeOf(ctx, s, companyID, "4-1001")
		return [][]string{{"BRG-001", "Contoh barang", "barang", "pcs", "15000", "10000", rev, hpp, inv}}
	},
	apply: applyProducts,
}

// codeOf mengembalikan kode pertama yang ada di bagan akun PT, untuk contoh template.
func codeOf(ctx context.Context, s *service, companyID uuid.UUID, codes ...string) string {
	m, err := s.accountsByCode(ctx, companyID)
	if err != nil {
		return ""
	}
	for _, c := range codes {
		if a, ok := m[c]; ok && a.Postable {
			return c
		}
	}
	return ""
}

var productFields = map[string]string{
	"code": "code", "name": "name", "kind": "kind", "unit": "unit", "salePrice": "sale", "purchasePrice": "purchase",
	"revenueAccountId": "revenue", "expenseAccountId": "expense", "inventoryAccountId": "inventory",
}

func applyProducts(ctx context.Context, s *service, j *job) error {
	accounts, err := s.accountsByCode(ctx, j.companyID)
	if err != nil {
		return err
	}
	existing, err := s.Inventory.Products(ctx, j.companyID, inventory.ProductFilter{IncludeInactive: true})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range existing {
		seen[strings.ToUpper(p.Code)] = true
	}
	acc := func(r sheet.Row, key string) (*uuid.UUID, bool) {
		code := r.Get(key)
		if code == "" {
			return nil, true
		}
		a, ok := accounts[code]
		if !ok {
			j.fail(r.Line, key, fmt.Sprintf("Akun %s tidak ada di bagan akun.", code))
			return nil, false
		}
		id := a.ID
		return &id, true
	}
	created := 0
	for _, r := range j.table.Rows {
		code, ok1 := j.required(r, "code")
		name, ok2 := j.required(r, "name")
		unit, ok3 := j.required(r, "unit")
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		if seen[strings.ToUpper(code)] {
			j.warn(r.Line, "code", fmt.Sprintf("Produk %s sudah ada; baris dilewati.", code))
			continue
		}
		in := inventory.ProductInput{Code: code, Name: name, Unit: unit, Active: true}
		switch strings.ToLower(r.Get("kind")) {
		case "barang", "goods":
			in.Kind = inventory.KindGoods
		case "jasa", "service":
			in.Kind = inventory.KindService
		default:
			j.fail(r.Line, "kind", "Isi barang atau jasa.")
			continue
		}
		sale, ok4 := j.amount(r, "sale")
		buy, ok5 := j.amount(r, "purchase")
		rev, ok6 := acc(r, "revenue")
		exp, ok7 := acc(r, "expense")
		inv, ok8 := acc(r, "inventory")
		if !ok4 || !ok5 || !ok6 || !ok7 || !ok8 {
			continue
		}
		in.SalePrice, in.PurchasePrice, in.RevenueAccountID, in.ExpenseAccountID = sale, buy, rev, exp
		if in.Kind == inventory.KindGoods {
			in.InventoryAccountID = inv
		}
		if err := s.savepoint(ctx, func() error {
			_, err := s.Inventory.CreateProduct(ctx, j.companyID, in, j.actor)
			return err
		}); err != nil {
			j.failErr(err, []int{r.Line}, productFields)
			continue
		}
		seen[strings.ToUpper(code)] = true
		created++
	}
	j.summary = append(j.summary, plural(created, "produk baru"))
	return nil
}

// Neraca saldo awal (GL-06) --------------------------------------------------------------

var openingKind = kind{
	KindInfo: KindInfo{Kind: "saldo-awal", Title: "Neraca saldo awal", Perm: rbac.JournalCreate, NeedDate: true},
	notes: []string{
		"Saldo per akun pada tanggal saldo awal, dari neraca saldo aplikasi lama. Isi debit atau kredit, bukan keduanya.",
		"Piutang usaha, utang usaha, dan akun persediaan dilewati: impor per pelanggan, pemasok, dan barang lewat impor saldo awal piutang, utang, dan stok.",
		"Selisihnya masuk akun ekuitas saldo awal, yang menjadi nol setelah semua saldo awal selesai diimpor.",
	},
	columns: []sheet.Column{
		col("code", true, "Kode akun*", "Kode akun", "Kode"),
		col("name", false, "Nama akun"),
		col("debit", false, "Debit"),
		col("credit", false, "Kredit"),
	},
	example: func(ctx context.Context, s *service, companyID uuid.UUID) [][]string {
		return [][]string{
			{codeOf(ctx, s, companyID, "1-1111"), "Bank", "250000000", ""},
			{codeOf(ctx, s, companyID, "3-1001"), "Modal disetor", "", "250000000"},
		}
	},
	apply: applyOpening,
}

func applyOpening(ctx context.Context, s *service, j *job) error {
	if prev, err := s.Ledger.EntriesBySource(ctx, j.companyID, ledger.SourceOpening); err != nil {
		return err
	} else if len(prev) > 0 {
		j.fail(j.table.Rows[0].Line, "", fmt.Sprintf("Neraca saldo awal sudah diimpor (%s per %s). Void jurnal itu dulu bila ingin mengimpor ulang.",
			prev[0].Number, prev[0].Date.Indonesian()))
		return nil
	}
	accounts, err := s.accountsByCode(ctx, j.companyID)
	if err != nil {
		return err
	}
	controlled, err := s.controlledAccounts(ctx, j.companyID)
	if err != nil {
		return err
	}
	opening, err := s.Ledger.RoleAccount(ctx, j.companyID, ledger.OpeningEquity)
	if err != nil {
		return err
	}
	var lines []ledger.LineDraft
	var rows []int
	used := map[uuid.UUID]int{}
	var net money.Amount
	for _, r := range j.table.Rows {
		code, ok := j.required(r, "code")
		if !ok {
			continue
		}
		debit, ok1 := j.amount(r, "debit")
		credit, ok2 := j.amount(r, "credit")
		if !ok1 || !ok2 {
			continue
		}
		a, ok := accounts[code]
		switch {
		case !ok:
			j.fail(r.Line, "code", fmt.Sprintf("Akun %s tidak ada di bagan akun.", code))
			continue
		case !a.Postable || !a.Active:
			j.fail(r.Line, "code", fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name))
			continue
		case debit.Sign() < 0 || credit.Sign() < 0:
			j.fail(r.Line, "debit", "Debit dan kredit tidak boleh negatif.")
			continue
		case debit.Sign() > 0 && credit.Sign() > 0:
			j.fail(r.Line, "credit", "Isi debit atau kredit, bukan keduanya.")
			continue
		case debit.IsZero() && credit.IsZero():
			continue
		}
		if why, ok := controlled[a.ID]; ok {
			j.warn(r.Line, "code", fmt.Sprintf("%s %s dilewati: %s", a.Code, a.Name, why))
			continue
		}
		if a.ID == opening.ID {
			j.warn(r.Line, "code", fmt.Sprintf("%s %s dilewati: akun ini diisi otomatis dari selisih saldo awal.", a.Code, a.Name))
			continue
		}
		if prev, dup := used[a.ID]; dup {
			j.fail(r.Line, "code", fmt.Sprintf("Akun %s sudah ada di baris %d.", a.Code, prev))
			continue
		}
		used[a.ID] = r.Line
		lines = append(lines, ledger.LineDraft{AccountID: a.ID, Debit: debit, Credit: credit, Memo: "Saldo awal"})
		rows = append(rows, r.Line)
		net = net.Add(debit).Sub(credit)
	}
	if j.failed() || len(lines) == 0 {
		if len(lines) == 0 && !j.failed() {
			j.fail(j.table.Rows[0].Line, "", "Tidak ada saldo yang bisa diimpor.")
		}
		return nil
	}
	if !net.IsZero() {
		l := ledger.LineDraft{AccountID: opening.ID, Memo: "Selisih saldo awal (diisi impor piutang, utang, dan stok)"}
		if net.Sign() > 0 {
			l.Credit = net
		} else {
			l.Debit = net.Neg()
		}
		lines = append(lines, l)
	}
	err = s.savepoint(ctx, func() error {
		number, err := s.Ledger.NextNumber(ctx, j.companyID, "SA", j.opts.Date)
		if err != nil {
			return err
		}
		_, err = s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: j.companyID, Kind: ledger.General, Date: j.opts.Date, Memo: "Saldo awal per " + j.opts.Date.Indonesian(),
			SourceType: ledger.SourceOpening, Number: number, Lines: lines, Actor: j.actor,
		})
		if err == nil {
			j.summary = append(j.summary, fmt.Sprintf("Jurnal saldo awal %s: %d akun", number, len(rows)))
		}
		return err
	})
	if err != nil {
		j.failErr(err, rows, map[string]string{"accountId": "code", "debit": "debit", "credit": "credit"})
	}
	if !net.IsZero() && err == nil {
		j.summary = append(j.summary, fmt.Sprintf("Selisih %s ke %s %s", net.Abs().IDR(), opening.Code, opening.Name))
	}
	return nil
}

// controlledAccounts adalah akun yang saldonya harus berasal dari dokumen, bukan neraca saldo.
func (s *service) controlledAccounts(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	for role, why := range map[ledger.Role]string{
		ledger.Receivable: "impor per faktur lewat saldo awal piutang.",
		ledger.Payable:    "impor per tagihan lewat saldo awal utang.",
	} {
		a, err := s.Ledger.RoleAccount(ctx, companyID, role)
		if err != nil {
			return nil, err
		}
		out[a.ID] = why
	}
	inv, err := s.Inventory.InventoryAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for id := range inv {
		out[id] = "impor per barang lewat saldo awal stok."
	}
	return out, nil
}

// Saldo awal piutang dan utang per dokumen -----------------------------------------------

var receivableKind = kind{
	KindInfo: KindInfo{Kind: "piutang-awal", Title: "Saldo awal piutang", Perm: rbac.SalesCreate},
	notes: []string{
		"Satu baris per faktur yang belum lunas di aplikasi lama. Pelanggan diisi nama persis atau NPWP-nya; impor kontak dulu bila belum ada.",
		"Faktur saldo awal tanpa PPN (PPN-nya sudah dilaporkan di masa asalnya) dan bisa langsung dilunasi.",
	},
	columns: openingDocColumns("Pelanggan*", "Nomor faktur*", "Tanggal faktur*", "Sisa piutang*"),
	example: func(context.Context, *service, uuid.UUID) [][]string {
		return [][]string{{"PT Contoh Pelanggan", "INV/2026/0912", "15/09/2026", "15/10/2026", "11100000", ""}}
	},
	apply: func(ctx context.Context, s *service, j *job) error { return applyOpeningDocs(ctx, s, j, true) },
}

var payableKind = kind{
	KindInfo: KindInfo{Kind: "utang-awal", Title: "Saldo awal utang", Perm: rbac.PurchaseCreate},
	notes: []string{
		"Satu baris per tagihan pemasok yang belum dibayar. Pemasok diisi nama persis atau NPWP-nya; impor kontak dulu bila belum ada.",
		"Tagihan saldo awal tanpa PPN dan PPh, dan bisa langsung dibayar.",
	},
	columns: openingDocColumns("Pemasok*", "Nomor faktur pemasok*", "Tanggal tagihan*", "Sisa utang*"),
	example: func(context.Context, *service, uuid.UUID) [][]string {
		return [][]string{{"CV Contoh Pemasok", "SUP-0456", "20/09/2026", "20/10/2026", "5550000", ""}}
	},
	apply: func(ctx context.Context, s *service, j *job) error { return applyOpeningDocs(ctx, s, j, false) },
}

func openingDocColumns(contact, number, date, amount string) []sheet.Column {
	return []sheet.Column{
		col("contact", true, contact, strings.TrimSuffix(contact, "*")),
		col("number", true, number, strings.TrimSuffix(number, "*"), "Nomor"),
		col("date", true, date, strings.TrimSuffix(date, "*"), "Tanggal"),
		col("due", false, "Jatuh tempo"),
		col("amount", true, amount, strings.TrimSuffix(amount, "*"), "Sisa"),
		col("memo", false, "Catatan"),
	}
}

var digitsOnly = regexp.MustCompile(`\D`)

func applyOpeningDocs(ctx context.Context, s *service, j *job, receivable bool) error {
	role, roleName := "supplier", "pemasok"
	if receivable {
		role, roleName = "customer", "pelanggan"
	}
	contacts, err := s.Core.Contacts(ctx, j.companyID, core.ContactFilter{Role: role})
	if err != nil {
		return err
	}
	byName, byNPWP := map[string]uuid.UUID{}, map[string]uuid.UUID{}
	for _, c := range contacts {
		byName[strings.ToLower(c.Name)] = c.ID
		if c.NPWP != "" {
			byNPWP[digitsOnly.ReplaceAllString(c.NPWP, "")] = c.ID
		}
	}
	created := 0
	var total money.Amount
	for _, r := range j.table.Rows {
		name, ok1 := j.required(r, "contact")
		number, ok2 := j.required(r, "number")
		date, ok3 := j.date(r, "date", true)
		due, ok4 := j.date(r, "due", false)
		amt, ok5 := j.amount(r, "amount")
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
			continue
		}
		id, ok := byName[strings.ToLower(name)]
		if !ok {
			id, ok = byNPWP[digitsOnly.ReplaceAllString(name, "")]
		}
		if !ok {
			j.fail(r.Line, "contact", fmt.Sprintf("%s %q belum ada atau bukan %s; impor kontak dulu.", strings.ToUpper(roleName[:1])+roleName[1:], name, roleName))
			continue
		}
		if amt.Sign() <= 0 {
			j.fail(r.Line, "amount", "Sisa harus lebih dari 0.")
			continue
		}
		fields := map[string]string{"customerId": "contact", "supplierId": "contact", "date": "date", "dueDate": "due", "amount": "amount", "memo": "memo", "supplierInvoiceNo": "number"}
		if err := s.savepoint(ctx, func() error {
			if receivable {
				_, err := s.Sales.CreateOpeningInvoice(ctx, j.companyID, sales.OpeningInput{
					ContactID: id, Reference: number, Date: date, DueDate: due, Amount: amt, Memo: r.Get("memo"),
				}, j.actor)
				return err
			}
			_, err := s.Purchase.CreateOpeningBill(ctx, j.companyID, purchase.OpeningInput{
				ContactID: id, Reference: number, Date: date, DueDate: due, Amount: amt, Memo: r.Get("memo"),
			}, j.actor)
			return err
		}); err != nil {
			j.failErr(err, []int{r.Line}, fields)
			continue
		}
		created++
		total = total.Add(amt)
	}
	what := "tagihan saldo awal"
	if receivable {
		what = "faktur saldo awal"
	}
	j.summary = append(j.summary, fmt.Sprintf("%s, total %s", plural(created, what), total.IDR()))
	return nil
}

// Saldo awal stok ------------------------------------------------------------------------

var stockKind = kind{
	KindInfo: KindInfo{Kind: "stok-awal", Title: "Saldo awal stok", Perm: rbac.InventoryCreate, NeedDate: true},
	notes: []string{
		"Kuantitas dan biaya satuan per barang pada tanggal saldo awal. Impor produk dulu bila belum ada.",
		"Stok awal dicatat sebagai satu dokumen penyesuaian yang melawan akun ekuitas saldo awal.",
	},
	columns: []sheet.Column{
		col("code", true, "Kode produk*", "Kode produk", "Kode"),
		col("qty", true, "Kuantitas*", "Kuantitas", "Qty"),
		col("cost", true, "Biaya satuan*", "Biaya satuan", "HPP satuan"),
	},
	example: func(context.Context, *service, uuid.UUID) [][]string { return [][]string{{"BRG-001", "120", "10000"}} },
	apply:   applyStock,
}

func applyStock(ctx context.Context, s *service, j *job) error {
	products, err := s.Inventory.Products(ctx, j.companyID, inventory.ProductFilter{IncludeInactive: true})
	if err != nil {
		return err
	}
	byCode := map[string]inventory.Product{}
	for _, p := range products {
		byCode[strings.ToUpper(p.Code)] = p
	}
	opening, err := s.Ledger.RoleAccount(ctx, j.companyID, ledger.OpeningEquity)
	if err != nil {
		return err
	}
	var lines []inventory.AdjustmentLineInput
	var rows []int
	used := map[uuid.UUID]bool{}
	var value money.Amount
	for _, r := range j.table.Rows {
		code, ok1 := j.required(r, "code")
		qty, ok2 := j.amount(r, "qty")
		cost, ok3 := j.amount(r, "cost")
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		p, ok := byCode[strings.ToUpper(code)]
		switch {
		case !ok:
			j.fail(r.Line, "code", fmt.Sprintf("Produk %s belum ada; impor produk dulu.", code))
			continue
		case !p.Goods():
			j.fail(r.Line, "code", fmt.Sprintf("%s adalah jasa, tidak punya stok.", code))
			continue
		case used[p.ID]:
			j.fail(r.Line, "code", fmt.Sprintf("Produk %s muncul dua kali.", code))
			continue
		case qty.Sign() <= 0:
			j.fail(r.Line, "qty", "Kuantitas harus lebih dari 0.")
			continue
		case cost.Sign() < 0:
			j.fail(r.Line, "cost", "Biaya satuan tidak boleh negatif.")
			continue
		}
		used[p.ID] = true
		c := cost
		lines = append(lines, inventory.AdjustmentLineInput{ProductID: p.ID, Qty: qty, UnitCost: &c, Memo: "Stok awal"})
		rows = append(rows, r.Line)
		value = value.Add(qty.Mul(cost, 0, money.HalfUp))
	}
	if j.failed() || len(lines) == 0 {
		return nil
	}
	err = s.savepoint(ctx, func() error {
		d, err := s.Inventory.CreateAdjustment(ctx, j.companyID, inventory.AdjustmentInput{
			Kind: "penyesuaian", Date: j.opts.Date, AccountID: opening.ID, Memo: "Stok awal per " + j.opts.Date.Indonesian(), Lines: lines,
		}, j.actor)
		if err == nil {
			j.summary = append(j.summary, fmt.Sprintf("Dokumen stok awal %s: %d barang, nilai sekitar %s", d.Number, len(lines), value.IDR()))
		}
		return err
	})
	if err != nil {
		j.failErr(err, rows, map[string]string{"productId": "code", "qty": "qty", "unitCost": "cost"})
	}
	return nil
}

// Jurnal umum ----------------------------------------------------------------------------

var journalKind = kind{
	KindInfo: KindInfo{Kind: "jurnal", Title: "Jurnal umum", Perm: rbac.JournalCreate},
	notes: []string{
		"Baris dengan tanggal dan nomor bukti yang sama menjadi satu jurnal; tiap jurnal harus seimbang.",
		"Jurnal diposting dengan nomor seri JU Lajur; nomor bukti lama dicatat di keterangannya.",
	},
	columns: []sheet.Column{
		col("date", true, "Tanggal*", "Tanggal"),
		col("ref", true, "Nomor bukti*", "Nomor bukti", "No. bukti", "Referensi"),
		col("code", true, "Kode akun*", "Kode akun"),
		col("memo", false, "Keterangan"),
		col("debit", false, "Debit"),
		col("credit", false, "Kredit"),
	},
	example: func(ctx context.Context, s *service, companyID uuid.UUID) [][]string {
		return [][]string{
			{"01/10/2026", "MEMO-001", codeOf(ctx, s, companyID, "6-1001", "6-2001"), "Beban bulan Oktober", "1500000", ""},
			{"01/10/2026", "MEMO-001", codeOf(ctx, s, companyID, "1-1111"), "Beban bulan Oktober", "", "1500000"},
		}
	},
	apply: applyJournals,
}

func applyJournals(ctx context.Context, s *service, j *job) error {
	accounts, err := s.accountsByCode(ctx, j.companyID)
	if err != nil {
		return err
	}
	type group struct {
		date  civil.Date
		ref   string
		memo  string
		lines []ledger.LineDraft
		rows  []int
	}
	var groups []*group
	index := map[string]*group{}
	for _, r := range j.table.Rows {
		date, ok1 := j.date(r, "date", true)
		ref, ok2 := j.required(r, "ref")
		code, ok3 := j.required(r, "code")
		debit, ok4 := j.amount(r, "debit")
		credit, ok5 := j.amount(r, "credit")
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
			continue
		}
		a, ok := accounts[code]
		if !ok {
			j.fail(r.Line, "code", fmt.Sprintf("Akun %s tidak ada di bagan akun.", code))
			continue
		}
		key := date.String() + "|" + ref
		g, ok := index[key]
		if !ok {
			g = &group{date: date, ref: ref}
			index[key] = g
			groups = append(groups, g)
		}
		memo := r.Get("memo")
		if g.memo == "" {
			g.memo = memo
		}
		g.lines = append(g.lines, ledger.LineDraft{AccountID: a.ID, Debit: debit, Credit: credit, Memo: memo})
		g.rows = append(g.rows, r.Line)
	}
	if j.failed() {
		return nil
	}
	posted := 0
	for _, g := range groups {
		memo := g.memo
		if memo == "" {
			memo = "Impor jurnal"
		}
		memo += " · bukti " + g.ref
		err := s.savepoint(ctx, func() error {
			_, err := s.Ledger.Post(ctx, ledger.EntryDraft{
				CompanyID: j.companyID, Kind: ledger.General, Date: g.date, Memo: memo, Lines: g.lines, Actor: j.actor,
			})
			return err
		})
		if err != nil {
			j.failErr(err, g.rows, map[string]string{"accountId": "code", "debit": "debit", "credit": "credit", "memo": "memo", "date": "date"})
			continue
		}
		posted++
	}
	j.summary = append(j.summary, plural(posted, "jurnal diposting"))
	return nil
}

// Rekap penjualan harian retail (IND-07) -------------------------------------------------

var dailyKind = kind{
	KindInfo: KindInfo{Kind: "penjualan-harian", Title: "Penjualan harian retail", Perm: rbac.SalesCreate},
	notes: []string{
		"Satu baris per barang per hari per toko; baris dengan tanggal dan toko yang sama menjadi satu rekap dan satu jurnal.",
		"Nilai penjualan sudah termasuk PPN (PPN digunggung). Kode pajak kosong = PPN untuk PT PKP, NON-PPN untuk non-PKP.",
		"Dibayar ke: kode akun kas toko, bank, atau piutang kartu/QRIS. Barang keluar dari stok dengan HPP rata-rata.",
	},
	columns: []sheet.Column{
		col("date", true, "Tanggal*", "Tanggal"),
		col("store", false, "Toko"),
		col("branch", false, "Kode cabang", "Cabang"),
		col("product", false, "Kode produk"),
		col("account", false, "Akun pendapatan"),
		col("memo", false, "Keterangan"),
		col("qty", true, "Kuantitas*", "Kuantitas", "Qty"),
		col("amount", true, "Nilai penjualan*", "Nilai penjualan", "Penjualan"),
		col("tax", false, "Kode pajak"),
		col("paid", true, "Dibayar ke*", "Dibayar ke", "Akun penerimaan"),
	},
	example: func(ctx context.Context, s *service, companyID uuid.UUID) [][]string {
		cash := codeOf(ctx, s, companyID, "1-1103", "1-1101")
		return [][]string{
			{"01/10/2026", "Toko Pusat", "", "BRG-001", "", "", "12", "180000", "", cash},
			{"01/10/2026", "Toko Pusat", "", "BRG-002", "", "", "3", "75000", "", codeOf(ctx, s, companyID, "1-1203", "1-1111")},
		}
	},
	apply: applyDaily,
}

func applyDaily(ctx context.Context, s *service, j *job) error {
	accounts, err := s.accountsByCode(ctx, j.companyID)
	if err != nil {
		return err
	}
	products, err := s.Inventory.Products(ctx, j.companyID, inventory.ProductFilter{IncludeInactive: true})
	if err != nil {
		return err
	}
	byCode := map[string]inventory.Product{}
	for _, p := range products {
		byCode[strings.ToUpper(p.Code)] = p
	}
	company, err := s.Core.Company(ctx, j.companyID)
	if err != nil {
		return err
	}
	defaultTax := "NON-PPN"
	if company.PKP {
		defaultTax = "PPN"
	}
	branches, err := s.Core.Dimensions(ctx, j.companyID, core.DimBranch)
	if err != nil {
		return err
	}
	branchByCode := map[string]uuid.UUID{}
	for _, b := range branches {
		branchByCode[b.Code] = b.ID
	}
	type group struct {
		in   sales.DailySaleInput
		rows []int
		pay  map[uuid.UUID]int // indeks pembayaran per akun
	}
	var groups []*group
	index := map[string]*group{}
	for _, r := range j.table.Rows {
		date, ok1 := j.date(r, "date", true)
		qty, ok2 := j.amount(r, "qty")
		amt, ok3 := j.amount(r, "amount")
		paidCode, ok4 := j.required(r, "paid")
		if !ok1 || !ok2 || !ok3 || !ok4 {
			continue
		}
		line := sales.DailyLineInput{Description: r.Get("memo"), Qty: qty, Amount: amt, TaxCode: strings.ToUpper(r.Get("tax"))}
		if line.TaxCode == "" {
			line.TaxCode = defaultTax
		}
		if code := r.Get("product"); code != "" {
			p, ok := byCode[strings.ToUpper(code)]
			if !ok {
				j.fail(r.Line, "product", fmt.Sprintf("Produk %s belum ada; impor produk dulu.", code))
				continue
			}
			id := p.ID
			line.ProductID = &id
		}
		if code := r.Get("account"); code != "" {
			a, ok := accounts[code]
			if !ok {
				j.fail(r.Line, "account", fmt.Sprintf("Akun %s tidak ada di bagan akun.", code))
				continue
			}
			line.AccountID = a.ID
		}
		if line.ProductID == nil && line.AccountID == uuid.Nil {
			j.fail(r.Line, "product", "Isi kode produk atau akun pendapatan.")
			continue
		}
		pa, ok := accounts[paidCode]
		if !ok {
			j.fail(r.Line, "paid", fmt.Sprintf("Akun %s tidak ada di bagan akun.", paidCode))
			continue
		}
		var branch *uuid.UUID
		if code := strings.ToUpper(r.Get("branch")); code != "" {
			id, ok := branchByCode[code]
			if !ok {
				j.fail(r.Line, "branch", fmt.Sprintf("Cabang %s tidak ada.", code))
				continue
			}
			branch = &id
		}
		key := date.String() + "|" + strings.ToLower(r.Get("store"))
		g, ok := index[key]
		if !ok {
			g = &group{in: sales.DailySaleInput{Date: date, Store: r.Get("store"), BranchID: branch}, pay: map[uuid.UUID]int{}}
			index[key] = g
			groups = append(groups, g)
		}
		g.in.Lines = append(g.in.Lines, line)
		g.rows = append(g.rows, r.Line)
		if i, ok := g.pay[pa.ID]; ok {
			g.in.Payments[i].Amount = g.in.Payments[i].Amount.Add(amt)
		} else {
			g.pay[pa.ID] = len(g.in.Payments)
			g.in.Payments = append(g.in.Payments, sales.DailyPaymentInput{AccountID: pa.ID, Amount: amt})
		}
	}
	if j.failed() {
		return nil
	}
	posted := 0
	var total money.Amount
	for _, g := range groups {
		var out sales.DailySaleDetail
		err := s.savepoint(ctx, func() error {
			var err error
			out, err = s.Sales.CreateDailySale(ctx, j.companyID, g.in, j.actor)
			return err
		})
		if err != nil {
			j.failErr(err, g.rows, map[string]string{"productId": "product", "accountId": "account", "qty": "qty", "amount": "amount",
				"taxCode": "tax", "description": "memo", "date": "date", "store": "store", "payments": "paid"})
			continue
		}
		posted++
		total = total.Add(out.Total)
	}
	j.summary = append(j.summary, fmt.Sprintf("%s, total %s", plural(posted, "rekap harian"), total.IDR()))
	return nil
}
