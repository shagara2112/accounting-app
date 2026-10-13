package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/core"
	"lajur/internal/modules/sales"
	"lajur/internal/modules/trade"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/dbtest"
	"lajur/internal/platform/license"
	"lajur/internal/platform/money"
	"lajur/internal/platform/store"
)

const testPassword = "kata-sandi-uji-2027"

type testEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	srv      *httptest.Server
	core     core.Service
	companyA uuid.UUID
	companyB uuid.UUID
}

func newEnv(t *testing.T, lic license.Provider) *testEnv {
	t.Helper()
	pool := dbtest.Pool(t)
	ctx := context.Background()
	svc := core.New(pool)
	a, err := svc.CreateCompany(ctx, core.NewCompany{Name: "PT Uji A", ShortName: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateCompany(ctx, core.NewCompany{Name: "PT Uji B", ShortName: "B"})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := New(Deps{
		Pool: pool, Log: slog.New(slog.DiscardHandler), Sessions: auth.NewSessions(pool, false),
		License: lic, Web: fstest.MapFS{"index.html": {Data: []byte("<html>lajur</html>")}},
	}.WithModules())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testEnv{t: t, pool: pool, srv: srv, core: svc, companyA: a.ID, companyB: b.ID}
}

func activeLicense() license.Provider {
	return license.Static{S: license.Status{State: license.Active, Message: "Lisensi aktif."}}
}

func (e *testEnv) user(email, role string, companies ...uuid.UUID) {
	e.t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := store.New(e.pool).CreateUser(ctx, store.CreateUserParams{
		ID: uuid.Must(uuid.NewV7()), Email: email, Name: email, PasswordHash: hash,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, c := range companies {
		if err := e.core.Assign(ctx, u.ID, c, role); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *testEnv) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

type result struct {
	status int
	body   map[string]any
	header http.Header
}

func (e *testEnv) do(c *http.Client, method, path string, body any, headers ...string) result {
	e.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rdr)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return result{status: resp.StatusCode, body: out, header: resp.Header}
}

func (e *testEnv) login(c *http.Client, email, password string) result {
	return e.do(c, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

func expect(t *testing.T, r result, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, seharusnya %d (body %v)", r.status, status, r.body)
	}
	if code != "" && r.body["code"] != code {
		t.Fatalf("code = %v, seharusnya %s", r.body["code"], code)
	}
}

func TestLoginSessionAndCompanyScope(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("pemilik@uji.test", "pemilik", e.companyA)
	c := e.client()

	expect(t, e.login(c, "pemilik@uji.test", "salah-kata-sandi"), http.StatusUnauthorized, "auth.invalid_credentials")
	expect(t, e.login(c, "tidak.ada@uji.test", testPassword), http.StatusUnauthorized, "auth.invalid_credentials")
	expect(t, e.login(c, "Pemilik@Uji.test", testPassword), http.StatusOK, "")

	me := e.do(c, http.MethodGet, "/api/v1/me", nil)
	expect(t, me, http.StatusOK, "")
	if me.body["activeCompanyId"] != e.companyA.String() {
		t.Fatalf("PT aktif = %v, seharusnya %s", me.body["activeCompanyId"], e.companyA)
	}
	if n := len(me.body["companies"].([]any)); n != 1 {
		t.Fatalf("jumlah PT = %d, seharusnya 1", n)
	}
	if me.body["companies"].([]any)[0].(map[string]any)["pkp"] != true {
		t.Fatal("status PKP PT tidak terkirim di /me; formulir faktur butuh ini untuk default PPN")
	}

	// Pengguna tidak boleh berpindah ke, atau membaca data, PT yang bukan aksesnya.
	expect(t, e.do(c, http.MethodPost, "/api/v1/session/company", map[string]any{"companyId": e.companyB}), http.StatusForbidden, "company.denied")
	expect(t, e.do(c, http.MethodGet, "/api/v1/audit", nil, "X-Company-Id", e.companyB.String()), http.StatusForbidden, "company.denied")

	list := e.do(c, http.MethodGet, "/api/v1/audit", nil)
	expect(t, list, http.StatusOK, "")
	actions := map[string]bool{}
	for _, it := range list.body["items"].([]any) {
		actions[it.(map[string]any)["action"].(string)] = true
	}
	if !actions["auth.login"] || !actions["auth.login_failed"] {
		t.Fatalf("audit trail tidak mencatat login: %v", actions)
	}
	verify := e.do(c, http.MethodGet, "/api/v1/audit/verify", nil)
	expect(t, verify, http.StatusOK, "")
	if verify.body["ok"] != true {
		t.Fatalf("rantai audit dianggap rusak: %v", verify.body)
	}

	expect(t, e.do(c, http.MethodPost, "/api/v1/auth/logout", nil), http.StatusNoContent, "")
	expect(t, e.do(c, http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "auth.required")
}

func TestLockoutAfterFiveFailures(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("akuntansi@uji.test", "staf_akuntansi", e.companyA)
	c := e.client()
	for i := 0; i < 5; i++ {
		expect(t, e.login(c, "akuntansi@uji.test", "tebakan-salah-123"), http.StatusUnauthorized, "auth.invalid_credentials")
	}
	expect(t, e.login(c, "akuntansi@uji.test", testPassword), http.StatusLocked, "auth.locked")
}

func TestRoleWithoutPermissionIsDenied(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("admin@uji.test", "admin", e.companyA)
	c := e.client()
	expect(t, e.login(c, "admin@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(c, http.MethodGet, "/api/v1/audit", nil), http.StatusForbidden, "rbac.denied")
}

func TestSuspendedLicenseLocksDataButAllowsLogin(t *testing.T) {
	e := newEnv(t, license.Static{S: license.Status{State: license.Suspended, Message: "Lisensi ditangguhkan."}})
	e.user("pemilik@uji.test", "pemilik", e.companyA)
	c := e.client()
	expect(t, e.login(c, "pemilik@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(c, http.MethodGet, "/api/v1/system/license", nil), http.StatusOK, "")
	expect(t, e.do(c, http.MethodGet, "/api/v1/companies", nil), http.StatusForbidden, "license.locked")
}

func TestCrossOriginWriteIsRejected(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("pemilik@uji.test", "pemilik", e.companyA)
	r := e.do(e.client(), http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "pemilik@uji.test", "password": testPassword}, "Origin", "http://situs-lain.test")
	expect(t, r, http.StatusForbidden, "request.origin")
}

func TestAuditTrailIsAppendOnlyAndDetectsTampering(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("pemilik@uji.test", "pemilik", e.companyA)
	c := e.client()
	expect(t, e.login(c, "pemilik@uji.test", "salah-kata-sandi"), http.StatusUnauthorized, "")
	expect(t, e.login(c, "pemilik@uji.test", testPassword), http.StatusOK, "")

	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, "UPDATE platform.audit_log SET action = 'diubah' WHERE seq = 1"); err == nil {
		t.Fatal("UPDATE audit_log seharusnya ditolak trigger append-only")
	}
	if _, err := e.pool.Exec(ctx, "DELETE FROM platform.audit_log"); err == nil {
		t.Fatal("DELETE audit_log seharusnya ditolak trigger append-only")
	}

	// Simulasikan orang yang punya akses superuser mematikan trigger lalu mengubah baris lama.
	for _, sql := range []string{
		"ALTER TABLE platform.audit_log DISABLE TRIGGER audit_log_append_only",
		"UPDATE platform.audit_log SET action = 'auth.dirusak' WHERE seq = 1",
		"ALTER TABLE platform.audit_log ENABLE TRIGGER audit_log_append_only",
	} {
		if _, err := e.pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	res, err := audit.Verify(ctx, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenAt != 1 {
		t.Fatalf("pengubahan tidak terdeteksi: %+v", res)
	}
}

func TestUnknownAPIAndSPAFallback(t *testing.T) {
	e := newEnv(t, activeLicense())
	expect(t, e.do(e.client(), http.MethodGet, "/api/v1/tidak-ada", nil), http.StatusNotFound, "api.not_found")

	resp, err := http.Get(e.srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "lajur") {
		t.Fatalf("SPA fallback gagal: %d %s", resp.StatusCode, b)
	}
}

// ledgerEnv menyiapkan bagan akun jasa di PT A dan mengembalikan id akun per kode.
func (e *testEnv) ledgerAccounts() map[string]string {
	e.t.Helper()
	ctx := context.Background()
	svc := ledger.New(e.pool)
	if _, err := svc.ApplyTemplate(ctx, e.companyA, "jasa", ledger.Actor{}); err != nil {
		e.t.Fatal(err)
	}
	list, err := svc.Accounts(ctx, e.companyA)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range list {
		out[a.Code] = a.ID.String()
	}
	return out
}

func journalBody(acc map[string]string, amount string) map[string]any {
	return map[string]any{
		"date": "2026-10-05", "memo": "Setoran modal",
		"lines": []map[string]any{
			{"accountId": acc["1-1111"], "debit": amount, "credit": "0", "memo": ""},
			{"accountId": acc["3-1001"], "debit": "0", "credit": amount, "memo": ""},
		},
	}
}

// Klik ganda atau kirim ulang dengan Idempotency-Key yang sama tidak membuat jurnal kedua.
func TestJournalPostIsIdempotent(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("akuntansi@uji.test", "staf_akuntansi", e.companyA)
	acc := e.ledgerAccounts()
	c := e.client()
	expect(t, e.login(c, "akuntansi@uji.test", testPassword), http.StatusOK, "")

	expect(t, e.do(c, http.MethodPost, "/api/v1/journals", journalBody(acc, "1000000")), http.StatusBadRequest, "idempotency.key_required")

	first := e.do(c, http.MethodPost, "/api/v1/journals", journalBody(acc, "1000000"), "Idempotency-Key", "kunci-uji-0001")
	expect(t, first, http.StatusCreated, "")
	again := e.do(c, http.MethodPost, "/api/v1/journals", journalBody(acc, "1000000"), "Idempotency-Key", "kunci-uji-0001")
	expect(t, again, http.StatusCreated, "")
	if again.body["id"] != first.body["id"] || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("kiriman ulang membuat jurnal baru: %v vs %v", first.body, again.body)
	}
	expect(t, e.do(c, http.MethodPost, "/api/v1/journals", journalBody(acc, "2000000"), "Idempotency-Key", "kunci-uji-0001"),
		http.StatusUnprocessableEntity, "idempotency.mismatch")

	// Posting yang gagal tidak "menghabiskan" kuncinya.
	bad := journalBody(acc, "1000000")
	bad["lines"].([]map[string]any)[1]["credit"] = "999999"
	unbalanced := e.do(c, http.MethodPost, "/api/v1/journals", bad, "Idempotency-Key", "kunci-uji-0002")
	expect(t, unbalanced, http.StatusUnprocessableEntity, "ledger.unbalanced")
	if !strings.Contains(unbalanced.body["detail"].(string), "selisih Rp 1.") {
		t.Fatalf("pesan selisih: %v", unbalanced.body["detail"])
	}
	expect(t, e.do(c, http.MethodPost, "/api/v1/journals", journalBody(acc, "500000"), "Idempotency-Key", "kunci-uji-0002"), http.StatusCreated, "")

	list := e.do(c, http.MethodGet, "/api/v1/journals?from=2026-10-01&to=2026-10-31", nil)
	expect(t, list, http.StatusOK, "")
	if n := len(list.body["items"].([]any)); n != 2 {
		t.Fatalf("jumlah jurnal = %d, seharusnya 2", n)
	}
}

// Matriks izin buku besar: Staf akuntansi memposting tapi tidak bisa void atau mengubah akun;
// Auditor hanya membaca; Manager bisa void dan mengunci periode.
func TestLedgerPermissionsFollowPersonas(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("akuntansi@uji.test", "staf_akuntansi", e.companyA)
	e.user("auditor@uji.test", "auditor", e.companyA)
	e.user("manager@uji.test", "manager", e.companyA)
	e.user("pajak@uji.test", "staf_pajak", e.companyA)
	acc := e.ledgerAccounts()

	staf := e.client()
	expect(t, e.login(staf, "akuntansi@uji.test", testPassword), http.StatusOK, "")
	posted := e.do(staf, http.MethodPost, "/api/v1/journals", journalBody(acc, "750000"), "Idempotency-Key", "kunci-staf-01")
	expect(t, posted, http.StatusCreated, "")
	id := posted.body["id"].(string)
	adj := journalBody(acc, "100000")
	adj["autoReverse"] = true
	adj["date"] = "2026-10-31"
	expect(t, e.do(staf, http.MethodPost, "/api/v1/adjustments", adj, "Idempotency-Key", "kunci-staf-02"), http.StatusCreated, "")
	voidBody := map[string]any{"date": "2026-10-06", "reason": "Salah akun bank"}
	expect(t, e.do(staf, http.MethodPost, "/api/v1/journals/"+id+"/void", voidBody, "Idempotency-Key", "kunci-staf-03"), http.StatusForbidden, "rbac.denied")
	expect(t, e.do(staf, http.MethodPatch, "/api/v1/ledger/accounts/"+acc["1-1101"], map[string]any{"name": "Kas", "active": true, "version": 1}),
		http.StatusForbidden, "rbac.denied")
	expect(t, e.do(staf, http.MethodPost, "/api/v1/ledger/accounts",
		map[string]any{"code": "1-1113", "name": "Bank BRI", "parentId": acc["1-1100"], "postable": true}), http.StatusCreated, "")
	expect(t, e.do(staf, http.MethodPost, "/api/v1/ledger/periods/2026-10/status", map[string]any{"status": "adjusting", "reason": ""}),
		http.StatusForbidden, "rbac.denied")

	auditor := e.client()
	expect(t, e.login(auditor, "auditor@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(auditor, http.MethodGet, "/api/v1/journals/"+id, nil), http.StatusOK, "")
	expect(t, e.do(auditor, http.MethodGet, "/api/v1/ledger/worksheet?month=2026-10", nil), http.StatusOK, "")
	expect(t, e.do(auditor, http.MethodPost, "/api/v1/journals", journalBody(acc, "1"), "Idempotency-Key", "kunci-audit-1"), http.StatusForbidden, "rbac.denied")

	pajak := e.client()
	expect(t, e.login(pajak, "pajak@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(pajak, http.MethodGet, "/api/v1/journals", nil), http.StatusForbidden, "rbac.denied")

	manager := e.client()
	expect(t, e.login(manager, "manager@uji.test", testPassword), http.StatusOK, "")
	voided := e.do(manager, http.MethodPost, "/api/v1/journals/"+id+"/void", voidBody, "Idempotency-Key", "kunci-mgr-01")
	expect(t, voided, http.StatusOK, "")
	if voided.body["reversal"].(map[string]any)["number"] != "JV/2026/10/0001" {
		t.Fatalf("void = %v", voided.body)
	}
	expect(t, e.do(manager, http.MethodPost, "/api/v1/ledger/periods/2026-10/status", map[string]any{"status": "locked", "reason": ""}),
		http.StatusNoContent, "")
	expect(t, e.do(staf, http.MethodPost, "/api/v1/journals", journalBody(acc, "1"), "Idempotency-Key", "kunci-staf-04"),
		http.StatusUnprocessableEntity, "period.locked")

	ws := e.do(auditor, http.MethodGet, "/api/v1/ledger/worksheet?month=2026-10", nil)
	if ws.body["balanced"] != true {
		t.Fatalf("neraca lajur tidak seimbang: %v", ws.body["final"])
	}
	audit := e.do(manager, http.MethodGet, "/api/v1/audit", nil)
	actions := map[string]bool{}
	for _, it := range audit.body["items"].([]any) {
		actions[it.(map[string]any)["action"].(string)] = true
	}
	for _, a := range []string{"ledger.journal.post", "ledger.journal.void", "ledger.account.create", "ledger.period.status"} {
		if !actions[a] {
			t.Errorf("audit trail tidak mencatat %s", a)
		}
	}
}

// Lisensi berakhir: data tetap bisa dibaca dan diekspor, tetapi tidak bisa diposting.
func TestExpiredLicenseMakesLedgerReadOnly(t *testing.T) {
	e := newEnv(t, license.Static{S: license.Status{State: license.Expired, Message: "Lisensi berakhir."}})
	e.user("akuntansi@uji.test", "staf_akuntansi", e.companyA)
	acc := e.ledgerAccounts()
	c := e.client()
	expect(t, e.login(c, "akuntansi@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(c, http.MethodGet, "/api/v1/ledger/accounts", nil), http.StatusOK, "")
	expect(t, e.do(c, http.MethodPost, "/api/v1/journals", journalBody(acc, "1000"), "Idempotency-Key", "kunci-exp-01"),
		http.StatusForbidden, "license.read_only")
}

// Matriks izin penjualan: Staf akuntansi membuat dan memposting faktur tetapi tidak bisa void;
// Manager bisa void; Staf pajak tidak melihat penjualan; Auditor hanya membaca.
func TestSalesPermissionsFollowPersonas(t *testing.T) {
	e := newEnv(t, activeLicense())
	e.user("akuntansi@uji.test", "staf_akuntansi", e.companyA)
	e.user("manager@uji.test", "manager", e.companyA)
	e.user("pajak@uji.test", "staf_pajak", e.companyA)
	e.user("auditor@uji.test", "auditor", e.companyA)
	acc := e.ledgerAccounts()

	staf := e.client()
	expect(t, e.login(staf, "akuntansi@uji.test", testPassword), http.StatusOK, "")
	cust := e.do(staf, http.MethodPost, "/api/v1/contacts", map[string]any{
		"name": "PT Pelanggan", "isCustomer": true, "npwp": "1234567890123456", "pkp": true, "paymentTermsDays": 30,
	}, "Idempotency-Key", "kontak-uji-01")
	expect(t, cust, http.StatusCreated, "")
	draft := e.do(staf, http.MethodPost, "/api/v1/sales/invoices", map[string]any{
		"customerId": cust.body["id"], "date": "2026-10-05", "memo": "Jasa",
		"lines": []map[string]any{{"description": "Konsultasi", "accountId": acc["4-1001"], "qty": "1", "unitPrice": "1000000", "discountPct": "0", "taxCode": "PPN"}},
	}, "Idempotency-Key", "faktur-uji-01")
	expect(t, draft, http.StatusCreated, "")
	id := draft.body["id"].(string)
	posted := e.do(staf, http.MethodPost, "/api/v1/sales/invoices/"+id+"/post", map[string]any{}, "Idempotency-Key", "faktur-uji-02")
	expect(t, posted, http.StatusOK, "")
	if posted.body["number"] != "FJ/2026/10/0001" || posted.body["total"] != "1110000.0000" {
		t.Fatalf("faktur diposting = %v / %v", posted.body["number"], posted.body["total"])
	}
	voidBody := map[string]any{"date": "2026-10-06", "reason": "Salah pelanggan"}
	expect(t, e.do(staf, http.MethodPost, "/api/v1/sales/invoices/"+id+"/void", voidBody, "Idempotency-Key", "faktur-uji-03"), http.StatusForbidden, "rbac.denied")

	pajak := e.client()
	expect(t, e.login(pajak, "pajak@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(pajak, http.MethodGet, "/api/v1/sales/invoices", nil), http.StatusForbidden, "rbac.denied")
	expect(t, e.do(pajak, http.MethodGet, "/api/v1/contacts", nil), http.StatusOK, "")

	auditor := e.client()
	expect(t, e.login(auditor, "auditor@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(auditor, http.MethodGet, "/api/v1/sales/invoices/"+id, nil), http.StatusOK, "")
	expect(t, e.do(auditor, http.MethodPost, "/api/v1/contacts", map[string]any{"name": "X", "isSupplier": true}, "Idempotency-Key", "kontak-uji-02"),
		http.StatusForbidden, "rbac.denied")

	manager := e.client()
	expect(t, e.login(manager, "manager@uji.test", testPassword), http.StatusOK, "")
	voided := e.do(manager, http.MethodPost, "/api/v1/sales/invoices/"+id+"/void", voidBody, "Idempotency-Key", "faktur-uji-04")
	expect(t, voided, http.StatusOK, "")
	if voided.body["status"] != "void" {
		t.Fatalf("status setelah void = %v", voided.body["status"])
	}
}

// Staf pajak mengekspor XML faktur keluaran tanpa akses ke menu penjualan, dan ekspor tetap
// berjalan walau lisensi sudah berakhir (PRD: ekspor tidak pernah dikunci).
func TestTaxExportForTaxStaffWithExpiredLicense(t *testing.T) {
	e := newEnv(t, license.Static{S: license.Status{State: license.Expired, Message: "Lisensi berakhir."}})
	e.user("pajak@uji.test", "staf_pajak", e.companyA)
	acc := e.ledgerAccounts()
	ctx := context.Background()
	m := Modules(e.pool)
	c, _ := m.Core.Company(ctx, e.companyA)
	if _, err := m.Core.UpdateTaxProfile(ctx, e.companyA, core.TaxProfile{NPWP: "0123456789012345", PKP: true, Version: c.Version}, audit.Actor{}); err != nil {
		t.Fatal(err)
	}
	cust, err := m.Core.CreateContact(ctx, e.companyA, core.ContactInput{Name: "PT Pembeli", IsCustomer: true, NPWP: "1111222233334444", PKP: true}, audit.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := m.Sales.CreateInvoice(ctx, e.companyA, sales.InvoiceInput{CustomerID: cust.ID, Date: civilDay("2026-10-05"),
		Lines: []trade.LineInput{{AccountID: uuid.MustParse(acc["4-1001"]), Qty: money.FromInt(1), UnitPrice: money.FromInt(5_000_000), TaxCode: "PPN"}}}, audit.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Sales.PostInvoice(ctx, e.companyA, inv.ID, audit.Actor{}); err != nil {
		t.Fatal(err)
	}

	c2 := e.client()
	expect(t, e.login(c2, "pajak@uji.test", testPassword), http.StatusOK, "")
	expect(t, e.do(c2, http.MethodGet, "/api/v1/sales/invoices", nil), http.StatusForbidden, "rbac.denied")
	list := e.do(c2, http.MethodGet, "/api/v1/tax/output-invoices?month=2026-10", nil)
	expect(t, list, http.StatusOK, "")
	if n := len(list.body["items"].([]any)); n != 1 {
		t.Fatalf("faktur keluaran = %d", n)
	}

	body, _ := json.Marshal(map[string]any{"month": "2026-10", "ids": []string{inv.ID.String()}})
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/tax/output-invoices/export", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c2.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	xml, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(xml), "<TaxInvoiceBulk") || !strings.Contains(resp.Header.Get("Content-Disposition"), ".xml") {
		t.Fatalf("ekspor XML = %d %s", resp.StatusCode, xml)
	}
	// Mengubah status faktur pajak adalah penulisan data biasa: terkunci saat lisensi berakhir.
	expect(t, e.do(c2, http.MethodPost, "/api/v1/tax/output-invoices/"+inv.ID.String()+"/approve", map[string]any{"number": "04002600000000031"}),
		http.StatusForbidden, "license.read_only")
}

func civilDay(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}
