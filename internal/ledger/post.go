package ledger

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

const (
	maxLines      = 500
	maxMemo       = 500
	maxLineMemo   = 250
	maxVoidReason = 300
)

// Seri nomor per jenis jurnal, mis. JP/2026/10/0001 (GL-07, GL-11).
const (
	seriesGeneral    = "JU"
	seriesAdjustment = "JP"
	seriesReversal   = "JB" // pembalik otomatis
	seriesVoid       = "JV" // pembalik karena void
)

// entry adalah draf ditambah detail yang hanya diisi mesin buku besar sendiri.
type entry struct {
	EntryDraft
	series     string
	reversalOf *uuid.UUID
}

// Post memvalidasi dan memposting satu jurnal umum atau penyesuaian. Jurnal penyesuaian
// bertanda AutoReverse langsung dibalik dengan jurnal bertanggal 1 bulan berikutnya.
func (l *ledger) Post(ctx context.Context, d EntryDraft) (Posted, error) {
	if d.Kind != General && d.Kind != Adjustment {
		return Posted{}, httpx.BadRequest("journal.kind", "Jenis jurnal harus umum atau penyesuaian.")
	}
	if d.SourceType == "" {
		d.SourceType = SourceManual
	}
	if d.SourceType == SourceVoid || d.SourceType == SourceAutoReversal {
		return Posted{}, fmt.Errorf("sumber %q hanya boleh dipakai mesin buku besar", d.SourceType)
	}
	if d.AutoReverse && d.Kind != Adjustment {
		return Posted{}, httpx.Unprocessable("journal.auto_reverse", "Balik otomatis hanya untuk jurnal penyesuaian.")
	}

	var out Posted
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		series := seriesGeneral
		if d.Kind == Adjustment {
			series = seriesAdjustment
		}
		ref, err := l.post(ctx, entry{EntryDraft: d, series: series})
		if err != nil {
			return err
		}
		out.Ref = ref
		if !d.AutoReverse {
			return nil
		}
		date := d.Date.MonthEnd().AddDays(1)
		rev, err := l.post(ctx, entry{
			EntryDraft: EntryDraft{
				CompanyID: d.CompanyID, Kind: Reversal, Date: date, Memo: "Balik otomatis " + ref.Number,
				SourceType: SourceAutoReversal, Lines: swap(d.Lines), Actor: d.Actor,
			},
			series: seriesReversal, reversalOf: &ref.ID,
		})
		if err != nil {
			var p *httpx.Problem
			if errors.As(err, &p) {
				p.Detail = fmt.Sprintf("Jurnal pembalik otomatis tanggal %s tidak bisa diposting. %s", date.Indonesian(), p.Detail)
			}
			return err
		}
		out.AutoReversal = &rev
		return nil
	})
	return out, err
}

// check menjalankan semua pemeriksaan jurnal sebelum ditulis (isi, akun, proyek, dimensi, dan
// periode) dan mengembalikan totalnya. Dipakai posting baru maupun revisi.
func (l *ledger) check(ctx context.Context, q *store.Queries, e entry) (money.Amount, error) {
	total, err := validate(e.EntryDraft)
	if err != nil {
		return money.Amount{}, err
	}
	if err := checkAccounts(ctx, q, e.CompanyID, e.Lines, e.Kind == Closing); err != nil {
		return money.Amount{}, err
	}
	if err := l.checkProjects(ctx, e); err != nil {
		return money.Amount{}, err
	}
	if err := checkDimensions(ctx, q, e); err != nil {
		return money.Amount{}, err
	}
	if err := checkPeriod(ctx, q, e.CompanyID, e.Date.MonthStart(), e.Kind); err != nil {
		return money.Amount{}, err
	}
	return total, nil
}

// insertLines menulis baris jurnal; trigger basis data memastikan akun detail, saldo agregat,
// dan jurnal seimbang saat commit.
func insertLines(ctx context.Context, q *store.Queries, companyID, entryID uuid.UUID, date civil.Date, lines []LineDraft) error {
	rows := make([]store.InsertLinesParams, len(lines))
	for i, ln := range lines {
		rows[i] = store.InsertLinesParams{
			ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, EntryID: entryID, EntryDate: date,
			LineNo: int16(i + 1), AccountID: ln.AccountID, Debit: ln.Debit, Credit: ln.Credit,
			Memo: strings.TrimSpace(ln.Memo), ProjectID: ln.ProjectID, BranchID: ln.BranchID, DepartmentID: ln.DepartmentID,
		}
	}
	_, err := q.InsertLines(ctx, rows)
	return err
}

