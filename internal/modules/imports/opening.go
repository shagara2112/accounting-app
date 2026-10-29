package imports

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"lajur/internal/ledger"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Halaman Saldo awal (GL-06): saldo per akun diketik di layar, hasilnya sama dengan impor neraca
// saldo awal (satu jurnal sumber "opening", seri SA). Piutang usaha dan utang usaha boleh diketik
// langsung (tanpa rincian faktur, revisi klien) atau diisi per dokumen lewat impor supaya umur
// piutang/utang ikut benar; persediaan tetap per barang lewat impor karena kartu stok dan HPP
// bergantung padanya. Selisihnya masuk akun ekuitas saldo awal, yang menjadi nol setelah semua
// saldo awal lengkap. Selama periodenya belum dikunci, angkanya bisa diubah
// (jurnal yang sama direvisi, riwayat versinya tersimpan).

type OpeningLine struct {
	AccountID uuid.UUID    `json:"accountId"`
	Debit     money.Amount `json:"debit"`
	Credit    money.Amount `json:"credit"`
}

// ControlledAccount adalah akun yang punya rincian per dokumen; Balance = saldonya per tanggal
// saldo awal (debit − kredit) dari dokumen saldo awal yang sudah diimpor. Manual: saldonya boleh
// juga diketik langsung di halaman Saldo awal (piutang dan utang usaha, tanpa rincian faktur).
type ControlledAccount struct {
	AccountID uuid.UUID    `json:"accountId"`
	Kind      string       `json:"kind"` // piutang-awal, utang-awal, atau stok-awal
	Reason    string       `json:"reason"`
	Balance   money.Amount `json:"balance"`
	Manual    bool         `json:"manual"`
}

type OpeningState struct {
	EntryID         *uuid.UUID          `json:"entryId,omitempty"`
	Number          string              `json:"number,omitempty"`
	Date            *civil.Date         `json:"date,omitempty"`
	Revision        int                 `json:"revision"`
	Lines           []OpeningLine       `json:"lines"`
	Controlled      []ControlledAccount `json:"controlled"`
	OpeningEquityID uuid.UUID           `json:"openingEquityId"`
	// OpeningEquity: saldo akun ekuitas saldo awal per tanggal saldo awal (debit − kredit);
	// nol berarti saldo awal sudah lengkap dan seimbang dengan dokumen piutang, utang, dan stok.
	OpeningEquity money.Amount `json:"openingEquity"`
}

type OpeningInput struct {
	Date     civil.Date    `json:"date"`
	Lines    []OpeningLine `json:"lines"`
	Revision int           `json:"revision"`
	Reason   string        `json:"reason"`
}

// controlledKinds memetakan akun yang diisi per dokumen (sama dengan controlledAccounts) ke
// jenis impor saldo awalnya.
func (s *service) controlledKinds(ctx context.Context, companyID uuid.UUID) (map[uuid.UUID]ControlledAccount, error) {
	out := map[uuid.UUID]ControlledAccount{}
	for _, x := range []struct {
		role         ledger.Role
		kind, reason string
	}{
		{ledger.Receivable, "piutang-awal", "Diketik langsung tanpa rincian faktur: tidak muncul di Umur piutang, dan pelunasannya dicatat lewat Kas dan bank. Supaya umur piutang benar, isi per faktur lewat impor."},
		{ledger.Payable, "utang-awal", "Diketik langsung tanpa rincian tagihan: tidak muncul di Umur utang, dan pembayarannya dicatat lewat Kas dan bank. Supaya umur utang benar, isi per tagihan lewat impor."},
	} {
		a, err := s.Ledger.RoleAccount(ctx, companyID, x.role)
		if err != nil {
			return nil, err
		}
		out[a.ID] = ControlledAccount{AccountID: a.ID, Kind: x.kind, Reason: x.reason, Manual: true}
	}
	inv, err := s.Inventory.InventoryAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for id := range inv {
		out[id] = ControlledAccount{AccountID: id, Kind: "stok-awal", Reason: "Diisi per barang lewat impor Saldo awal stok."}
	}
	return out, nil
}

