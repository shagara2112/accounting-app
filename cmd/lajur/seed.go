//go:build !release

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/cash"
	"lajur/internal/modules/core"
	"lajur/internal/modules/inventory"
	"lajur/internal/modules/project"
	"lajur/internal/modules/purchase"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/config"
	"lajur/internal/platform/db"
	"lajur/internal/platform/money"
	"lajur/internal/platform/store"
	"lajur/internal/server"
)

func init() { extraCommands["seed-dev"] = seedDev }

// seedDev mengisi data contoh Grup Arta: tiga PT dan satu pengguna per persona.
// Kata sandi semua pengguna diambil dari LAJUR_DEV_SEED_PASSWORD. Aman dijalankan ulang.
func seedDev(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DevSeedPassword == "" {
		return errors.New("LAJUR_DEV_SEED_PASSWORD kosong; lihat .env.example")
	}
	deps, closeDB, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()
	hash, err := auth.HashPassword(cfg.DevSeedPassword)
	if err != nil {
		return err
	}

	return db.InTx(ctx, deps.Pool, pgx.TxOptions{}, func(ctx context.Context) error {
		existing, err := deps.Core.Companies(ctx)
		if err != nil {
			return err
		}
		byName := map[string]uuid.UUID{}
		for _, c := range existing {
			byName[c.Name] = c.ID
		}
		var companies []uuid.UUID
		industries := map[uuid.UUID]string{}
		for _, c := range []core.NewCompany{
			{Name: "PT Arta Konstruksi", ShortName: "Konstruksi", Industry: ptr("konstruksi")},
			{Name: "PT Arta Jasa Teknik", ShortName: "Jasa", Industry: ptr("jasa")},
			{Name: "PT Arta Retail Nusantara", ShortName: "Retail", Industry: ptr("retail")},
		} {
			id, ok := byName[c.Name]
			if !ok {
				created, err := deps.Core.CreateCompany(ctx, c)
				if err != nil {
					return err
				}
				id = created.ID
			}
			companies = append(companies, id)
			industries[id] = *c.Industry
		}

		q := store.New(db.Conn(ctx, deps.Pool))
		users := []struct{ email, name, role string }{
			{"pemilik@arta.test", "Pemilik Grup Arta", "pemilik"},
			{"manager@arta.test", "Manager Keuangan", "manager"},
			{"akuntansi@arta.test", "Staf Akuntansi", "staf_akuntansi"},
			{"pajak@arta.test", "Staf Pajak", "staf_pajak"},
			{"auditor@arta.test", "Auditor Eksternal", "auditor"},
			{"admin@arta.test", "Admin Pelanggan", "admin"},
		}
		ids := map[string]uuid.UUID{}
		for _, u := range users {
			row, err := q.GetUserByEmail(ctx, u.email)
			if errors.Is(err, pgx.ErrNoRows) {
				row, err = q.CreateUser(ctx, store.CreateUserParams{
					ID: uuid.Must(uuid.NewV7()), Email: u.email, Name: u.name, PasswordHash: hash,
				})
			}
			if err != nil {
				return err
			}
			for _, c := range companies {
				if err := deps.Core.Assign(ctx, row.ID, c, u.role); err != nil {
					return err
				}
			}
			ids[u.role] = row.ID
			fmt.Printf("  %-22s %s\n", u.email, u.role)
		}
		for _, c := range companies {
			if err := seedLedger(ctx, deps.Ledger, c, industries[c], ids["staf_akuntansi"], ids["manager"]); err != nil {
				return err
			}
			if err := seedTrade(ctx, deps, c, industries[c], audit.Actor{UserID: ids["staf_akuntansi"]}); err != nil {
				return fmt.Errorf("data penjualan dan pembelian: %w", err)
			}
			if err := seedTax(ctx, deps, c, industries[c], audit.Actor{UserID: ids["staf_pajak"]}); err != nil {
				return fmt.Errorf("data pajak: %w", err)
			}
			if err := seedProjectsInventory(ctx, deps, c, industries[c], audit.Actor{UserID: ids["staf_akuntansi"]}); err != nil {
				return fmt.Errorf("data proyek dan persediaan: %w", err)
			}
		}
		byIndustry := map[string]uuid.UUID{}
		for _, c := range companies {
			byIndustry[industries[c]] = c
		}
		if err := seedGroup(ctx, deps, byIndustry, audit.Actor{UserID: ids["staf_akuntansi"]}); err != nil {
			return fmt.Errorf("data antar-PT: %w", err)
		}
		fmt.Println("Data contoh Grup Arta siap. Kata sandi semua akun = LAJUR_DEV_SEED_PASSWORD.")
		return nil
	})
}

func ptr(s string) *string { return &s }