// post menulis satu jurnal lengkap. Harus berjalan di dalam transaksi (lihat db.Run).
func (l *ledger) post(ctx context.Context, e entry) (Ref, error) {
	q := l.q(ctx)
	total, err := l.check(ctx, q, e)
	if err != nil {
		return Ref{}, err
	}

	ref := Ref{ID: uuid.Must(uuid.NewV7()), Number: e.Number}
	if ref.Number == "" {
		if ref.Number, err = nextNumber(ctx, q, e.CompanyID, e.series, e.Date, nil); err != nil {
			return Ref{}, err
		}
	}
	if err := q.InsertEntry(ctx, store.InsertEntryParams{
		ID: ref.ID, CompanyID: e.CompanyID, Number: ref.Number, Kind: string(e.Kind), EntryDate: e.Date,
		Memo: strings.TrimSpace(e.Memo), SourceType: e.SourceType, SourceID: e.SourceID,
		ReversalOf: e.reversalOf, AutoReverse: e.AutoReverse, Total: total, PostedBy: e.Actor.UserRef(),
	}); err != nil {
		return Ref{}, err
	}
	if err := insertLines(ctx, q, e.CompanyID, ref.ID, e.Date, e.Lines); err != nil {
		return Ref{}, err
	}

	_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
		UserID: userRef(e.Actor), CompanyID: &e.CompanyID, Action: "ledger.journal.post",
		ObjectType: "journal_entry", ObjectID: ref.ID.String(), IP: e.Actor.IP,
		After: map[string]any{
			"number": ref.Number, "kind": e.Kind, "date": e.Date.String(),
			"total": total.String(), "source": e.SourceType, "lines": len(e.Lines),
		},
	})
	return ref, err
}

// validate memeriksa isi jurnal tanpa menyentuh basis data dan mengembalikan totalnya.
func validate(d EntryDraft) (money.Amount, error) {
	fields := map[string]string{}
	switch {
	case d.Date.IsZero():
		fields["date"] = "Tanggal wajib diisi."
	case d.Date.Year() < 2000 || d.Date.Year() > 2100:
		fields["date"] = "Tanggal di luar rentang yang wajar."
	}
	if utf8.RuneCountInString(d.Memo) > maxMemo {
		fields["memo"] = fmt.Sprintf("Keterangan maksimal %d karakter.", maxMemo)
	}
	if len(d.Lines) < 2 {
		return money.Amount{}, httpx.Unprocessable("journal.too_few_lines", "Jurnal minimal punya dua baris: satu debit dan satu kredit.")
	}
	// Jurnal penutup dibuat mesin: satu baris per akun laba rugi, berapa pun jumlahnya.
	if len(d.Lines) > maxLines && d.Kind != Closing {
		return money.Amount{}, httpx.Unprocessable("journal.too_many_lines", fmt.Sprintf("Jurnal maksimal %d baris.", maxLines))
	}

	var debit, credit money.Amount
	for i, ln := range d.Lines {
		key := fmt.Sprintf("lines.%d.", i)
		if ln.AccountID == uuid.Nil {
			fields[key+"accountId"] = "Pilih akun."
		}
		switch {
		case ln.Debit.Sign() < 0 || ln.Credit.Sign() < 0:
			fields[key+"amount"] = "Nominal tidak boleh negatif."
		case ln.Debit.IsZero() == ln.Credit.IsZero():
			fields[key+"amount"] = "Isi debit atau kredit, salah satu saja."
		}
		if utf8.RuneCountInString(ln.Memo) > maxLineMemo {
			fields[key+"memo"] = fmt.Sprintf("Keterangan baris maksimal %d karakter.", maxLineMemo)
		}
		debit, credit = debit.Add(ln.Debit), credit.Add(ln.Credit)
	}
	if len(fields) > 0 {
		return money.Amount{}, httpx.Unprocessable("journal.invalid", firstMessage(fields)).WithFields(fields)
	}
	if !debit.Equal(credit) {
		return money.Amount{}, httpx.Unprocessable("ledger.unbalanced", fmt.Sprintf(
			"Jurnal tidak seimbang: total debit %s, kredit %s, selisih %s.",
			debit.IDR(), credit.IDR(), debit.Sub(credit).Abs().IDR()))
	}
	return debit, nil
}

