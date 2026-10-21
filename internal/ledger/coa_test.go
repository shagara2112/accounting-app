package ledger

import (
	"strings"
	"testing"
)

// Template harus sah untuk setiap industri: kode unik, kelas induk sama dengan anak,
// maksimal 5 level, dan akun header selalu punya anak.
func TestTemplatesAreConsistent(t *testing.T) {
	for _, name := range Templates {
		accounts, err := Template(name)
		if err != nil {
			t.Fatal(err)
		}
		byCode := map[string]TemplateAccount{}
		children := map[string]int{}
		classes := map[byte]bool{}
		for _, a := range accounts {
			if _, dup := byCode[a.Code]; dup {
				t.Errorf("%s: kode %s dobel", name, a.Code)
			}
			byCode[a.Code] = a
			if classOfCode(a.Code) == 0 {
				t.Errorf("%s: format kode %s salah", name, a.Code)
			}
			if a.Level < 1 || a.Level > MaxLevel {
				t.Errorf("%s: level %s = %d", name, a.Code, a.Level)
			}
			if a.Level == 1 {
				classes[a.Code[0]] = true
				if a.Parent != "" || !a.Header || len(a.Code) != 1 {
					t.Errorf("%s: akun kelompok %s salah bentuk", name, a.Code)
				}
				continue
			}
			p, ok := byCode[a.Parent]
			switch {
			case !ok:
				t.Errorf("%s: induk %s untuk %s belum ada sebelumnya", name, a.Parent, a.Code)
			case !p.Header:
				t.Errorf("%s: induk %s bukan header", name, a.Parent)
			case p.Code[0] != a.Code[0]:
				t.Errorf("%s: %s berbeda kelas dengan induknya %s", name, a.Code, a.Parent)
			case p.Level != a.Level-1:
				t.Errorf("%s: level %s tidak berurutan", name, a.Code)
			}
			children[a.Parent]++
		}
		if len(classes) != 9 {
			t.Errorf("%s: hanya %d dari 9 akun kelompok", name, len(classes))
		}
		for _, a := range accounts {
			if a.Header && children[a.Code] == 0 && name != "kosong" {
				t.Errorf("%s: header %s %s tidak punya anak", name, a.Code, a.Name)
			}
		}
	}
}

// Akun yang dipakai data contoh dan aturan posting modul berikutnya harus ada di semua
// industri, dengan kode yang sama, supaya konsolidasi grup tinggal memetakan.
func TestTemplatesShareCommonCodes(t *testing.T) {
	common := strings.Fields(`1-1101 1-1111 1-1201 1-1401 1-1403 1-1501 1-2104 1-2204 2-1101 2-1201
		2-1203 2-1302 3-1001 3-2001 4-1001 5-1001 6-1001 6-2001 6-2002 6-3004 7-1001 8-1001 9-1001`)
	for _, name := range []string{"jasa", "konstruksi", "retail"} {
		accounts, _ := Template(name)
		postable := map[string]bool{}
		for _, a := range accounts {
			postable[a.Code] = !a.Header
		}
		for _, code := range common {
			if !postable[code] {
				t.Errorf("%s: akun detail %s tidak ada", name, code)
			}
		}
	}
	if _, err := Template("pertambangan"); err == nil {
		t.Error("template tidak dikenal seharusnya ditolak")
	}
}

func TestClassFromCode(t *testing.T) {
	cases := map[string]int{"1": 1, "1-1101": 1, "2-1000": 2, "9-1001.01": 9, "0-1000": 0, "10-100": 0, "1-": 0, "1-11a": 0, "": 0}
	for code, want := range cases {
		if got := classOfCode(code); got != want {
			t.Errorf("classOfCode(%q) = %d, seharusnya %d", code, got, want)
		}
	}
}

// Akun sistem yang dipakai aturan posting modul harus ada sebagai akun detail di setiap
// template industri, supaya PT baru langsung bisa memposting faktur, tagihan, dan pelunasan.
func TestRoleAccountsExistInEveryTemplate(t *testing.T) {
	for _, name := range []string{"jasa", "konstruksi", "retail"} {
		accounts, _ := Template(name)
		postable := map[string]bool{}
		for _, a := range accounts {
			postable[a.Code] = !a.Header
		}
		for role, info := range roles {
			found := false
			for _, code := range info.codes {
				found = found || postable[code]
			}
			if !found {
				t.Errorf("%s: akun sistem %s (%v) tidak ada", name, role, info.codes)
			}
		}
	}
}

// Setiap peran akun sistem tampil tepat sekali di halaman Akun sistem, dengan kelompok dan
// keterangan, dan kode bawaannya sah.
func TestRoleOrderCoversAllRoles(t *testing.T) {
	seen := map[Role]bool{}
	for _, r := range roleOrder {
		info, ok := roles[r]
		if !ok || seen[r] {
			t.Fatalf("peran %s tidak dikenal atau dobel di roleOrder", r)
		}
		if info.group == "" || info.purpose == "" || classOfCode(info.codes[0]) == 0 {
			t.Fatalf("peran %s belum lengkap: %+v", r, info)
		}
		seen[r] = true
	}
	if len(seen) != len(roles) {
		t.Fatalf("roleOrder memuat %d dari %d peran", len(seen), len(roles))
	}
}