func (s *service) openingEntry(ctx context.Context, companyID uuid.UUID) (*ledger.EntryDetail, error) {
	list, err := s.Ledger.EntriesBySource(ctx, companyID, ledger.SourceOpening)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	e, err := s.Ledger.Entry(ctx, companyID, list[0].ID)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *service) balanceAt(ctx context.Context, companyID, accountID uuid.UUID, date civil.Date) (money.Amount, error) {
	l, err := s.Ledger.AccountLedger(ctx, companyID, accountID, date, date)
	if err != nil {
		return money.Amount{}, err
	}
	// Closing disajikan menurut saldo normal; kembalikan ke debit − kredit.
	if l.Normal == ledger.Credit {
		return l.Closing.Neg(), nil
	}
	return l.Closing, nil
}

// Opening membaca saldo awal yang sudah ada (bila ada) untuk halaman Saldo awal.
func (s *service) Opening(ctx context.Context, companyID uuid.UUID) (OpeningState, error) {
	opening, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OpeningEquity)
	if err != nil {
		return OpeningState{}, err
	}
	controlled, err := s.controlledKinds(ctx, companyID)
	if err != nil {
		return OpeningState{}, err
	}
	out := OpeningState{Lines: []OpeningLine{}, Controlled: []ControlledAccount{}, OpeningEquityID: opening.ID}
	e, err := s.openingEntry(ctx, companyID)
	if err != nil {
		return OpeningState{}, err
	}
	if e != nil {
		id, date := e.ID, e.Date
		out.EntryID, out.Number, out.Date, out.Revision = &id, e.Number, &date, e.Revision
		for _, l := range e.Lines {
			if l.AccountID == opening.ID {
				continue
			}
			out.Lines = append(out.Lines, OpeningLine{AccountID: l.AccountID, Debit: l.Debit, Credit: l.Credit})
		}
		for id, c := range controlled {
			if c.Balance, err = s.balanceAt(ctx, companyID, id, date); err != nil {
				return OpeningState{}, err
			}
			controlled[id] = c
		}
		if out.OpeningEquity, err = s.balanceAt(ctx, companyID, opening.ID, date); err != nil {
			return OpeningState{}, err
		}
	}
	for _, c := range controlled {
		out.Controlled = append(out.Controlled, c)
	}
	return out, nil
}