// checkAccounts memastikan setiap akun ada di PT ini, akun detail, dan aktif.
// checkAccounts memastikan akun ada, akun detail, dan aktif. Jurnal penutup boleh memakai akun
// nonaktif, karena saldo akun yang sudah dinonaktifkan pun harus ikut direklas.
func checkAccounts(ctx context.Context, q *store.Queries, companyID uuid.UUID, lines []LineDraft, allowInactive bool) error {
	ids := make([]uuid.UUID, 0, len(lines))
	seen := map[uuid.UUID]bool{}
	for _, ln := range lines {
		if !seen[ln.AccountID] {
			seen[ln.AccountID] = true
			ids = append(ids, ln.AccountID)
		}
	}
	rows, err := q.AccountsByIDs(ctx, store.AccountsByIDsParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return err
	}
	byID := make(map[uuid.UUID]store.LedgerAccount, len(rows))
	for _, a := range rows {
		byID[a.ID] = a
	}
	fields := map[string]string{}
	for i, ln := range lines {
		a, ok := byID[ln.AccountID]
		key := fmt.Sprintf("lines.%d.accountId", i)
		switch {
		case !ok:
			fields[key] = "Akun tidak ditemukan di PT ini."
		case !a.Postable:
			fields[key] = fmt.Sprintf("%s %s adalah akun header; pilih akun detail di bawahnya.", a.Code, a.Name)
		case !a.Active && !allowInactive:
			fields[key] = fmt.Sprintf("Akun %s %s nonaktif.", a.Code, a.Name)
		}
	}
	if len(fields) > 0 {
		return httpx.Unprocessable("journal.account", firstMessage(fields)).WithFields(fields)
	}
	return nil
}