// seedLedger mengisi bagan akun sesuai industri, lalu jurnal tiga bulan terakhir: dua bulan
// lalu sudah dikunci, bulan lalu sedang tahap penyesuaian, dan bulan ini masih terbuka.
// PT yang sudah punya jurnal dilewati.
func seedLedger(ctx context.Context, l ledger.Service, company uuid.UUID, industry string, staf, manager uuid.UUID) error {
	accounts, err := l.Accounts(ctx, company)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		if _, err := l.ApplyTemplate(ctx, company, industry, ledger.Actor{UserID: staf}); err != nil {
			return err
		}
		if accounts, err = l.Accounts(ctx, company); err != nil {
			return err
		}
	}
	settings, err := l.Settings(ctx, company)
	if err != nil || settings.HasEntries {
		return err
	}
	code := map[string]uuid.UUID{}
	for _, a := range accounts {
		code[a.Code] = a.ID
	}

	scale := map[string]int64{"jasa": 1, "konstruksi": 3, "retail": 2}[industry]
	words := map[string][3]string{
		"jasa":       {"Faktur jasa konsultansi teknik", "Honor tenaga ahli proyek", "Pelunasan faktur jasa"},
		"konstruksi": {"Termin pembangunan gudang", "Pembelian material baja ringan dan semen", "Pembayaran termin dari pemberi kerja"},
		"retail":     {"Rekap penjualan harian toko", "HPP barang dagang terjual", "Setoran penjualan ke bank"},
	}[industry]
	rp := func(n int64) money.Amount { return money.FromInt(n * scale) }
	type ln struct {
		code          string
		debit, credit money.Amount
	}
	post := func(kind ledger.Kind, date civil.Date, memo string, autoReverse bool, lines ...ln) error {
		drafts := make([]ledger.LineDraft, len(lines))
		for i, x := range lines {
			drafts[i] = ledger.LineDraft{AccountID: code[x.code], Debit: x.debit, Credit: x.credit}
		}
		_, err := l.Post(ctx, ledger.EntryDraft{
			CompanyID: company, Kind: kind, Date: date, Memo: memo, AutoReverse: autoReverse,
			Lines: drafts, Actor: ledger.Actor{UserID: staf},
		})
		return err
	}
	var zero money.Amount
	month := civil.Today(civil.Jakarta).MonthStart().AddMonths(-2)
	today := civil.Today(civil.Jakarta)
	revenue := []int64{300_000_000, 340_000_000, 280_000_000}
	cost := []int64{120_000_000, 150_000_000, 110_000_000}

	if err := post(ledger.General, month, "Setoran modal awal", false,
		ln{"1-1111", rp(2_000_000_000), zero}, ln{"3-1001", zero, rp(2_000_000_000)}); err != nil {
		return err
	}
	if err := post(ledger.General, month.AddDays(2), "Pembelian peralatan kantor", false,
		ln{"1-2104", rp(120_000_000), zero}, ln{"1-1111", zero, rp(120_000_000)}); err != nil {
		return err
	}
	if err := post(ledger.General, month.AddDays(4), "Sewa kantor dibayar di muka 12 bulan", false,
		ln{"1-1501", rp(240_000_000), zero}, ln{"1-1111", zero, rp(240_000_000)}); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		m := month.AddMonths(i)
		last := m.MonthEnd()
		day := func(d int) civil.Date {
			if t := m.AddDays(d - 1); !t.After(today) {
				return t
			}
			return today
		}
		ppn := revenue[i] * 11 / 100
		steps := []func() error{
			func() error {
				return post(ledger.General, day(10), words[0]+" "+civil.MonthName(m.Month()), false,
					ln{"1-1201", rp(revenue[i] + ppn), zero}, ln{"4-1001", zero, rp(revenue[i])}, ln{"2-1201", zero, rp(ppn)})
			},
			func() error {
				vat := cost[i] * 11 / 100
				return post(ledger.General, day(12), words[1], false,
					ln{"5-1001", rp(cost[i]), zero}, ln{"1-1401", rp(vat), zero}, ln{"2-1101", zero, rp(cost[i] + vat)})
			},
			func() error {
				return post(ledger.General, day(11), "Gaji karyawan "+civil.MonthName(m.Month()), false,
					ln{"6-1001", rp(85_000_000), zero}, ln{"1-1111", zero, rp(85_000_000)})
			},
			func() error {
				return post(ledger.General, day(11), "Biaya administrasi bank", false,
					ln{"8-1001", rp(150_000), zero}, ln{"1-1111", zero, rp(150_000)})
			},
		}
		if i < 2 {
			steps = append(steps,
				func() error {
					return post(ledger.General, day(25), words[2], false,
						ln{"1-1111", rp(revenue[i] + ppn), zero}, ln{"1-1201", zero, rp(revenue[i] + ppn)})
				},
				func() error {
					return post(ledger.Adjustment, last, "Amortisasi sewa dibayar di muka", false,
						ln{"6-2001", rp(20_000_000), zero}, ln{"1-1501", zero, rp(20_000_000)})
				},
				func() error {
					return post(ledger.Adjustment, last, "Penyusutan peralatan kantor", false,
						ln{"6-3004", rp(2_500_000), zero}, ln{"1-2204", zero, rp(2_500_000)})
				},
				func() error {
					return post(ledger.Adjustment, last, "Akrual listrik, air, dan telepon", true,
						ln{"6-2002", rp(4_250_000), zero}, ln{"2-1302", zero, rp(4_250_000)})
				},
			)
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
	}

	mgr := ledger.Actor{UserID: manager}
	if err := l.SetPeriodStatus(ctx, company, month, "locked", "", mgr); err != nil {
		return err
	}
	if err := l.SetPeriodStatus(ctx, company, month.AddMonths(1), "adjusting", "", mgr); err != nil {
		return err
	}
	fmt.Printf("  buku %-10s bagan akun dan jurnal %s – %s\n", industry, month.MonthLabel(), today.MonthLabel())
	return nil
}

