package rbac

import "testing"

func role(t *testing.T, key string) Perms {
	t.Helper()
	for _, r := range Builtin {
		if r.Key == key {
			return NewPerms(r.Permissions)
		}
	}
	t.Fatalf("peran %q tidak ada", key)
	return nil
}

// Mengikuti matriks izin enam persona di TDD.
func TestBuiltinRolesFollowMatrix(t *testing.T) {
	cases := []struct {
		role string
		perm string
		want bool
	}{
		{"pemilik", JournalVoid, true},
		{"manager", PeriodManage, true},
		{"manager", ApprovalManage, true},
		{"staf_akuntansi", JournalCreate, true},
		{"staf_akuntansi", AdjustmentCreate, true},
		{"staf_akuntansi", AdjustmentPost, true},
		{"staf_akuntansi", AccountCreate, true},
		{"staf_akuntansi", AccountEdit, false},
		{"manager", AccountEdit, true},
		{"auditor", AccountCreate, false},
		{"staf_akuntansi", JournalVoid, false},
		{"staf_akuntansi", SalesVoid, false},
		{"staf_akuntansi", PeriodManage, false},
		{"staf_akuntansi", AuditView, false},
		{"staf_pajak", TaxExport, true},
		{"staf_pajak", LedgerView, false},
		{"staf_pajak", SalesCreate, false},
		{"auditor", AuditView, true},
		{"auditor", LedgerView, true},
		{"auditor", JournalCreate, false},
		{"auditor", DataExport, true},
		{"admin", UserManage, true},
		{"admin", SystemManage, true},
		{"admin", LedgerView, false},
		{"admin", DataExport, false},
		{"staf_akuntansi", PanjarApprove, true},
		{"manager", PanjarApprove, true},
		{"auditor", PanjarView, true},
		{"auditor", PanjarApprove, false},
		{"kepala_unit", PanjarCreate, true},
		{"kepala_unit", CompanyView, true},
		{"kepala_unit", PanjarView, false},
		{"kepala_unit", PanjarApprove, false},
		{"kepala_unit", LedgerView, false},
		{"pemilik", PanjarUnits, true},
		{"manager", PanjarUnits, true},
		{"staf_akuntansi", PanjarUnits, false},
		{"kepala_unit", PanjarUnits, false},
	}
	for _, c := range cases {
		if got := role(t, c.role).Has(c.perm); got != c.want {
			t.Errorf("%s punya %s = %v, seharusnya %v", c.role, c.perm, got, c.want)
		}
	}
}

func TestBuiltinRolesUseCatalogOnly(t *testing.T) {
	catalog := NewPerms(All)
	for _, r := range Builtin {
		for _, p := range r.Permissions {
			if !catalog.Has(p) {
				t.Errorf("peran %s memakai izin di luar katalog: %s", r.Key, p)
			}
		}
	}
}

func TestCatalogCoversEveryPermissionOnce(t *testing.T) {
	seen := map[string]int{}
	for _, g := range Catalog {
		for _, p := range g.Perms {
			seen[p.Key]++
		}
	}
	for _, p := range All {
		if seen[p] != 1 {
			t.Errorf("izin %s muncul %d kali di Catalog", p, seen[p])
		}
		delete(seen, p)
	}
	for p := range seen {
		t.Errorf("izin %s ada di Catalog tetapi tidak di All", p)
	}
}