// checkProjects menjalankan pemeriksaan proyek untuk jurnal baru. Jurnal pembalik (void dan
// balik otomatis) tidak diperiksa: pembatalan harus selalu bisa, apa pun status proyeknya.
func (l *ledger) checkProjects(ctx context.Context, e entry) error {
	if l.projectCheck == nil || e.reversalOf != nil {
		return nil
	}
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, ln := range e.Lines {
		if ln.ProjectID != nil && !seen[*ln.ProjectID] {
			seen[*ln.ProjectID] = true
			ids = append(ids, *ln.ProjectID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return l.projectCheck(ctx, e.CompanyID, ids)
}

// checkPeriod menolak posting ke periode yang sudah ditutup. Baris periode dikunci FOR SHARE,
// sehingga periode tidak bisa ditutup selagi posting ini belum selesai.
func checkPeriod(ctx context.Context, q *store.Queries, companyID uuid.UUID, month civil.Date, kind Kind) error {
	if err := q.EnsurePeriod(ctx, store.EnsurePeriodParams{CompanyID: companyID, Month: month}); err != nil {
		return err
	}
	status, err := q.LockPeriodShared(ctx, store.LockPeriodSharedParams{CompanyID: companyID, Month: month})
	if err != nil {
		return err
	}
	switch {
	case status == "open":
		return nil
	case status == "adjusting" && (kind == Adjustment || kind == Closing):
		return nil
	case status == "adjusting":
		return httpx.Unprocessable("period.adjusting", fmt.Sprintf(
			"Periode %s sudah ditutup untuk transaksi; hanya jurnal penyesuaian yang boleh masuk.", month.MonthLabel()))
	default:
		return httpx.Unprocessable("period.locked", fmt.Sprintf(
			"Periode %s sudah dikunci. Minta Manager membukanya bila perlu koreksi.", month.MonthLabel()))
	}
}

// Void membatalkan jurnal manual dengan jurnal pembalik bertanggal void; jurnal aslinya tidak
// pernah diubah selain statusnya. Pembalik otomatis milik jurnal itu ikut dibatalkan.
func (l *ledger) Void(ctx context.Context, companyID, entryID uuid.UUID, date civil.Date, reason string, actor Actor) (Voided, error) {
	return l.void(ctx, companyID, entryID, date, reason, actor, func(ctx context.Context, q *store.Queries, e store.LedgerJournalEntry) error {
		switch e.SourceType {
		case SourceManual, SourceOpening:
			return nil
		case SourceAutoReversal:
			origin := "jurnal penyesuaiannya"
			if e.ReversalOf != nil {
				if o, err := q.GetEntry(ctx, store.GetEntryParams{CompanyID: companyID, ID: *e.ReversalOf}); err == nil {
					origin = o.Number
				}
			}
			return httpx.Unprocessable("journal.void_auto_reversal", fmt.Sprintf(
				"%s dibuat otomatis dari %s. Void %s; jurnal pembalik ini ikut dibatalkan.", e.Number, origin, origin))
		case SourceVoid:
			return httpx.Unprocessable("journal.void_reversal", fmt.Sprintf("%s adalah jurnal pembatalan dan tidak bisa di-void.", e.Number))
		default:
			return httpx.Unprocessable("journal.void_via_source", fmt.Sprintf(
				"%s berasal dari dokumen %s; batalkan lewat dokumen itu.", e.Number, e.SourceType))
		}
	})
}

// VoidSource dipakai modul untuk membatalkan jurnal dokumennya sendiri, mis. faktur yang di-void.
func (l *ledger) VoidSource(ctx context.Context, companyID, entryID uuid.UUID, sourceType string, date civil.Date, reason string, actor Actor) (Voided, error) {
	return l.void(ctx, companyID, entryID, date, reason, actor, func(_ context.Context, _ *store.Queries, e store.LedgerJournalEntry) error {
		if e.SourceType != sourceType {
			return fmt.Errorf("jurnal %s bersumber %s, bukan %s", e.Number, e.SourceType, sourceType)
		}
		return nil
	})
}

func (l *ledger) void(ctx context.Context, companyID, entryID uuid.UUID, date civil.Date, reason string, actor Actor,
	allowed func(context.Context, *store.Queries, store.LedgerJournalEntry) error) (Voided, error) {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 5 || n > maxVoidReason {
		msg := fmt.Sprintf("Tulis alasan void (5–%d karakter).", maxVoidReason)
		return Voided{}, httpx.Unprocessable("journal.void_reason", msg).WithFields(map[string]string{"reason": msg})
	}
	if date.IsZero() {
		date = l.today()
	}

	var out Voided
	err := db.Run(ctx, l.pool, func(ctx context.Context) error {
		q := l.q(ctx)
		e, err := q.GetEntryForUpdate(ctx, store.GetEntryForUpdateParams{CompanyID: companyID, ID: entryID})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("journal.not_found", "Jurnal tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if e.Status == "void" {
			return httpx.Conflict("journal.already_void", fmt.Sprintf("Jurnal %s sudah di-void.", e.Number))
		}
		if err := allowed(ctx, q, e); err != nil {
			return err
		}
		if date.Before(e.EntryDate) {
			msg := fmt.Sprintf("Tanggal void tidak boleh sebelum tanggal jurnal (%s).", e.EntryDate.Indonesian())
			return httpx.Unprocessable("journal.void_date", msg).WithFields(map[string]string{"date": msg})
		}

		rev, err := l.reverse(ctx, q, e, date, "Void "+e.Number+": "+reason, actor)
		if err != nil {
			return err
		}
		out.Reversal = rev
		if err := markVoid(ctx, q, e, reason, actor); err != nil {
			return err
		}

		// Pembalik otomatis jurnal penyesuaian ikut dibatalkan, pada tanggal yang tidak lebih
		// awal dari tanggalnya sendiri, supaya saldo setiap bulan kembali seperti semula.
		children, err := q.ReversalsOf(ctx, store.ReversalsOfParams{CompanyID: companyID, ReversalOf: &e.ID})
		if err != nil {
			return err
		}
		for _, c := range children {
			if c.SourceType != SourceAutoReversal || c.Status != "posted" {
				continue
			}
			child, err := q.GetEntryForUpdate(ctx, store.GetEntryForUpdateParams{CompanyID: companyID, ID: c.ID})
			if err != nil {
				return err
			}
			d := date
			if d.Before(child.EntryDate) {
				d = child.EntryDate
			}
			if _, err := l.reverse(ctx, q, child, d, "Void "+child.Number+" bersama "+e.Number, actor); err != nil {
				return err
			}
			if err := markVoid(ctx, q, child, "Ikut dibatalkan bersama "+e.Number+": "+reason, actor); err != nil {
				return err
			}
			out.AlsoVoids = append(out.AlsoVoids, Ref{ID: child.ID, Number: child.Number})
		}

		also := make([]string, len(out.AlsoVoids))
		for i, r := range out.AlsoVoids {
			also[i] = r.Number
		}
		_, err = audit.Write(ctx, db.Conn(ctx, l.pool), audit.Entry{
			UserID: userRef(actor), CompanyID: &companyID, Action: "ledger.journal.void",
			ObjectType: "journal_entry", ObjectID: e.ID.String(), IP: actor.IP,
			Before: map[string]any{"number": e.Number, "status": "posted"},
			After:  map[string]any{"number": e.Number, "status": "void", "reason": reason, "reversal": rev.Number, "alsoVoided": also},
		})
		return err
	})
	return out, err
}

// reverse memposting jurnal pembalik e: semua baris sama dengan debit dan kredit ditukar.
// Jenisnya mengikuti jurnal asli, supaya di neraca lajur keduanya saling meniadakan di kolom yang sama.
func (l *ledger) reverse(ctx context.Context, q *store.Queries, e store.LedgerJournalEntry, date civil.Date, memo string, actor Actor) (Ref, error) {
	lines, err := q.EntryLines(ctx, store.EntryLinesParams{CompanyID: e.CompanyID, EntryID: e.ID})
	if err != nil {
		return Ref{}, err
	}
	drafts := make([]LineDraft, len(lines))
	for i, ln := range lines {
		drafts[i] = LineDraft{AccountID: ln.AccountID, Debit: ln.Credit, Credit: ln.Debit, Memo: ln.Memo, ProjectID: ln.ProjectID,
			BranchID: ln.BranchID, DepartmentID: ln.DepartmentID}
	}
	if utf8.RuneCountInString(memo) > maxMemo {
		memo = string([]rune(memo)[:maxMemo])
	}
	return l.post(ctx, entry{
		EntryDraft: EntryDraft{
			CompanyID: e.CompanyID, Kind: Kind(e.Kind), Date: date, Memo: memo,
			SourceType: SourceVoid, Lines: drafts, Actor: actor,
		},
		series: seriesVoid, reversalOf: &e.ID,
	})
}

func markVoid(ctx context.Context, q *store.Queries, e store.LedgerJournalEntry, reason string, actor Actor) error {
	n, err := q.VoidEntry(ctx, store.VoidEntryParams{CompanyID: e.CompanyID, ID: e.ID, VoidedBy: userRef(actor), VoidReason: &reason})
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("jurnal %s gagal ditandai void", e.Number)
	}
	return nil
}

func swap(lines []LineDraft) []LineDraft {
	out := make([]LineDraft, len(lines))
	for i, ln := range lines {
		out[i] = LineDraft{AccountID: ln.AccountID, Debit: ln.Credit, Credit: ln.Debit, Memo: ln.Memo, ProjectID: ln.ProjectID,
			BranchID: ln.BranchID, DepartmentID: ln.DepartmentID}
	}
	return out
}

func userRef(a Actor) *uuid.UUID { return a.UserRef() }

var seriesPattern = regexp.MustCompile(`^[A-Z]{2,4}$`)

// firstMessage mengambil pesan field pertama (urut kunci) sebagai ringkasan problem.
func firstMessage(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fieldOrder(keys[i]) < fieldOrder(keys[j]) })
	return fields[keys[0]]
}