// seedTrade mengisi kontak, rekening kas/bank, faktur, pelunasan, tagihan, pembayaran, dan
// transaksi kas bulan berjalan. PT yang sudah punya kontak dilewati.
func seedTrade(ctx context.Context, d *server.Deps, company uuid.UUID, industry string, staf audit.Actor) error {
	existing, err := d.Core.Contacts(ctx, company, core.ContactFilter{IncludeInactive: true})
	if err != nil || len(existing) > 0 {
		return err
	}
	accounts, err := d.Ledger.Accounts(ctx, company)
	if err != nil {
		return err
	}
	code := map[string]uuid.UUID{}
	for _, a := range accounts {
		code[a.Code] = a.ID
	}
	// PT dari seed lama belum punya akun beban PPh final untuk semua industri.
	if _, ok := code["9-1003"]; !ok {
		parent := code["9-1000"]
		a, err := d.Ledger.CreateAccount(ctx, company, ledger.NewAccount{Code: "9-1003", Name: "Beban PPh final", ParentID: &parent, Postable: true}, staf)
		if err != nil {
			return err
		}
		code[a.Code] = a.ID
	}

	for _, ca := range []struct{ code, kind, bank, number string }{
		{"1-1101", "kas", "", ""}, {"1-1102", "kas", "", ""},
		{"1-1111", "bank", "BCA", "0123456789"}, {"1-1112", "bank", "Mandiri", "1370012345678"},
	} {
		if _, err := d.Cash.RegisterAccount(ctx, company, cash.AccountInput{
			AccountID: code[ca.code], Kind: ca.kind, BankName: ca.bank, AccountNumber: ca.number, Active: true,
		}, staf); err != nil {
			return err
		}
	}

	type contact struct {
		name     string
		customer bool
		npwp     string
		terms    int
	}
	names := map[string][4]string{
		"jasa":       {"PT Contoh Energi Mandiri", "PT Contoh Data Teknologi", "CV Contoh Tenaga Ahli", "PT Contoh Graha Perkantoran"},
		"konstruksi": {"PT Contoh Logistik Gudang", "PT Contoh Properti Hunian", "PT Contoh Baja Ringan", "CV Contoh Mandor Bangunan"},
		"retail":     {"Koperasi Contoh Sejahtera", "PT Contoh Kantin Sekolah", "PT Contoh Distribusi Pangan", "CV Contoh Kemasan"},
	}[industry]
	ids := make([]uuid.UUID, 4)
	for i, c := range []contact{
		{names[0], true, fmt.Sprintf("01100000000000%d1", i0(industry)), 30},
		{names[1], true, fmt.Sprintf("01200000000000%d2", i0(industry)), 14},
		{names[2], false, fmt.Sprintf("02100000000000%d3", i0(industry)), 14},
		{names[3], false, fmt.Sprintf("02200000000000%d4", i0(industry)), 30},
	} {
		created, err := d.Core.CreateContact(ctx, company, core.ContactInput{
			Name: c.name, IsCustomer: c.customer, IsSupplier: !c.customer, NPWP: c.npwp, PKP: true,
			PaymentTermsDays: c.terms, Address: "Jl. Contoh No. " + fmt.Sprint(i+1) + ", Jakarta",
		}, staf)
		if err != nil {
			return err
		}
		ids[i] = created.ID
	}
	custA, custB, supA, supB := ids[0], ids[1], ids[2], ids[3]

	scale := map[string]int64{"jasa": 1, "konstruksi": 3, "retail": 2}[industry]
	rp := func(n int64) money.Amount { return money.FromInt(n * scale) }
	today := civil.Today(civil.Jakarta)
	month := today.MonthStart()
	day := func(n int) civil.Date {
		if t := month.AddDays(n - 1); !t.After(today) {
			return t
		}
		return today
	}
	ln := func(acc string, desc string, price money.Amount, taxCode string) trade.LineInput {
		return trade.LineInput{Description: desc, AccountID: code[acc], Qty: money.FromInt(1), UnitPrice: price, TaxCode: taxCode}
	}
	revenue := map[string]string{"jasa": "Jasa konsultansi teknik", "konstruksi": "Termin pekerjaan struktur", "retail": "Penjualan barang dagang"}[industry]
	whCode := map[string]string{"jasa": "PPH23", "konstruksi": "PPH42-K265", "retail": ""}[industry]

	postInvoice := func(in sales.InvoiceInput) (sales.InvoiceDetail, error) {
		inv, err := d.Sales.CreateInvoice(ctx, company, in, staf)
		if err != nil {
			return inv, err
		}
		return d.Sales.PostInvoice(ctx, company, inv.ID, staf)
	}
	// 1. Lewat jatuh tempo, belum dibayar.
	if _, err := postInvoice(sales.InvoiceInput{CustomerID: custA, Date: day(1), DueDate: day(5), Memo: "Tagihan awal bulan",
		Lines: []trade.LineInput{ln("4-1001", revenue, rp(25_000_000), "PPN")}}); err != nil {
		return err
	}
	// 2. Dibayar sebagian.
	inv2, err := postInvoice(sales.InvoiceInput{CustomerID: custB, Date: day(3), Memo: "Kontrak bulanan",
		Lines: []trade.LineInput{ln("4-1001", revenue, rp(12_000_000), "PPN"), ln("4-1002", "Layanan tambahan", rp(3_000_000), "PPN")}})
	if err != nil {
		return err
	}
	if _, err := d.Sales.CreateReceipt(ctx, company, sales.ReceiptInput{CustomerID: custB, Date: day(10), CashAccountID: code["1-1111"],
		Allocations: []sales.AllocationInput{{InvoiceID: inv2.ID, Amount: rp(10_000_000)}}}, staf); err != nil {
		return err
	}
	// 3. Lunas, dengan PPh dipotong pelanggan (jasa: PPh 23, konstruksi: PPh final 4(2)).
	inv3, err := postInvoice(sales.InvoiceInput{CustomerID: custA, Date: day(6), Memo: "Pekerjaan selesai",
		Lines: []trade.LineInput{ln("4-1001", revenue, rp(10_000_000), "PPN")}})
	if err != nil {
		return err
	}
	alloc := sales.AllocationInput{InvoiceID: inv3.ID, Amount: inv3.Total}
	if whCode != "" {
		table, err := d.Tax.Table(ctx)
		if err != nil {
			return err
		}
		c, err := table.Find(whCode, day(9))
		if err != nil {
			return err
		}
		alloc.WithholdingCode, alloc.Withholding = whCode, c.Withholding(inv3.Totals.DPP)
	}
	if _, err := d.Sales.CreateReceipt(ctx, company, sales.ReceiptInput{CustomerID: custA, Date: day(9), CashAccountID: code["1-1111"],
		Allocations: []sales.AllocationInput{alloc}}, staf); err != nil {
		return err
	}
	// 4. Draf yang belum diposting.
	if _, err := d.Sales.CreateInvoice(ctx, company, sales.InvoiceInput{CustomerID: custB, Date: today, Memo: "Draf untuk dicek Manager",
		Lines: []trade.LineInput{ln("4-1001", revenue, rp(7_500_000), "PPN")}}, staf); err != nil {
		return err
	}

	postBill := func(in purchase.BillInput) (purchase.BillDetail, error) {
		b, err := d.Purchase.CreateBill(ctx, company, in, staf)
		if err != nil {
			return b, err
		}
		return d.Purchase.PostBill(ctx, company, b.ID, staf)
	}
	cost := map[string][2]string{"jasa": {"5-1001", "Honor tenaga ahli"}, "konstruksi": {"5-1001", "Material baja ringan"}, "retail": {"5-1001", "Pembelian barang dagang"}}[industry]
	bill1, err := postBill(purchase.BillInput{SupplierID: supA, SupplierInvoiceNo: "INV/" + fmt.Sprint(month.Year()) + "/0091", Date: day(2),
		WithholdingCode: map[string]string{"jasa": "PPH23", "konstruksi": "", "retail": ""}[industry],
		Lines:           []trade.LineInput{ln(cost[0], cost[1], rp(8_000_000), "PPN")}})
	if err != nil {
		return err
	}
	if _, err := d.Purchase.CreatePayment(ctx, company, purchase.PaymentInput{SupplierID: supA, Date: day(8), CashAccountID: code["1-1111"],
		Allocations: []purchase.AllocationInput{{BillID: bill1.ID, Amount: bill1.Payable}}}, staf); err != nil {
		return err
	}
	if _, err := postBill(purchase.BillInput{SupplierID: supB, SupplierInvoiceNo: "SW-10/" + fmt.Sprint(month.Year()), Date: day(4),
		Memo: "Sewa dan layanan kantor", Lines: []trade.LineInput{ln("6-2001", "Sewa ruang rapat", rp(4_500_000), "PPN")}}); err != nil {
		return err
	}
	if _, err := d.Purchase.CreateBill(ctx, company, purchase.BillInput{SupplierID: supB, Date: today, Memo: "Draf tagihan listrik gedung",
		Lines: []trade.LineInput{ln("6-2002", "Listrik gedung", rp(1_250_000), "NON-PPN")}}, staf); err != nil {
		return err
	}

	if _, err := d.Cash.Record(ctx, company, cash.TransactionInput{Kind: "out", Date: day(7), CashAccountID: code["1-1111"], Memo: "Mutasi bank",
		Lines: []cash.LineInput{{AccountID: code["8-1001"], Amount: money.FromInt(25_000), Memo: "Biaya administrasi bulanan"}}}, staf); err != nil {
		return err
	}
	petty := code["1-1101"]
	if _, err := d.Cash.Record(ctx, company, cash.TransactionInput{Kind: "transfer", Date: day(7), CashAccountID: code["1-1111"],
		ToAccountID: &petty, Amount: money.FromInt(1_500_000), Memo: "Pengisian kas kecil"}, staf); err != nil {
		return err
	}
	fmt.Printf("  dagang %-9s kontak, rekening, faktur, pelunasan, tagihan, dan kas %s\n", industry, today.MonthLabel())
	return nil
}

