package admin

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/modules/admin/internal/store"
	"lajur/internal/modules/core"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
)

// Industries adalah pilihan industri PT; masing-masing punya template bagan akun.
var Industries = []struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}{
	{"jasa", "Jasa"}, {"konstruksi", "Konstruksi"}, {"retail", "Retail dan distribusi"},
}

type Company struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	ShortName string    `json:"shortName"`
	Industry  *string   `json:"industry,omitempty"`
	NPWP      *string   `json:"npwp,omitempty"`
	Active    bool      `json:"active"`
	Users     int       `json:"users"`
	CreatedAt time.Time `json:"createdAt"`
}

type NewCompany struct {
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Industry  string `json:"industry"`
	NPWP      string `json:"npwp"`
}

func (s *service) Companies(ctx context.Context) ([]Company, error) {
	rows, err := s.q(ctx).ListCompanies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Company, 0, len(rows))
	for _, r := range rows {
		out = append(out, Company{
			ID: r.ID, Name: r.Name, ShortName: r.ShortName, Industry: r.Industry, NPWP: r.Npwp,
			Active: r.Active, Users: int(r.Users), CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// checkCompany memeriksa isian PT; prefix membedakan nama field di setup wizard ("company.").
func (s *service) checkCompany(ctx context.Context, in NewCompany, prefix string) (NewCompany, map[string]string, error) {
	in.Name, in.ShortName, in.NPWP = strings.TrimSpace(in.Name), strings.TrimSpace(in.ShortName), strings.TrimSpace(in.NPWP)
	fields := map[string]string{}
	switch {
	case in.Name == "":
		fields[prefix+"name"] = "Nama PT wajib diisi."
	case utf8.RuneCountInString(in.Name) > 120:
		fields[prefix+"name"] = "Nama PT maksimal 120 karakter."
	default:
		taken, err := s.q(ctx).CompanyNameTaken(ctx, in.Name)
		if err != nil {
			return in, nil, err
		}
		if taken {
			fields[prefix+"name"] = "Sudah ada PT dengan nama ini."
		}
	}
	switch {
	case in.ShortName == "":
		fields[prefix+"shortName"] = "Nama singkat wajib diisi."
	case utf8.RuneCountInString(in.ShortName) > 24:
		fields[prefix+"shortName"] = "Nama singkat maksimal 24 karakter."
	}
	known := in.Industry == ""
	for _, i := range Industries {
		known = known || i.Key == in.Industry
	}
	if !known {
		fields[prefix+"industry"] = "Industri tidak dikenal."
	}
	if in.NPWP != "" && !npwpOK(in.NPWP) {
		fields[prefix+"npwp"] = "NPWP harus 15 atau 16 digit."
	}
	return in, fields, nil
}

func npwpOK(s string) bool {
	n := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			n++
		case r == '.' || r == '-' || r == ' ':
		default:
			return false
		}
	}
	return n == 15 || n == 16
}

// createCompany membuat PT beserta bagan akun dari template industrinya (template "kosong"
// bila industri tidak dipilih) dan memberi pembuatnya peran roleKey di PT itu.
func (s *service) createCompany(ctx context.Context, in NewCompany, userID uuid.UUID, roleKey string, actor audit.Actor) (core.Company, error) {
	nc := core.NewCompany{Name: in.Name, ShortName: in.ShortName}
	template := "kosong"
	if in.Industry != "" {
		nc.Industry, template = &in.Industry, in.Industry
	}
	if in.NPWP != "" {
		nc.NPWP = &in.NPWP
	}
	c, err := s.Core.CreateCompany(ctx, nc)
	if err != nil {
		return c, err
	}
	if _, err := s.Ledger.ApplyTemplate(ctx, c.ID, template, actor); err != nil {
		return c, err
	}
	q := s.q(ctx)
	role, err := q.GetRoleByKey(ctx, roleKey)
	if err != nil {
		return c, err
	}
	if err := q.SetAccess(ctx, store.SetAccessParams{UserID: userID, CompanyID: c.ID, RoleID: role.ID}); err != nil {
		return c, err
	}
	_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
		UserID: actor.UserRef(), CompanyID: &c.ID, Action: "core.company.create", ObjectType: "company", ObjectID: c.ID.String(), IP: actor.IP,
		After: map[string]any{"name": c.Name, "shortName": c.ShortName, "industry": in.Industry, "template": template, "creatorRole": roleKey},
	})
	return c, err
}