// SaveOpening memposting jurnal saldo awal, atau merevisinya bila sudah ada. Pindah bulan:
// jurnal lama di-void lalu jurnal baru diposting, karena nomor jurnal memuat bulannya.
func (s *service) SaveOpening(ctx context.Context, companyID uuid.UUID, in OpeningInput, actor audit.Actor) (OpeningState, error) {
	if in.Date.IsZero() {
		msg := "Isi tanggal saldo awal, biasanya akhir bulan sebelum mulai memakai Lajur."
		return OpeningState{}, httpx.Unprocessable("opening.invalid", msg).WithFields(map[string]string{"date": msg})
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(in.Reason) > 300 {
		msg := "Alasan perubahan maksimal 300 karakter."
		return OpeningState{}, httpx.Unprocessable("opening.invalid", msg).WithFields(map[string]string{"reason": msg})
	}
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		opening, err := s.Ledger.RoleAccount(ctx, companyID, ledger.OpeningEquity)
		if err != nil {
			return err
		}
		controlled, err := s.controlledKinds(ctx, companyID)
		if err != nil {
			return err
		}
		accounts, err := s.Ledger.Accounts(ctx, companyID)
		if err != nil {
			return err
		}
		byID := make(map[uuid.UUID]ledger.Account, len(accounts))
		for _, a := range accounts {
			byID[a.ID] = a
		}

		fields := map[string]string{}
		seen := map[uuid.UUID]bool{}
		var lines []ledger.LineDraft
		var net money.Amount
		for _, l := range in.Lines {
			if l.Debit.IsZero() && l.Credit.IsZero() {
				continue
			}
			key := "lines." + l.AccountID.String()
			a, ok := byID[l.AccountID]
			switch {
			case !ok:
				fields[key] = "Akun tidak ditemukan di PT ini."
			case !a.Postable || !a.Active:
				fields[key] = fmt.Sprintf("%s %s bukan akun detail yang aktif.", a.Code, a.Name)
			case a.ID == opening.ID:
				fields[key] = fmt.Sprintf("%s %s diisi otomatis dari selisih saldo awal.", a.Code, a.Name)
			case controlled[a.ID].Kind != "" && !controlled[a.ID].Manual:
				fields[key] = fmt.Sprintf("%s %s: %s", a.Code, a.Name, controlled[a.ID].Reason)
			case l.Debit.Sign() < 0 || l.Credit.Sign() < 0:
				fields[key] = "Debit dan kredit tidak boleh negatif."
			case l.Debit.Sign() > 0 && l.Credit.Sign() > 0:
				fields[key] = "Isi debit atau kredit, bukan keduanya."
			case seen[a.ID]:
				fields[key] = fmt.Sprintf("Akun %s diisi dua kali.", a.Code)
			}
			if _, bad := fields[key]; bad {
				continue
			}
			seen[a.ID] = true
			lines = append(lines, ledger.LineDraft{AccountID: a.ID, Debit: l.Debit, Credit: l.Credit, Memo: "Saldo awal"})
			net = net.Add(l.Debit).Sub(l.Credit)
		}
		if len(fields) > 0 {
			return httpx.Unprocessable("opening.invalid", fields[firstKey(fields)]).WithFields(fields)
		}
		if len(lines) == 0 {
			return httpx.Unprocessable("opening.empty", "Isi saldo minimal satu akun.")
		}
		if !net.IsZero() {
			l := ledger.LineDraft{AccountID: opening.ID, Memo: "Selisih saldo awal"}
			if net.Sign() > 0 {
				l.Credit = net
			} else {
				l.Debit = net.Neg()
			}
			lines = append(lines, l)
		}
		memo := "Saldo awal per " + in.Date.Indonesian()

		existing, err := s.openingEntry(ctx, companyID)
		if err != nil {
			return err
		}
		if existing != nil && existing.Date.MonthStart() == in.Date.MonthStart() {
			if existing.Revision != in.Revision {
				return httpx.Conflict("conflict.version", fmt.Sprintf("Saldo awal %s baru saja diubah orang lain. Muat ulang lalu coba lagi.", existing.Number))
			}
			reason := in.Reason
			if reason == "" {
				reason = "Diubah dari halaman Saldo awal"
			}
			_, err := s.Ledger.ReviseOpening(ctx, companyID, existing.ID, ledger.ReviseInput{
				Date: in.Date, Memo: memo, Lines: lines, Reason: reason, Revision: existing.Revision,
			}, actor)
			return err
		}
		if existing != nil {
			if existing.Revision != in.Revision {
				return httpx.Conflict("conflict.version", fmt.Sprintf("Saldo awal %s baru saja diubah orang lain. Muat ulang lalu coba lagi.", existing.Number))
			}
			if _, err := s.Ledger.Void(ctx, companyID, existing.ID, existing.Date,
				"Saldo awal dipindah ke "+in.Date.Indonesian(), actor); err != nil {
				return err
			}
		}
		number, err := s.Ledger.NextNumber(ctx, companyID, "SA", in.Date)
		if err != nil {
			return err
		}
		_, err = s.Ledger.Post(ctx, ledger.EntryDraft{
			CompanyID: companyID, Kind: ledger.General, Date: in.Date, Memo: memo,
			SourceType: ledger.SourceOpening, Number: number, Lines: lines, Actor: actor,
		})
		return err
	})
	if err != nil {
		return OpeningState{}, err
	}
	return s.Opening(ctx, companyID)
}

func firstKey(m map[string]string) string {
	first := ""
	for k := range m {
		if first == "" || k < first {
			first = k
		}
	}
	return first
}