// i0 membedakan NPWP contoh antar-PT.
func i0(industry string) int {
	return map[string]int{"jasa": 1, "konstruksi": 2, "retail": 3}[industry]
}

// seedTax melengkapi profil pajak PT (NPWP, NITKU, alamat) dan mencatat satu faktur pajak yang
// sudah terbit, supaya halaman Pajak menunjukkan berbagai status. Hanya untuk PT tanpa NPWP.
func seedTax(ctx context.Context, d *server.Deps, company uuid.UUID, industry string, staf audit.Actor) error {
	c, err := d.Core.Company(ctx, company)
	if err != nil || c.NPWP != "" {
		return err
	}
	npwp := fmt.Sprintf("091234567890%04d", i0(industry))
	if _, err := d.Core.UpdateTaxProfile(ctx, company, core.TaxProfile{
		NPWP: npwp, NITKU: npwp + "000000", Address: "Jl. Contoh Raya No. " + fmt.Sprint(i0(industry)*10) + ", Jakarta Selatan",
		PKP: true, Version: c.Version,
	}, staf); err != nil {
		return err
	}
	month := civil.Today(civil.Jakarta).MonthStart()
	list, err := d.Coretax.Outputs(ctx, company, month)
	if err != nil {
		return err
	}
	for _, it := range list.Items {
		if it.Status != "ready" {
			continue
		}
		// Nomor contoh: 04 (kode transaksi) + 0 + tahun 2 digit + nomor urut.
		nsfp := fmt.Sprintf("040%02d%011d", month.Year()%100, i0(industry))
		if err := d.Coretax.Approve(ctx, company, it.InvoiceID, nsfp, it.Date.AddDays(1), staf); err != nil {
			return err
		}
		break
	}
	fmt.Printf("  pajak %-10s profil PT dan satu faktur pajak terbit\n", industry)
	return nil
}

