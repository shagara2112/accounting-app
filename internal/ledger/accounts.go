package ledger

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
)

// Accounts mengembalikan bagan akun urut kode, lengkap dengan saldo per hari ini:
// akun neraca sejak awal, akun laba rugi sejak awal tahun buku berjalan.
func (l *ledger) Accounts(ctx context.Context, companyID uuid.UUID) ([]Account, error) {
	q := l.q(ctx)
	rows, err := q.ListAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	startMonth, err := l.fiscalStartMonth(ctx, companyID)
	if err != nil {
		return nil, err
	}
	today := l.today()
	nets, err := q.AccountNetBalances(ctx, store.AccountNetBalancesParams{
		CompanyID: companyID, AsOfMonth: today.MonthStart(), FiscalStart: fiscalStart(startMonth, today),
	})
	if err != nil {
		return nil, err
	}

	out := make([]Account, len(rows))
	index := make(map[uuid.UUID]int, len(rows))
	for i, r := range rows {
		out[i] = toAccount(r)
		index[r.ID] = i
	}
	for _, n := range nets {
		if i, ok := index[n.AccountID]; ok {
			out[i].Balance = ClassOf(out[i].Class).Present(n.Net)
		}
	}
	// Saldo akun header = jumlah saldo anaknya, dihitung dari level terdalam ke atas.
	for level := MaxLevel; level > 1; level-- {
		for i := range out {
			if out[i].Level == level && out[i].ParentID != nil {
				p := index[*out[i].ParentID]
				out[p].Balance = out[p].Balance.Add(out[i].Balance)
			}
		}
	}
	return out, nil
}

func (l *ledger) AccountByCode(ctx context.Context, companyID uuid.UUID, code string) (Account, error) {
	r, err := l.q(ctx).GetAccountByCode(ctx, store.GetAccountByCodeParams{CompanyID: companyID, Code: code})
	if isNoRows(err) {
		return Account{}, httpx.NotFound("account.not_found", fmt.Sprintf("Akun %s tidak ada.", code))
	}
	if err != nil {
		return Account{}, err
	}
	return toAccount(r), nil
}

// CreateAccount menambah akun. Angka kepala kode harus sama dengan kelas induknya; akun
// berkode satu angka adalah akun kelompok (level 1) dan selalu akun header.
func (l *ledger) CreateAccount(ctx context.Context, companyID uuid.UUID, a NewAccount, actor Actor) (Account, error) {
	a.Code = strings.TrimSpace(a.Code)
	a.Name = strings.TrimSpace(a.Name)
	fields := map[string]string{}
	class := classOfCode(a.Code)
	if class == 0 {
		fields["code"] = "Format kode: angka kepala 1–9, lalu tanda hubung dan angka, mis. 1-1101."
	}
	if n := utf8.RuneCountInString(a.Name); n < 1 || n > 120 {
		fields["name"] = "Nama akun wajib diisi (maksimal 120 karakter)."
	}
	if len(fields) > 0 {
		return Account{}, httpx.Unprocessable("account.invalid", firstMessage(fields)).WithFields(fields)
	}

	var out Account
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		if _, err := q.GetAccountByCode(ctx, store.GetAccountByCodeParams{CompanyID: companyID, Code: a.Code}); err == nil {
			msg := fmt.Sprintf("Kode %s sudah dipakai.", a.Code)
			return httpx.Conflict("account.code_taken", msg).WithFields(map[string]string{"code": msg})
		} else if !isNoRows(err) {
			return err
		}

		level := 1
		if len(a.Code) == 1 {
			if a.ParentID != nil || a.Postable {
				msg := "Akun kelompok (kode satu angka) tidak punya induk dan selalu akun header."
				return httpx.Unprocessable("account.invalid", msg).WithFields(map[string]string{"code": msg})
			}
		} else {
			if a.ParentID == nil {
				msg := "Pilih akun induk."
				return httpx.Unprocessable("account.invalid", msg).WithFields(map[string]string{"parentId": msg})
			}
			parent, err := q.GetAccount(ctx, store.GetAccountParams{CompanyID: companyID, ID: *a.ParentID})
			if isNoRows(err) {
				msg := "Akun induk tidak ditemukan di PT ini."
				return httpx.Unprocessable("account.invalid", msg).WithFields(map[string]string{"parentId": msg})
			}
			if err != nil {
				return err
			}
			var msg, field = "", "parentId"
			switch {
			case parent.Postable:
				msg = fmt.Sprintf("%s %s adalah akun detail; induk harus akun header.", parent.Code, parent.Name)
			case !parent.Active:
				msg = fmt.Sprintf("Akun induk %s %s nonaktif.", parent.Code, parent.Name)
			case int(parent.Class) != class:
				field = "code"
				msg = fmt.Sprintf("Kode %s berkepala %d (%s), tidak cocok dengan induk %s (%s).",
					a.Code, class, ClassOf(class).Name, parent.Code, ClassOf(int(parent.Class)).Name)
			case int(parent.Level) >= MaxLevel:
				msg = fmt.Sprintf("Bagan akun maksimal %d tingkat.", MaxLevel)
			}
			if msg != "" {
				return httpx.Unprocessable("account.invalid", msg).WithFields(map[string]string{field: msg})
			}
			level = int(parent.Level) + 1
		}

		row, err := q.InsertAccount(ctx, store.InsertAccountParams{
			ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, Code: a.Code, Name: a.Name, Class: int16(class),
			ParentID: a.ParentID, Level: int16(level), Postable: a.Postable, CreatedBy: userRef(actor),
		})
		if err != nil {
			return err
		}
		out = toAccount(row)
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.account.create",
			ObjectType: "account", ObjectID: row.ID.String(), IP: actor.IP,
			After: map[string]any{"code": row.Code, "name": row.Name, "postable": row.Postable},
		})
		return err
	})
	return out, err
}

