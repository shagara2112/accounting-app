//go:build !release

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/config"
	"lajur/internal/platform/db"
	"lajur/internal/platform/money"
	"lajur/internal/platform/store"
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
