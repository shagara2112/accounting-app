package core

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/core/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
)

// TaxProfile adalah identitas pajak PT yang dicetak di faktur pajak dan XML Coretax.
type TaxProfile struct {
	NPWP    string `json:"npwp"`
	NITKU   string `json:"nitku"`
	Address string `json:"address"`
	PKP     bool   `json:"pkp"`
	Version int    `json:"version"`
}

func (p *TaxProfile) validate() error {
	p.NPWP, p.NITKU, p.Address = digits(p.NPWP), digits(p.NITKU), strings.TrimSpace(p.Address)
	f := map[string]string{}
	if p.NPWP != "" && (len(p.NPWP) != 16 || !allDigits.MatchString(p.NPWP)) {
		f["npwp"] = "NPWP harus 16 digit angka."
	}
	if p.PKP && p.NPWP == "" {
		f["npwp"] = "PT PKP wajib punya NPWP."
	}
	if p.NITKU != "" {
		switch {
		case len(p.NITKU) != 22 || !allDigits.MatchString(p.NITKU):
			f["nitku"] = "NITKU harus 22 digit angka (NPWP diikuti 6 digit)."
		case p.NPWP != "" && !strings.HasPrefix(p.NITKU, p.NPWP):
			f["nitku"] = "NITKU harus diawali NPWP PT ini."
		}
	}
	if utf8.RuneCountInString(p.Address) > 500 {
		f["address"] = "Alamat maksimal 500 karakter."
	}
	if len(f) > 0 {
		msg := f["npwp"]
		if msg == "" {
			msg = f["nitku"] + f["address"]
		}
		return httpx.Unprocessable("company.invalid", msg).WithFields(f)
	}
	return nil
}

func (s *service) UpdateTaxProfile(ctx context.Context, id uuid.UUID, in TaxProfile, actor audit.Actor) (Company, error) {
	if err := in.validate(); err != nil {
		return Company{}, err
	}
	var out Company
	err := db.Run(ctx, s.pool, func(ctx context.Context) error {
		before, err := s.Company(ctx, id)
		if err != nil {
			return err
		}
		r, err := s.q(ctx).UpdateCompanyTaxProfile(ctx, store.UpdateCompanyTaxProfileParams{
			ID: id, Version: int32(in.Version), Npwp: nullable(in.NPWP), Nitku: nullable(in.NITKU),
			Address: in.Address, Pkp: in.PKP,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Conflict("conflict.version", "Profil PT baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if err != nil {
			return err
		}
		out = toCompany(r)
		_, err = audit.Write(ctx, db.Conn(ctx, s.pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &id, Action: "core.company.tax_profile",
			ObjectType: "company", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"npwp": before.NPWP, "nitku": before.NITKU, "pkp": before.PKP},
			After:  map[string]any{"npwp": out.NPWP, "nitku": out.NITKU, "pkp": out.PKP},
		})
		return err
	})
	return out, err
}