// UpdateAccount mengganti nama atau status aktif. Akun yang masih bersaldo, atau akun header
// yang masih punya anak aktif, tidak bisa dinonaktifkan.
func (l *ledger) UpdateAccount(ctx context.Context, companyID, accountID uuid.UUID, c AccountChange, actor Actor) (Account, error) {
	c.Name = strings.TrimSpace(c.Name)
	if n := utf8.RuneCountInString(c.Name); n < 1 || n > 120 {
		msg := "Nama akun wajib diisi (maksimal 120 karakter)."
		return Account{}, httpx.Unprocessable("account.invalid", msg).WithFields(map[string]string{"name": msg})
	}
	var out Account
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		cur, err := q.GetAccount(ctx, store.GetAccountParams{CompanyID: companyID, ID: accountID})
		if isNoRows(err) {
			return httpx.NotFound("account.not_found", "Akun tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if cur.Active && !c.Active {
			if cur.Postable {
				net, err := q.AccountNetAllTime(ctx, store.AccountNetAllTimeParams{CompanyID: companyID, AccountID: accountID})
				if err != nil {
					return err
				}
				if !net.IsZero() {
					return httpx.Unprocessable("account.has_balance", fmt.Sprintf(
						"Saldo %s %s masih %s; akun bersaldo tidak bisa dinonaktifkan.",
						cur.Code, cur.Name, ClassOf(int(cur.Class)).Present(net).IDR()))
				}
			} else {
				n, err := q.CountActiveChildren(ctx, store.CountActiveChildrenParams{CompanyID: companyID, ParentID: &accountID})
				if err != nil {
					return err
				}
				if n > 0 {
					return httpx.Unprocessable("account.has_active_children", fmt.Sprintf(
						"%s %s masih punya %d akun aktif di bawahnya.", cur.Code, cur.Name, n))
				}
			}
		}
		if !cur.Active && c.Active && cur.ParentID != nil {
			parent, err := q.GetAccount(ctx, store.GetAccountParams{CompanyID: companyID, ID: *cur.ParentID})
			if err != nil {
				return err
			}
			if !parent.Active {
				return httpx.Unprocessable("account.parent_inactive", fmt.Sprintf(
					"Aktifkan dulu akun induknya, %s %s.", parent.Code, parent.Name))
			}
		}

		row, err := q.UpdateAccount(ctx, store.UpdateAccountParams{
			CompanyID: companyID, ID: accountID, Name: c.Name, Active: c.Active, Version: int32(c.Version),
		})
		if isNoRows(err) {
			return httpx.Conflict("conflict.version", "Akun ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
		}
		if err != nil {
			return err
		}
		out = toAccount(row)
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.account.update",
			ObjectType: "account", ObjectID: row.ID.String(), IP: actor.IP,
			Before: map[string]any{"code": cur.Code, "name": cur.Name, "active": cur.Active},
			After:  map[string]any{"code": row.Code, "name": row.Name, "active": row.Active},
		})
		return err
	})
	return out, err
}