// seedProjectsInventory mengisi produk, stok awal, mutasi persediaan, dan proyek dengan RAB,
// uang muka, serta termin bulan berjalan. PT yang sudah punya produk dilewati.
func seedProjectsInventory(ctx context.Context, d *server.Deps, company uuid.UUID, industry string, staf audit.Actor) error {
	existing, err := d.Inventory.Products(ctx, company, inventory.ProductFilter{IncludeInactive: true})
	if err != nil || len(existing) > 0 {
		return err
	}
	accounts, err := d.Ledger.Accounts(ctx, company)
	if err != nil {
		return err
	}
	code := map[string]uuid.UUID{}
	for _, a := range accounts {
		code[a.Code] = a.ID
	}
	// PT dari seed lama belum punya akun piutang retensi di semua industri.
	if _, ok := code["1-1202"]; !ok {
		parent := code["1-1200"]
		a, err := d.Ledger.CreateAccount(ctx, company, ledger.NewAccount{Code: "1-1202", Name: "Piutang retensi", ParentID: &parent, Postable: true}, staf)
		if err != nil {
			return err
		}
		code[a.Code] = a.ID
	}
	contacts, err := d.Core.Contacts(ctx, company, core.ContactFilter{})
	if err != nil {
		return err
	}
	var customer, supplier uuid.UUID
	for _, c := range contacts {
		if c.IsCustomer && customer == uuid.Nil {
			customer = c.ID
		}
		if c.IsSupplier && supplier == uuid.Nil {
			supplier = c.ID
		}
	}
	today := civil.Today(civil.Jakarta)
	month := today.MonthStart()
	day := func(n int) civil.Date {
		if t := month.AddDays(n - 1); !t.After(today) {
			return t
		}
		return today
	}
	id := func(c string) *uuid.UUID { v := code[c]; return &v }
	qty := func(n int64) money.Amount { return money.FromInt(n) }

	type product struct {
		code, name, kind, unit string
		price, cost            int64
		stock                  int64
	}
	var list []product
	invAcc, cogsAcc := "", "5-1001"
	switch industry {
	case "retail":
		invAcc = "1-1302"
		list = []product{
			{"BRG-001", "Beras premium 5 kg", inventory.KindGoods, "karung", 78_000, 66_000, 120},
			{"BRG-002", "Minyak goreng 2 L", inventory.KindGoods, "botol", 39_500, 33_000, 200},
			{"BRG-003", "Gula pasir 1 kg", inventory.KindGoods, "pak", 18_500, 15_200, 300},
			{"JS-001", "Ongkos kirim", inventory.KindService, "kali", 25_000, 0, 0},
		}
	case "konstruksi":
		invAcc = "1-1301"
		list = []product{
			{"MTR-001", "Semen Portland 50 kg", inventory.KindGoods, "sak", 0, 62_000, 500},
			{"MTR-002", "Besi beton 10 mm", inventory.KindGoods, "batang", 0, 86_500, 300},
			{"JS-001", "Sewa ekskavator per hari", inventory.KindService, "hari", 4_500_000, 0, 0},
		}
	default:
		list = []product{
			{"JS-KONS", "Konsultansi teknik", inventory.KindService, "hari", 6_500_000, 0, 0},
			{"JS-AUDIT", "Audit energi gedung", inventory.KindService, "paket", 45_000_000, 0, 0},
		}
	}
	made := map[string]inventory.Product{}
	var opening []inventory.AdjustmentLineInput
	for _, p := range list {
		in := inventory.ProductInput{
			Code: p.code, Name: p.name, Kind: p.kind, Unit: p.unit, SalePrice: money.FromInt(p.price),
			PurchasePrice: money.FromInt(p.cost), RevenueAccountID: id("4-1001"),
		}
		if p.kind == inventory.KindGoods {
			in.InventoryAccountID, in.ExpenseAccountID = id(invAcc), id(cogsAcc)
		} else if industry == "konstruksi" {
			in.RevenueAccountID, in.ExpenseAccountID = id("4-1002"), id("5-1004")
		}
		created, err := d.Inventory.CreateProduct(ctx, company, in, staf)
		if err != nil {
			return err
		}
		made[p.code] = created
		if p.stock > 0 {
			c := money.FromInt(p.cost)
			opening = append(opening, inventory.AdjustmentLineInput{ProductID: created.ID, Qty: qty(p.stock), UnitCost: &c})
		}
	}
	if len(opening) > 0 {
		if _, err := d.Inventory.CreateAdjustment(ctx, company, inventory.AdjustmentInput{
			Kind: inventory.AdjManual, Date: day(1), AccountID: code["3-3001"], Memo: "Saldo awal persediaan", Lines: opening,
		}, staf); err != nil {
			return err
		}
	}

	postInvoice := func(in sales.InvoiceInput) (sales.InvoiceDetail, error) {
		inv, err := d.Sales.CreateInvoice(ctx, company, in, staf)
		if err != nil {
			return inv, err
		}
		return d.Sales.PostInvoice(ctx, company, inv.ID, staf)
	}
	postBill := func(in purchase.BillInput) error {
		b, err := d.Purchase.CreateBill(ctx, company, in, staf)
		if err != nil {
			return err
		}
		_, err = d.Purchase.PostBill(ctx, company, b.ID, staf)
		return err
	}
	pid := func(c string) *uuid.UUID { v := made[c].ID; return &v }

	switch industry {
	case "retail":
		if err := postBill(purchase.BillInput{SupplierID: supplier, SupplierInvoiceNo: "DO-" + fmt.Sprint(month.Year()) + "/118", Date: day(4),
			Memo: "Restok sembako", Lines: []trade.LineInput{
				{Description: "Beras premium 5 kg", ProductID: pid("BRG-001"), Qty: qty(80), UnitPrice: money.FromInt(67_500), TaxCode: "NON-PPN"},
				{Description: "Minyak goreng 2 L", ProductID: pid("BRG-002"), Qty: qty(100), UnitPrice: money.FromInt(33_600), TaxCode: "PPN"},
			}}); err != nil {
			return err
		}
		if _, err := postInvoice(sales.InvoiceInput{CustomerID: customer, Date: day(6), Memo: "Pesanan koperasi", Lines: []trade.LineInput{
			{Description: "Beras premium 5 kg", ProductID: pid("BRG-001"), Qty: qty(60), UnitPrice: money.FromInt(78_000), TaxCode: "NON-PPN"},
			{Description: "Minyak goreng 2 L", ProductID: pid("BRG-002"), Qty: qty(90), UnitPrice: money.FromInt(39_500), TaxCode: "PPN"},
			{Description: "Gula pasir 1 kg", ProductID: pid("BRG-003"), Qty: qty(150), UnitPrice: money.FromInt(18_500), TaxCode: "PPN"},
			{Description: "Ongkos kirim", ProductID: pid("JS-001"), Qty: qty(2), UnitPrice: money.FromInt(25_000), TaxCode: "PPN"},
		}}); err != nil {
			return err
		}
		counted := qty(147)
		if _, err := d.Inventory.CreateAdjustment(ctx, company, inventory.AdjustmentInput{
			Kind: inventory.AdjCount, Date: day(12), AccountID: code["5-1003"], Memo: "Stock opname gudang tengah bulan",
			Lines: []inventory.AdjustmentLineInput{{ProductID: made["BRG-003"].ID, CountedQty: &counted, Memo: "3 pak sobek"}},
		}, staf); err != nil {
			return err
		}

	case "konstruksi":
		p, err := d.Project.CreateProject(ctx, company, project.ProjectInput{
			Code: "PRJ-2026-01", Name: "Gudang logistik Cikarang", CustomerID: &customer, ContractValue: money.FromInt(2_400_000_000),
			DPPct: money.FromInt(20), RetentionPct: money.FromInt(5), PPhCode: ptr("PPH42-K265"), RevenueAccountID: id("4-1001"),
			StartDate: &month, Notes: "Kontrak lump sum, termin per progres bulanan.",
		}, staf)
		if err != nil {
			return err
		}
		if _, err := d.Project.SetBudget(ctx, company, p.ID, []project.BudgetLineInput{
			{AccountID: code["5-1001"], Description: "Material (semen, besi, baja ringan)", Amount: money.FromInt(900_000_000)},
			{AccountID: code["5-1002"], Description: "Upah tukang dan mandor", Amount: money.FromInt(450_000_000)},
			{AccountID: code["5-1003"], Description: "Subkontraktor MEP", Amount: money.FromInt(600_000_000)},
			{AccountID: code["5-1004"], Description: "Sewa alat berat", Amount: money.FromInt(150_000_000)},
			{AccountID: code["5-1005"], Description: "Overhead lapangan", Amount: money.FromInt(100_000_000)},
		}, staf); err != nil {
			return err
		}
		billing := func(kind string, date civil.Date, progress int64) error {
			inv, err := d.Project.CreateBilling(ctx, company, p.ID, project.BillingInput{Kind: kind, Date: date, ProgressPct: money.FromInt(progress)}, staf)
			if err != nil {
				return err
			}
			_, err = d.Sales.PostInvoice(ctx, company, inv.ID, staf)
			return err
		}
		if err := billing(sales.KindAdvance, day(2), 0); err != nil {
			return err
		}
		pr := p.ID
		if _, err := d.Inventory.CreateAdjustment(ctx, company, inventory.AdjustmentInput{
			Kind: inventory.AdjIssue, Date: day(5), AccountID: code["5-1001"], ProjectID: &pr, Memo: "Pengecoran pondasi",
			Lines: []inventory.AdjustmentLineInput{
				{ProductID: made["MTR-001"].ID, Qty: qty(180)}, {ProductID: made["MTR-002"].ID, Qty: qty(120)},
			},
		}, staf); err != nil {
			return err
		}
		if err := postBill(purchase.BillInput{SupplierID: supplier, SupplierInvoiceNo: "SPK-07/" + fmt.Sprint(month.Year()), Date: day(9),
			WithholdingCode: "PPH42-K265", Memo: "Subkontraktor MEP tahap 1", Lines: []trade.LineInput{
				{Description: "Instalasi listrik dan plumbing tahap 1", AccountID: code["5-1003"], Qty: qty(1), UnitPrice: money.FromInt(180_000_000), TaxCode: "PPN", ProjectID: &pr},
			}}); err != nil {
			return err
		}
		if _, err := d.Cash.Record(ctx, company, cash.TransactionInput{Kind: "out", Date: day(10), CashAccountID: code["1-1102"], Memo: "Upah mingguan proyek",
			Lines: []cash.LineInput{
				{AccountID: code["5-1002"], Amount: money.FromInt(42_500_000), Memo: "Upah tukang minggu 1–2", ProjectID: &pr},
				{AccountID: code["5-1005"], Amount: money.FromInt(3_750_000), Memo: "Konsumsi dan air minum lapangan", ProjectID: &pr},
			}}, staf); err != nil {
			return err
		}
		if err := billing(sales.KindProgress, day(12), 25); err != nil {
			return err
		}
		if _, err := d.Project.CreateProject(ctx, company, project.ProjectInput{
			Code: "PRJ-2026-02", Name: "Renovasi kantor cabang Bekasi", CustomerID: &customer, ContractValue: money.FromInt(350_000_000),
			RetentionPct: money.FromInt(5), PPhCode: ptr("PPH42-K265"), RevenueAccountID: id("4-1001"), StartDate: &today,
		}, staf); err != nil {
			return err
		}

	default:
		p, err := d.Project.CreateProject(ctx, company, project.ProjectInput{
			Code: "JT-2026-07", Name: "Audit energi gedung kantor", CustomerID: &customer, ContractValue: money.FromInt(180_000_000),
			PPhCode: ptr("PPH23"), RevenueAccountID: id("4-1001"), StartDate: &month,
		}, staf)
		if err != nil {
			return err
		}
		if _, err := d.Project.SetBudget(ctx, company, p.ID, []project.BudgetLineInput{
			{AccountID: code["5-1001"], Description: "Tenaga ahli", Amount: money.FromInt(70_000_000)},
			{AccountID: code["5-1003"], Description: "Perjalanan dan akomodasi", Amount: money.FromInt(15_000_000)},
		}, staf); err != nil {
			return err
		}
		pr := p.ID
		if _, err := postInvoice(sales.InvoiceInput{CustomerID: customer, Date: day(8), Memo: "Tahap 1 audit energi", ProjectID: &pr, Lines: []trade.LineInput{
			{Description: "Audit energi gedung, tahap 1", ProductID: pid("JS-AUDIT"), Qty: qty(1), UnitPrice: money.FromInt(45_000_000), TaxCode: "PPN"},
			{Description: "Konsultansi teknik", ProductID: pid("JS-KONS"), Qty: qty(3), UnitPrice: money.FromInt(6_500_000), TaxCode: "PPN"},
		}}); err != nil {
			return err
		}
		if err := postBill(purchase.BillInput{SupplierID: supplier, SupplierInvoiceNo: "TA-" + fmt.Sprint(month.Year()) + "/31", Date: day(9),
			WithholdingCode: "PPH23", Memo: "Tenaga ahli audit energi", Lines: []trade.LineInput{
				{Description: "Tenaga ahli energi 6 hari", AccountID: code["5-1001"], Qty: qty(6), UnitPrice: money.FromInt(3_200_000), TaxCode: "PPN", ProjectID: &pr},
			}}); err != nil {
			return err
		}
		if _, err := d.Cash.Record(ctx, company, cash.TransactionInput{Kind: "out", Date: day(9), CashAccountID: code["1-1101"], Memo: "Perjalanan survei lapangan",
			Lines: []cash.LineInput{{AccountID: code["5-1003"], Amount: money.FromInt(1_850_000), Memo: "Tiket dan hotel tim survei", ProjectID: &pr}}}, staf); err != nil {
			return err
		}
	}
	fmt.Printf("  proyek %-9s produk, stok, dan proyek %s\n", industry, today.MonthLabel())
	return nil
}