// fieldOrder mengurutkan "date" dan "memo" lebih dulu, lalu baris sesuai nomornya.
func fieldOrder(k string) string {
	var i int
	var rest string
	if _, err := fmt.Sscanf(k, "lines.%d.%s", &i, &rest); err == nil {
		return fmt.Sprintf("z%05d.%s", i, rest)
	}
	return k
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// checkDimensions memastikan cabang dan departemen di baris jurnal ada di PT ini dan aktif.
// Jurnal pembalik tidak diperiksa keaktifannya: pembatalan harus selalu bisa.
func checkDimensions(ctx context.Context, q *store.Queries, e entry) error {
	var branches, departments []uuid.UUID
	for _, ln := range e.Lines {
		if ln.BranchID != nil {
			branches = append(branches, *ln.BranchID)
		}
		if ln.DepartmentID != nil {
			departments = append(departments, *ln.DepartmentID)
		}
	}
	if len(branches) == 0 && len(departments) == 0 {
		return nil
	}
	rows, err := q.Dimensions(ctx, store.DimensionsParams{CompanyID: e.CompanyID, BranchIds: branches, DepartmentIds: departments})
	if err != nil {
		return err
	}
	type dim struct {
		code   string
		active bool
	}
	found := map[uuid.UUID]dim{}
	for _, r := range rows {
		found[r.ID] = dim{r.Code, r.Active}
	}
	fields := map[string]string{}
	check := func(i int, id *uuid.UUID, field, label string) {
		if id == nil {
			return
		}
		d, ok := found[*id]
		switch {
		case !ok:
			fields[fmt.Sprintf("lines.%d.%s", i, field)] = label + " tidak ditemukan di PT ini."
		case !d.active && e.reversalOf == nil:
			fields[fmt.Sprintf("lines.%d.%s", i, field)] = fmt.Sprintf("%s %s nonaktif.", label, d.code)
		}
	}
	for i, ln := range e.Lines {
		check(i, ln.BranchID, "branchId", "Cabang")
		check(i, ln.DepartmentID, "departmentId", "Departemen")
	}
	if len(fields) > 0 {
		return httpx.Unprocessable("journal.dimension", firstMessage(fields)).WithFields(fields)
	}
	return nil
}