// ApplyTemplate mengisi bagan akun PT yang masih kosong dari template industri.
func (l *ledger) ApplyTemplate(ctx context.Context, companyID uuid.UUID, template string, actor Actor) (int, error) {
	accounts, err := Template(template)
	if err != nil {
		return 0, httpx.BadRequest("coa.template", "Template bagan akun tidak dikenal.")
	}
	err = db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		n, err := q.CountAccounts(ctx, companyID)
		if err != nil {
			return err
		}
		if n > 0 {
			return httpx.Conflict("coa.not_empty", "Bagan akun PT ini sudah terisi; template hanya untuk PT yang belum punya akun.")
		}
		ids := map[string]uuid.UUID{}
		for _, t := range accounts {
			id := uuid.Must(uuid.NewV7())
			var parent *uuid.UUID
			if t.Parent != "" {
				p := ids[t.Parent]
				parent = &p
			}
			if _, err := q.InsertAccount(ctx, store.InsertAccountParams{
				ID: id, CompanyID: companyID, Code: t.Code, Name: t.Name, Class: int16(t.Code[0] - '0'),
				ParentID: parent, Level: int16(t.Level), Postable: !t.Header, CreatedBy: userRef(actor),
			}); err != nil {
				return err
			}
			ids[t.Code] = id
		}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.coa.template",
			ObjectType: "company", ObjectID: companyID.String(), IP: actor.IP,
			After: map[string]any{"template": template, "accounts": len(accounts)},
		})
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(accounts), nil
}

func toAccount(r store.LedgerAccount) Account {
	return Account{
		ID: r.ID, Code: r.Code, Name: r.Name, Class: int(r.Class), ParentID: r.ParentID,
		Level: int(r.Level), Postable: r.Postable, Active: r.Active, Version: int(r.Version),
	}
}

// CheckAccount memastikan akun ada di PT ini, akun detail yang aktif, dan kelasnya termasuk
// classes. Dipakai modul lain untuk memvalidasi akun default, mis. akun dokumen cerminan.
func (l *ledger) CheckAccount(ctx context.Context, companyID, accountID uuid.UUID, classes []int) error {
	r, err := l.q(ctx).GetAccount(ctx, store.GetAccountParams{CompanyID: companyID, ID: accountID})
	if isNoRows(err) {
		return httpx.Unprocessable("ledger.account", "Akun tidak ditemukan di PT ini.")
	}
	if err != nil {
		return err
	}
	if !r.Postable || !r.Active {
		return httpx.Unprocessable("ledger.account", fmt.Sprintf("%s %s bukan akun detail yang aktif.", r.Code, r.Name))
	}
	for _, c := range classes {
		if int(r.Class) == c {
			return nil
		}
	}
	return httpx.Unprocessable("ledger.account", fmt.Sprintf("%s %s tidak bisa dipakai di sini; pilih akun berkepala %s.", r.Code, r.Name, classList(classes)))
}

func classList(classes []int) string {
	parts := make([]string, len(classes))
	for i, c := range classes {
		parts[i] = fmt.Sprint(c)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " atau " + parts[len(parts)-1]
}