// seedGroup menautkan PT Grup Arta satu sama lain lewat kontak (ME-03) dan mencatat transaksi
// antar-PT bulan ini: jasa desain dari PT Jasa dan material dari PT Retail ke PT Konstruksi.
// Dilewati bila tautannya sudah ada.
func seedGroup(ctx context.Context, d *server.Deps, byIndustry map[string]uuid.UUID, staf audit.Actor) error {
	jasa, konstruksi, retail := byIndustry["jasa"], byIndustry["konstruksi"], byIndustry["retail"]
	if _, err := d.Core.LinkedContact(ctx, konstruksi, jasa); err == nil {
		return nil
	}
	code := func(company uuid.UUID, c string) (*uuid.UUID, error) {
		a, err := d.Ledger.AccountByCode(ctx, company, c)
		if err != nil {
			return nil, err
		}
		return &a.ID, nil
	}
	link := func(in, other uuid.UUID, revenue, expense string) (core.Contact, error) {
		o, err := d.Core.Company(ctx, other)
		if err != nil {
			return core.Contact{}, err
		}
		rev, err := code(in, revenue)
		if err != nil {
			return core.Contact{}, err
		}
		exp, err := code(in, expense)
		if err != nil {
			return core.Contact{}, err
		}
		bank, err := code(in, "1-1111")
		if err != nil {
			return core.Contact{}, err
		}
		npwp := o.NPWP
		return d.Core.CreateContact(ctx, in, core.ContactInput{
			Name: o.Name, IsCustomer: true, IsSupplier: true, NPWP: npwp, PKP: o.PKP, PaymentTermsDays: 30,
			Address: "Grup Arta", LinkedCompanyID: &other, ICRevenueAccountID: rev, ICExpenseAccountID: exp, ICCashAccountID: bank,
		}, staf)
	}
	jasaToKons, err := link(jasa, konstruksi, "4-1001", "6-2008")
	if err != nil {
		return err
	}
	konsToJasa, err := link(konstruksi, jasa, "4-1002", "5-1003")
	if err != nil {
		return err
	}
	retailToKons, err := link(retail, konstruksi, "4-1001", "6-2008")
	if err != nil {
		return err
	}
	if _, err := link(konstruksi, retail, "4-1002", "5-1001"); err != nil {
		return err
	}
	if _, err := link(jasa, retail, "4-1001", "6-2008"); err != nil {
		return err
	}
	if _, err := link(retail, jasa, "4-1002", "6-2008"); err != nil {
		return err
	}

	today := civil.Today(civil.Jakarta)
	month := today.MonthStart()
	day := func(n int) civil.Date {
		if t := month.AddDays(n - 1); !t.After(today) {
			return t
		}
		return today
	}
	post := func(company, customer uuid.UUID, date civil.Date, desc, account string, amount int64) (sales.InvoiceDetail, error) {
		acc, err := code(company, account)
		if err != nil {
			return sales.InvoiceDetail{}, err
		}
		inv, err := d.Sales.CreateInvoice(ctx, company, sales.InvoiceInput{CustomerID: customer, Date: date, Memo: "Transaksi antar-PT Grup Arta",
			Lines: []trade.LineInput{{Description: desc, AccountID: *acc, Qty: money.FromInt(1), UnitPrice: money.FromInt(amount), TaxCode: "PPN"}}}, staf)
		if err != nil {
			return inv, err
		}
		return d.Sales.PostInvoice(ctx, company, inv.ID, staf)
	}
	inv, err := post(jasa, jasaToKons.ID, day(8), "Jasa desain MEP gudang Cikarang", "4-1001", 35_000_000)
	if err != nil {
		return err
	}
	if _, err := post(retail, retailToKons.ID, day(9), "Semen dan besi untuk proyek", "4-1001", 12_000_000); err != nil {
		return err
	}
	// PT Konstruksi membayar sebagian tagihan cerminannya, PT Jasa mencatat penerimaannya.
	l, err := d.Consol.Link(ctx, jasa, sales.SourceInvoice, inv.ID)
	if err != nil || l == nil {
		return err
	}
	if _, err := d.Purchase.CreatePayment(ctx, konstruksi, purchase.PaymentInput{SupplierID: konsToJasa.ID, Date: day(11), CashAccountID: *mustCode(code(konstruksi, "1-1111")),
		Allocations: []purchase.AllocationInput{{BillID: l.DocID, Amount: money.FromInt(15_000_000)}}}, staf); err != nil {
		return err
	}
	if _, err := d.Sales.CreateReceipt(ctx, jasa, sales.ReceiptInput{CustomerID: jasaToKons.ID, Date: day(11), CashAccountID: *mustCode(code(jasa, "1-1111")),
		Allocations: []sales.AllocationInput{{InvoiceID: inv.ID, Amount: money.FromInt(15_000_000)}}}, staf); err != nil {
		return err
	}
	fmt.Println("  grup     kontak antar-PT dan transaksi antar-PT bulan ini")
	return nil
}

func mustCode(id *uuid.UUID, err error) *uuid.UUID {
	if err != nil {
		panic(err)
	}
	return id
}
