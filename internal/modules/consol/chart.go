package consol

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/modules/consol/internal/store"
	"lajur/internal/modules/core"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/rbac"
)

type CompanyRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	ShortName string    `json:"shortName"`
}

// Member adalah akun PT yang masuk ke satu akun grup.
type Member struct {
	CompanyID uuid.UUID `json:"companyId"`
	AccountID uuid.UUID `json:"accountId"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Mapped    bool      `json:"mapped"` // dipetakan manual, bukan karena kodenya sama
}

type GroupAccount struct {
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	ParentCode *string  `json:"parentCode,omitempty"`
	Postable   bool     `json:"postable"`
	Class      int      `json:"class"`
	Level      int      `json:"level"`
	Stored     bool     `json:"stored"`   // sudah tersimpan di bagan akun grup
	Conflict   bool     `json:"conflict"` // akun PT anggotanya bernama berbeda
	Members    []Member `json:"members"`
}

// GroupChart adalah bagan akun grup: akun yang tersimpan ditambah kode akun PT yang belum
// tersimpan (dibaca langsung dari bagan akun PT), beserta pemetaannya.
type GroupChart struct {
	Companies []CompanyRef   `json:"companies"`
	Accounts  []GroupAccount `json:"accounts"`
	Problems  []string       `json:"problems"`
}

// chartData adalah bagan akun grup lengkap dengan peta akun PT → kode grup.
type chartData struct {
	GroupChart
	byCode   map[string]*GroupAccount
	accounts map[uuid.UUID][]ledger.Account // per PT
	mapping  map[uuid.UUID]map[uuid.UUID]string
}

// visibleCompanies: PT yang bisa dilihat pengguna di konsolidasi (izin consol.view).
func (s *service) visibleCompanies(ctx context.Context, userID uuid.UUID) ([]core.Company, error) {
	all, err := s.Core.Companies(ctx)
	if err != nil {
		return nil, err
	}
	var out []core.Company
	for _, c := range all {
		if c.Kind != "pt" {
			continue
		}
		role, err := s.Core.Role(ctx, userID, c.ID)
		if err != nil {
			if err == rbac.ErrNoAccess {
				continue
			}
			return nil, err
		}
		if role.Perms.Has(rbac.ConsolView) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *service) chartFor(ctx context.Context, companies []core.Company) (chartData, error) {
	q := s.q(ctx)
	stored, err := q.GroupAccounts(ctx)
	if err != nil {
		return chartData{}, err
	}
	overrides, err := q.AccountMap(ctx)
	if err != nil {
		return chartData{}, err
	}
	d := chartData{
		byCode: map[string]*GroupAccount{}, accounts: map[uuid.UUID][]ledger.Account{}, mapping: map[uuid.UUID]map[uuid.UUID]string{},
	}
	d.Problems = []string{}
	for _, g := range stored {
		d.byCode[g.Code] = &GroupAccount{Code: g.Code, Name: g.Name, ParentCode: g.ParentCode, Postable: g.Postable, Class: int(g.Class), Stored: true}
	}
	manual := map[uuid.UUID]map[uuid.UUID]string{}
	for _, m := range overrides {
		if manual[m.CompanyID] == nil {
			manual[m.CompanyID] = map[uuid.UUID]string{}
		}
		manual[m.CompanyID][m.AccountID] = m.GroupCode
	}

	for _, c := range companies {
		d.Companies = append(d.Companies, CompanyRef{ID: c.ID, Name: c.Name, ShortName: c.ShortName})
		list, err := s.Ledger.Accounts(ctx, c.ID)
		if err != nil {
			return chartData{}, err
		}
		d.accounts[c.ID] = list
		byID := map[uuid.UUID]ledger.Account{}
		for _, a := range list {
			byID[a.ID] = a
		}
		// Akun PT yang kodenya belum ada di bagan grup menjadi akun grup sementara.
		for _, a := range list {
			if _, ok := d.byCode[a.Code]; ok {
				continue
			}
			g := &GroupAccount{Code: a.Code, Name: a.Name, Postable: a.Postable, Class: a.Class}
			if a.ParentID != nil {
				p := byID[*a.ParentID].Code
				g.ParentCode = &p
			}
			d.byCode[a.Code] = g
		}
		d.mapping[c.ID] = map[uuid.UUID]string{}
		for _, a := range list {
			if !a.Postable {
				continue
			}
			code, mapped := manual[c.ID][a.ID]
			if !mapped {
				code = a.Code
			}
			d.mapping[c.ID][a.ID] = code
			g := d.byCode[code]
			g.Members = append(g.Members, Member{CompanyID: c.ID, AccountID: a.ID, Code: a.Code, Name: a.Name, Mapped: mapped})
			if !g.Postable {
				d.Problems = append(d.Problems, fmt.Sprintf("%s: akun %s %s masuk ke akun grup %s %s yang berupa akun header; petakan ke akun grup detail.",
					c.ShortName, a.Code, a.Name, g.Code, g.Name))
			}
			if g.Class != a.Class {
				d.Problems = append(d.Problems, fmt.Sprintf("%s: akun %s %s (kelas %d) dipetakan ke akun grup %s berkelas %d.",
					c.ShortName, a.Code, a.Name, a.Class, g.Code, g.Class))
			}
		}
	}

	for _, g := range d.byCode {
		names := map[string]bool{}
		for _, m := range g.Members {
			names[strings.ToLower(strings.TrimSpace(m.Name))] = true
		}
		g.Conflict = len(names) > 1
		if g.Members == nil {
			g.Members = []Member{}
		}
	}
	for code, g := range d.byCode {
		g.Level = d.level(code)
		d.Accounts = append(d.Accounts, *g)
	}
	sort.Slice(d.Accounts, func(i, j int) bool { return d.Accounts[i].Code < d.Accounts[j].Code })
	if d.Companies == nil {
		d.Companies = []CompanyRef{}
	}
	return d, nil
}

func (d chartData) level(code string) int {
	n := 1
	for g := d.byCode[code]; g != nil && g.ParentCode != nil && n < 10; g = d.byCode[*g.ParentCode] {
		n++
	}
	return n
}

// ledgerChart mengubah bagan akun grup menjadi akun-akun sintetis dengan id dari kodenya,
// supaya penyusun laporan modul report bisa dipakai apa adanya.
func (d chartData) ledgerChart() []ledger.Account {
	out := make([]ledger.Account, 0, len(d.byCode))
	for code, g := range d.byCode {
		a := ledger.Account{ID: groupID(code), Code: code, Name: g.Name, Class: g.Class, Level: d.level(code), Postable: g.Postable, Active: true}
		if g.ParentCode != nil {
			p := groupID(*g.ParentCode)
			a.ParentID = &p
		}
		out = append(out, a)
	}
	return out
}

var groupNS = uuid.MustParse("5b2f8f3e-6a4c-4d8e-9a65-3f0e0a7c1b20")

func groupID(code string) uuid.UUID { return uuid.NewSHA1(groupNS, []byte(code)) }

func (s *service) GroupChart(ctx context.Context, userID uuid.UUID) (GroupChart, error) {
	companies, err := s.visibleCompanies(ctx, userID)
	if err != nil {
		return GroupChart{}, err
	}
	d, err := s.chartFor(ctx, companies)
	return d.GroupChart, err
}

type GroupAccountInput struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	ParentCode *string `json:"parentCode"`
}

var codePattern = regexp.MustCompile(`^[1-9](-[0-9]{1,8}(\.[0-9]{1,4}){0,2})?$`)

// SaveGroupAccount mengganti nama akun grup, atau menambah akun grup detail baru di bawah akun
// header, mis. untuk memisahkan "4-1001" yang di tiap PT berarti pendapatan berbeda.
func (s *service) SaveGroupAccount(ctx context.Context, in GroupAccountInput, actor audit.Actor) error {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	if !codePattern.MatchString(in.Code) {
		return field("code", "Kode akun grup harus berformat seperti bagan akun, mis. 4-1011.")
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 200 {
		return field("name", "Nama akun grup wajib diisi (maksimal 200 karakter).")
	}
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		all, err := s.Core.Companies(ctx)
		if err != nil {
			return err
		}
		var pts []core.Company
		for _, c := range all {
			if c.Kind == "pt" {
				pts = append(pts, c)
			}
		}
		d, err := s.chartFor(ctx, pts)
		if err != nil {
			return err
		}
		g, exists := d.byCode[in.Code]
		if !exists {
			if in.ParentCode == nil {
				return field("parentCode", "Pilih akun header induknya.")
			}
			parent, ok := d.byCode[*in.ParentCode]
			switch {
			case !ok:
				return field("parentCode", "Akun induk tidak ada di bagan akun grup.")
			case parent.Postable:
				return field("parentCode", fmt.Sprintf("%s %s adalah akun detail; pilih akun header.", parent.Code, parent.Name))
			case int(in.Code[0]-'0') != parent.Class:
				return field("code", fmt.Sprintf("Kode harus diawali %d, sama dengan kelas akun induknya.", parent.Class))
			}
			g = &GroupAccount{Code: in.Code, Postable: true, Class: parent.Class, ParentCode: in.ParentCode}
			d.byCode[in.Code] = g
		}
		if err := s.ensureStored(ctx, d, g.ParentCode, actor); err != nil {
			return err
		}
		parent := g.ParentCode
		if err := s.q(ctx).UpsertGroupAccount(ctx, store.UpsertGroupAccountParams{
			Code: g.Code, Name: in.Name, ParentCode: parent, Postable: g.Postable, Class: int16(g.Class), UpdatedBy: actor.UserRef(),
		}); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), Action: "consol.group_account.save", ObjectType: "group_account", ObjectID: g.Code, IP: actor.IP,
			Before: map[string]any{"name": g.Name, "new": !exists}, After: map[string]any{"name": in.Name},
		})
		return err
	})
}

// ensureStored menyimpan akun grup sementara beserta induk-induknya.
func (s *service) ensureStored(ctx context.Context, d chartData, code *string, actor audit.Actor) error {
	for code != nil {
		g := d.byCode[*code]
		if g == nil {
			return fmt.Errorf("akun grup %s tidak ada", *code)
		}
		if !g.Stored {
			if err := s.q(ctx).InsertGroupAccountIfMissing(ctx, store.InsertGroupAccountIfMissingParams{
				Code: g.Code, Name: g.Name, ParentCode: g.ParentCode, Postable: g.Postable, Class: int16(g.Class), UpdatedBy: actor.UserRef(),
			}); err != nil {
				return err
			}
			g.Stored = true
		}
		code = g.ParentCode
	}
	return nil
}

// SetMapping memetakan akun detail PT ke akun grup detail berkelas sama; kode kosong
// mengembalikannya ke akun grup berkode sama.
func (s *service) SetMapping(ctx context.Context, companyID, accountID uuid.UUID, groupCode string, actor audit.Actor) error {
	groupCode = strings.TrimSpace(groupCode)
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		company, err := s.Core.Company(ctx, companyID)
		if err != nil {
			return err
		}
		d, err := s.chartFor(ctx, []core.Company{company})
		if err != nil {
			return err
		}
		var account *ledger.Account
		for _, a := range d.accounts[companyID] {
			if a.ID == accountID {
				a := a
				account = &a
			}
		}
		if account == nil || !account.Postable {
			return field("accountId", "Pilih akun detail PT ini.")
		}
		q := s.q(ctx)
		if groupCode == "" || groupCode == account.Code {
			if err := q.DeleteAccountMap(ctx, store.DeleteAccountMapParams{CompanyID: companyID, AccountID: accountID}); err != nil {
				return err
			}
		} else {
			g, ok := d.byCode[groupCode]
			switch {
			case !ok:
				return field("groupCode", "Akun grup tidak ditemukan.")
			case !g.Postable:
				return field("groupCode", fmt.Sprintf("%s %s adalah akun header grup; pilih akun detail.", g.Code, g.Name))
			case g.Class != account.Class:
				return field("groupCode", fmt.Sprintf("%s %s berkelas %d, sedangkan akun PT berkelas %d.", g.Code, g.Name, g.Class, account.Class))
			}
			code := groupCode
			if err := s.ensureStored(ctx, d, &code, actor); err != nil {
				return err
			}
			if err := q.SetAccountMap(ctx, store.SetAccountMapParams{CompanyID: companyID, AccountID: accountID, GroupCode: groupCode, UpdatedBy: actor.UserRef()}); err != nil {
				return err
			}
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "consol.account_map.set", ObjectType: "account", ObjectID: accountID.String(), IP: actor.IP,
			After: map[string]any{"account": account.Code, "groupCode": groupCode},
		})
		return err
	})
}

func field(name, msg string) error {
	return httpx.Unprocessable("consol.invalid", msg).WithFields(map[string]string{name: msg})
}
