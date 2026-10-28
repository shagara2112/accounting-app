package panjar_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/budget"
	"lajur/internal/modules/core"
	"lajur/internal/modules/fixture"
	"lajur/internal/modules/panjar"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

var day, rp = fixture.Day, fixture.Rp

func code(err error) string {
	var p *httpx.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func spread(n int64) []money.Amount {
	out := make([]money.Amount, 12)
	for i := range out {
		out[i] = rp(n)
	}
	return out
}

// Panjar unit: kepala unit (Other) mengajukan, finance (Actor) memutuskan dan menandai dibayar.
// Pengaju tidak melihat pengajuan unit lain, tidak bisa memutuskan pengajuannya sendiri, dan
// sisa anggaran unit memperhitungkan panjar yang sudah disetujui tetapi belum dibayar.
func TestPanjarApprovalFlow(t *testing.T) {
	e := fixture.New(t)
	today := day("2026-10-15")
	svc := panjar.New(panjar.Deps{Pool: e.Pool, Ledger: e.Ledger, Core: e.Core, Budget: e.Budget, Today: func() civil.Date { return today }})
	dept, err := e.Core.SaveDimension(e.Ctx, e.Company, "departemen", nil, core.DimensionInput{Code: "TKN", Name: "Unit Teknik", Active: true}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	head, finance := e.Other, e.Actor
	asHead, asFinance := panjar.Viewer{UserID: head.UserID}, panjar.Viewer{UserID: finance.UserID, All: true}
	if err := e.Core.Assign(e.Ctx, head.UserID, e.Company, "kepala_unit"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetUnits(e.Ctx, e.Company, head.UserID, []uuid.UUID{dept.ID}, finance); err != nil {
		t.Fatal(err)
	}

	// Anggaran beban unit 2,4 jt setahun, realisasi Oktober 500 rb.
	b, err := e.Budget.CreateBudget(e.Ctx, e.Company, budget.NewBudget{FiscalYear: 2026, Name: "Awal"}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Budget.SaveSlice(e.Ctx, e.Company, b.ID, budget.SliceInput{DepartmentID: &dept.ID, Version: b.Version, Lines: []budget.LineInput{
		{AccountID: e.Acc["6-2001"], Months: spread(200_000)},
	}}, e.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Ledger.Post(e.Ctx, ledger.EntryDraft{
		CompanyID: e.Company, Kind: ledger.General, Date: day("2026-10-05"), Memo: "uji", Actor: e.Actor,
		Lines: []ledger.LineDraft{
			{AccountID: e.Acc["6-2001"], Debit: rp(500_000), DepartmentID: &dept.ID},
			{AccountID: e.Acc["1-1111"], Credit: rp(500_000)},
		},
	}); err != nil {
		t.Fatal(err)
	}

	acc := e.Acc["6-2001"]
	in := panjar.Input{DepartmentID: dept.ID, Purpose: "Perbaikan genset kantor", NeededDate: ptr(day("2026-10-20")), Lines: []panjar.LineInput{
		{Description: "Suku cadang", AccountID: &acc, Amount: rp(300_000)},
		{Description: "Jasa teknisi", Amount: rp(200_000)},
	}}

	// Isian salah ditolak.
	header := e.Acc["6"]
	for name, tc := range map[string]struct {
		mut  func(*panjar.Input)
		want string
	}{
		"keperluan kosong": {func(i *panjar.Input) { i.Purpose = "  " }, "panjar.invalid"},
		"tanpa rincian":    {func(i *panjar.Input) { i.Lines = nil }, "panjar.invalid"},
		"nominal nol":      {func(i *panjar.Input) { i.Lines = []panjar.LineInput{{Description: "x"}} }, "panjar.invalid"},
		"tanggal lampau":   {func(i *panjar.Input) { i.NeededDate = ptr(day("2026-10-01")) }, "panjar.invalid"},
		"akun header": {func(i *panjar.Input) {
			i.Lines = []panjar.LineInput{{Description: "x", AccountID: &header, Amount: rp(1)}}
		}, "panjar.invalid"},
		"unit tidak dikenal": {func(i *panjar.Input) { i.DepartmentID = uuid.Must(uuid.NewV7()) }, "dimension.not_found"},
	} {
		bad := in
		bad.Lines = append([]panjar.LineInput(nil), in.Lines...)
		tc.mut(&bad)
		if _, err := svc.Create(e.Ctx, e.Company, bad, asHead, head); code(err) != tc.want {
			t.Fatalf("%s: err = %v, mau %s", name, err, tc.want)
		}
	}

	p, err := svc.Create(e.Ctx, e.Company, in, asHead, head)
	if err != nil || !strings.HasPrefix(p.Number, "PNJ") || p.Status != panjar.Submitted || !p.Total.Equal(rp(500_000)) ||
		p.RequestDate != today || len(p.Lines) != 2 || p.Lines[0].AccountCode != "6-2001" || p.DepartmentCode != "TKN" {
		t.Fatalf("pengajuan = %+v, %v", p, err)
	}

	// Pengaju melihat pengajuan unitnya; pengguna terbatas tanpa unit tidak melihat apa pun; finance
	// melihat semua dan jumlah yang menunggu.
	if l, _ := svc.Requests(e.Ctx, e.Company, asHead, panjar.Filter{From: day("2026-01-01"), To: day("2026-12-31")}); len(l.Items) != 1 || !l.Items[0].Mine || l.Pending != 0 {
		t.Fatalf("daftar pengaju = %+v", l)
	}
	if l, _ := svc.Requests(e.Ctx, e.Company, panjar.Viewer{UserID: finance.UserID}, panjar.Filter{From: day("2026-01-01"), To: day("2026-12-31")}); len(l.Items) != 0 {
		t.Fatalf("pengguna lain tanpa izin lihat seharusnya tidak melihat pengajuan: %+v", l)
	}
	if l, _ := svc.Requests(e.Ctx, e.Company, asFinance, panjar.Filter{From: day("2026-01-01"), To: day("2026-12-31")}); len(l.Items) != 1 || l.Items[0].Mine || l.Pending != 1 {
		t.Fatalf("daftar finance = %+v", l)
	}
	if _, err := svc.Request(e.Ctx, e.Company, p.ID, panjar.Viewer{UserID: finance.UserID}); code(err) != "panjar.not_found" {
		t.Fatalf("rincian pengajuan orang lain seharusnya tersembunyi: %v", err)
	}

	// Hanya pengaju yang mengubah, dengan versi terbaru.
	in.Version = p.Version
	in.Lines[1].Amount = rp(250_000)
	if _, err := svc.Update(e.Ctx, e.Company, p.ID, in, asFinance, finance); code(err) != "panjar.not_owner" {
		t.Fatalf("ubah oleh bukan pengaju: %v", err)
	}
	in.Version = p.Version - 1
	if _, err := svc.Update(e.Ctx, e.Company, p.ID, in, asHead, head); code(err) != "conflict.version" {
		t.Fatalf("versi basi: %v", err)
	}
	in.Version = p.Version
	if p, err = svc.Update(e.Ctx, e.Company, p.ID, in, asHead, head); err != nil || !p.Total.Equal(rp(550_000)) {
		t.Fatalf("ubah = %+v, %v", p, err)
	}

	// Keputusan: tidak boleh memutuskan pengajuan sendiri, tolak wajib beralasan, nominal tidak
	// boleh melebihi pengajuan.
	if _, err := svc.Decide(e.Ctx, e.Company, p.ID, panjar.Decision{Approve: true, Version: p.Version}, head); code(err) != "panjar.self_approval" {
		t.Fatalf("memutuskan pengajuan sendiri: %v", err)
	}
	if _, err := svc.Decide(e.Ctx, e.Company, p.ID, panjar.Decision{Version: p.Version}, finance); code(err) != "panjar.invalid" {
		t.Fatalf("tolak tanpa alasan: %v", err)
	}
	if _, err := svc.Decide(e.Ctx, e.Company, p.ID, panjar.Decision{Approve: true, Amount: ptr(rp(600_000)), Version: p.Version}, finance); code(err) != "panjar.invalid" {
		t.Fatalf("setuju melebihi pengajuan: %v", err)
	}
	p, err = svc.Decide(e.Ctx, e.Company, p.ID, panjar.Decision{Approve: true, Amount: ptr(rp(400_000)), Note: "Jasa teknisi pakai tarif kontrak", Version: p.Version}, finance)
	if err != nil || p.Status != panjar.Approved || p.ApprovedAmount == nil || !p.ApprovedAmount.Equal(rp(400_000)) || p.DecidedByName == "" || p.DecidedAt == nil {
		t.Fatalf("setujui = %+v, %v", p, err)
	}
	if _, err := svc.Cancel(e.Ctx, e.Company, p.ID, p.Version, head); code(err) != "panjar.status" {
		t.Fatalf("batal setelah disetujui: %v", err)
	}
	if _, err := svc.Decide(e.Ctx, e.Company, p.ID, panjar.Decision{Note: "x", Version: p.Version}, finance); code(err) != "panjar.status" {
		t.Fatalf("memutuskan dua kali: %v", err)
	}

	// Sisa anggaran unit = 2,4 jt − 500 rb realisasi − 400 rb panjar disetujui.
	u, err := svc.UnitBudget(e.Ctx, e.Company, dept.ID, asHead, today)
	if err != nil || !u.HasBudget || u.FiscalYear != 2026 || u.To != "2026-10" || !u.YearBudget.Equal(rp(2_400_000)) ||
		!u.Actual.Equal(rp(500_000)) || !u.Committed.Equal(rp(400_000)) || !u.Remaining.Equal(rp(1_500_000)) {
		t.Fatalf("anggaran unit = %+v, %v", u, err)
	}

	// Tandai dibayar dengan bukti kas keluar.
	if _, err := svc.MarkPaid(e.Ctx, e.Company, p.ID, panjar.Payment{Date: today, Version: p.Version}, finance); code(err) != "panjar.invalid" {
		t.Fatalf("bayar tanpa bukti: %v", err)
	}
	if _, err := svc.MarkPaid(e.Ctx, e.Company, p.ID, panjar.Payment{Date: day("2026-10-16"), Ref: "KK/2026/10/0001", Version: p.Version}, finance); code(err) != "panjar.invalid" {
		t.Fatalf("tanggal bayar setelah hari ini: %v", err)
	}
	p, err = svc.MarkPaid(e.Ctx, e.Company, p.ID, panjar.Payment{Date: today, Ref: "KK/2026/10/0001", Version: p.Version}, finance)
	if err != nil || p.Status != panjar.Paid || p.PaidAt == nil || *p.PaidAt != today || p.PaidRef != "KK/2026/10/0001" {
		t.Fatalf("dibayar = %+v, %v", p, err)
	}
	if u, _ := svc.UnitBudget(e.Ctx, e.Company, dept.ID, asHead, today); !u.Committed.IsZero() {
		t.Fatalf("panjar dibayar tidak lagi terikat: %+v", u)
	}
	other, err := e.Core.SaveDimension(e.Ctx, e.Company, "departemen", nil, core.DimensionInput{Code: "UMM", Name: "Unit Umum", Active: true}, e.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := svc.UnitBudget(e.Ctx, e.Company, other.ID, asFinance, today); err != nil || u.HasBudget {
		t.Fatalf("unit tanpa baris anggaran = %+v, %v", u, err)
	}

	// Pengajuan kedua dibatalkan pengaju, ketiga ditolak finance.
	in.Version = 0
	two, err := svc.Create(e.Ctx, e.Company, in, asHead, head)
	if err != nil || two.Number == p.Number {
		t.Fatalf("pengajuan kedua = %+v, %v", two, err)
	}
	if _, err := svc.Cancel(e.Ctx, e.Company, two.ID, two.Version, finance); code(err) != "panjar.not_owner" {
		t.Fatalf("batal oleh bukan pengaju: %v", err)
	}
	if two, err = svc.Cancel(e.Ctx, e.Company, two.ID, two.Version, head); err != nil || two.Status != panjar.Cancelled {
		t.Fatalf("batal = %+v, %v", two, err)
	}
	three, err := svc.Create(e.Ctx, e.Company, in, asHead, head)
	if err != nil {
		t.Fatal(err)
	}
	if three, err = svc.Decide(e.Ctx, e.Company, three.ID, panjar.Decision{Note: "Anggaran unit bulan ini habis", Version: three.Version}, finance); err != nil ||
		three.Status != panjar.Rejected || three.ApprovedAmount != nil || three.DecisionNote == "" {
		t.Fatalf("tolak = %+v, %v", three, err)
	}
	if l, _ := svc.Requests(e.Ctx, e.Company, asFinance, panjar.Filter{Status: panjar.Paid, From: day("2026-01-01"), To: day("2026-12-31")}); len(l.Items) != 1 || l.Items[0].ID != p.ID || l.Pending != 0 {
		t.Fatalf("saring dibayar = %+v", l)
	}
	if _, err := svc.Requests(e.Ctx, e.Company, asFinance, panjar.Filter{Status: "x"}); code(err) != "request.query" {
		t.Fatalf("status tidak dikenal: %v", err)
	}
}

// Kepala unit hanya mengelola unit yang ditetapkan Manager atau Pemilik: mengajukan, melihat
// pengajuan, dan membaca anggaran unit itu saja. Penetapan hanya untuk pengguna berperan terbatas
// di PT ini, dan unitnya harus departemen PT ini.
func TestPanjarUnitHeads(t *testing.T) {
	e := fixture.New(t)
	svc := e.Panjar
	head, finance := e.Other, e.Actor
	asHead, asFinance := panjar.Viewer{UserID: head.UserID}, panjar.Viewer{UserID: finance.UserID, All: true}
	unit := func(code string) uuid.UUID {
		d, err := e.Core.SaveDimension(e.Ctx, e.Company, "departemen", nil, core.DimensionInput{Code: code, Name: "Unit " + code, Active: true}, e.Actor)
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	tkn, umm := unit("TKN"), unit("UMM")
	in := func(dept uuid.UUID) panjar.Input {
		return panjar.Input{DepartmentID: dept, Purpose: "Kebutuhan unit", Lines: []panjar.LineInput{{Description: "Alat tulis", Amount: rp(100_000)}}}
	}
	all := panjar.Filter{From: day("2000-01-01"), To: day("2100-12-31")}

	if _, err := svc.SetUnits(e.Ctx, e.Company, head.UserID, []uuid.UUID{tkn}, finance); code(err) != "panjar.not_unit_head" {
		t.Fatalf("pengguna tanpa akses ke PT: %v", err)
	}
	if err := e.Core.Assign(e.Ctx, head.UserID, e.Company, "kepala_unit"); err != nil {
		t.Fatal(err)
	}
	heads, err := svc.UnitHeads(e.Ctx, e.Company)
	if err != nil || len(heads) != 1 || heads[0].UserID != head.UserID || heads[0].RoleName != "Kepala unit" || len(heads[0].DepartmentIDs) != 0 {
		t.Fatalf("daftar kepala unit = %+v, %v", heads, err)
	}
	if _, err := svc.SetUnits(e.Ctx, e.Company, finance.UserID, []uuid.UUID{tkn}, finance); code(err) != "panjar.not_unit_head" {
		t.Fatalf("Manager bukan kepala unit: %v", err)
	}

	// Belum ditetapkan: tidak bisa mengajukan untuk unit mana pun.
	if _, err := svc.Create(e.Ctx, e.Company, in(tkn), asHead, head); code(err) != "panjar.unit_not_assigned" {
		t.Fatalf("mengajukan tanpa unit: %v", err)
	}
	if l, _ := svc.Requests(e.Ctx, e.Company, asHead, all); !l.Restricted || l.Units == nil || len(l.Units) != 0 {
		t.Fatalf("daftar tanpa unit = %+v", l)
	}
	if _, err := svc.SetUnits(e.Ctx, e.Company, head.UserID, []uuid.UUID{uuid.Must(uuid.NewV7())}, finance); code(err) != "panjar.invalid" {
		t.Fatalf("unit asing: %v", err)
	}
	h, err := svc.SetUnits(e.Ctx, e.Company, head.UserID, []uuid.UUID{tkn, tkn}, finance)
	if err != nil || len(h.DepartmentIDs) != 1 || h.DepartmentIDs[0] != tkn {
		t.Fatalf("tetapkan unit = %+v, %v", h, err)
	}

	own, err := svc.Create(e.Ctx, e.Company, in(tkn), asHead, head)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(e.Ctx, e.Company, in(umm), asHead, head); code(err) != "panjar.unit_not_assigned" {
		t.Fatalf("mengajukan untuk unit lain: %v", err)
	}
	moved := in(umm)
	moved.Version = own.Version
	if _, err := svc.Update(e.Ctx, e.Company, own.ID, moved, asHead, head); code(err) != "panjar.unit_not_assigned" {
		t.Fatalf("memindahkan pengajuan ke unit lain: %v", err)
	}

	// Finance tidak dibatasi. Kepala unit melihat semua pengajuan unitnya, tidak unit lain.
	forTKN, err := svc.Create(e.Ctx, e.Company, in(tkn), asFinance, finance)
	if err != nil {
		t.Fatal(err)
	}
	forUMM, err := svc.Create(e.Ctx, e.Company, in(umm), asFinance, finance)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.Requests(e.Ctx, e.Company, asHead, all); len(l.Items) != 2 || len(l.Units) != 1 || l.Pending != 0 {
		t.Fatalf("daftar kepala unit = %+v", l)
	}
	if d, err := svc.Request(e.Ctx, e.Company, forTKN.ID, asHead); err != nil || d.Mine {
		t.Fatalf("pengajuan unitnya oleh orang lain = %+v, %v", d, err)
	}
	if _, err := svc.Request(e.Ctx, e.Company, forUMM.ID, asHead); code(err) != "panjar.not_found" {
		t.Fatalf("pengajuan unit lain seharusnya tersembunyi: %v", err)
	}
	if _, err := svc.UnitBudget(e.Ctx, e.Company, umm, asHead, day("2026-10-15")); code(err) != "panjar.unit_not_assigned" {
		t.Fatalf("anggaran unit lain: %v", err)
	}
	if _, err := svc.UnitBudget(e.Ctx, e.Company, tkn, asHead, day("2026-10-15")); err != nil {
		t.Fatalf("anggaran unitnya: %v", err)
	}

	// Penetapan dicabut: hanya pengajuannya sendiri yang masih terlihat.
	if _, err := svc.SetUnits(e.Ctx, e.Company, head.UserID, nil, finance); err != nil {
		t.Fatal(err)
	}
	if l, _ := svc.Requests(e.Ctx, e.Company, asHead, all); len(l.Items) != 1 || l.Items[0].ID != own.ID {
		t.Fatalf("setelah unit dicabut = %+v", l)
	}
}

func ptr[T any](v T) *T { return &v }