// CreateCompany menambah PT ke grup. Pembuatnya mendapat peran yang sama dengan perannya di PT aktif.
func (s *service) CreateCompany(ctx context.Context, in NewCompany, creatorRole string, actor audit.Actor) (Company, error) {
	var out Company
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		in, fields, err := s.checkCompany(ctx, in, "")
		if err != nil {
			return err
		}
		if err := invalid(fields); err != nil {
			return err
		}
		c, err := s.createCompany(ctx, in, actor.UserID, creatorRole, actor)
		if err != nil {
			return err
		}
		list, err := s.Companies(ctx)
		if err != nil {
			return err
		}
		for _, x := range list {
			if x.ID == c.ID {
				out = x
			}
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------- setup wizard

type SetupInput struct {
	License *LicenseInput `json:"license"`
	Company NewCompany    `json:"company"`
	Admin   struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	} `json:"admin"`
}

func (s *service) RemoteLicense() bool { return s.License.Remote() }

func (s *service) SetupRequired(ctx context.Context) (bool, error) {
	n, err := s.q(ctx).CountUsers(ctx)
	return n == 0, err
}

// ErrSetupDone: setup hanya bisa dijalankan sekali, selama belum ada pengguna.
var ErrSetupDone = httpx.Conflict("setup.done", "Lajur sudah disiapkan. Masuk dengan akun yang sudah ada.")

// Setup menyiapkan instalasi baru: mengaktifkan lisensi, membuat PT pertama dengan bagan
// akunnya, dan akun Pemilik pertama. Mengembalikan id pengguna Pemilik itu.
func (s *service) Setup(ctx context.Context, in SetupInput, ip string) (uuid.UUID, error) {
	adminID := uuid.Must(uuid.NewV7())
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		if err := q.LockSetup(ctx); err != nil {
			return err
		}
		required, err := s.SetupRequired(ctx)
		if err != nil {
			return err
		}
		if !required {
			return ErrSetupDone
		}

		company, fields, err := s.checkCompany(ctx, in.Company, "company.")
		if err != nil {
			return err
		}
		name, email, profile := checkProfile(in.Admin.Name, in.Admin.Email)
		for k, v := range profile {
			fields["admin."+k] = v
		}
		hash, err := auth.HashPassword(in.Admin.Password)
		if errors.Is(err, auth.ErrWeakPassword) {
			fields["admin.password"] = "Kata sandi minimal 10 karakter."
		} else if err != nil {
			return err
		}
		if s.License.Remote() && in.License == nil {
			fields["license.licenseKey"] = "Isi Client ID dan license key dari pemilik aplikasi."
		}
		if err := invalid(fields); err != nil {
			return err
		}
		if s.License.Remote() {
			if err := s.License.Activate(ctx, *in.License); err != nil {
				return err
			}
		}

		if err := q.InsertUser(ctx, store.InsertUserParams{ID: adminID, Email: email, Name: name, PasswordHash: hash}); err != nil {
			return err
		}
		actor := audit.Actor{UserID: adminID, IP: ip}
		c, err := s.createCompany(ctx, company, adminID, "pemilik", actor)
		if err != nil {
			return err
		}
		if err := q.MarkSetupDone(ctx); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: &adminID, CompanyID: &c.ID, Action: "platform.setup", ObjectType: "installation", IP: ip,
			After: map[string]any{"company": c.Name, "admin": email, "remoteLicense": s.License.Remote()},
		})
		return err
	})
	return adminID, err
}

// LocalLicense dipakai di mode pengembangan: tidak ada yang perlu diaktifkan.
type LocalLicense struct{}

func (LocalLicense) Remote() bool                                 { return false }
func (LocalLicense) Activate(context.Context, LicenseInput) error { return nil }
