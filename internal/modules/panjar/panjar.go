// Package panjar adalah pengajuan panjar unit (revisi klien): kepala unit mengajukan kebutuhan
// unitnya (departemen) sebelum membeli, lalu finance menyetujui (boleh dengan nominal lebih kecil)
// atau menolak. Lajur hanya mencatat pengajuan dan keputusannya; uangnya tetap dibayar dan
// dicatat finance seperti biasa di Kas dan bank atau Pembelian, lalu pengajuannya ditandai
// sudah dibayar dengan nomor bukti pembayaran itu. Unit yang dikelola setiap kepala unit
// ditetapkan Manager atau Pemilik.
package panjar

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger"
	"lajur/internal/modules/budget"
	"lajur/internal/modules/core"
	"lajur/internal/modules/panjar/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/rbac"
)

const (
	Submitted = "diajukan"
	Approved  = "disetujui"
	Rejected  = "ditolak"
	Cancelled = "dibatalkan"
	Paid      = "dibayar"

	maxLines = 50
)

type Line struct {
	LineNo      int          `json:"lineNo"`
	Description string       `json:"description"`
	AccountID   *uuid.UUID   `json:"accountId,omitempty"`
	AccountCode string       `json:"accountCode,omitempty"`
	AccountName string       `json:"accountName,omitempty"`
	Amount      money.Amount `json:"amount"`
}

type Request struct {
	ID             uuid.UUID     `json:"id"`
	Number         string        `json:"number"`
	DepartmentID   uuid.UUID     `json:"departmentId"`
	DepartmentCode string        `json:"departmentCode"`
	DepartmentName string        `json:"departmentName"`
	RequestDate    civil.Date    `json:"requestDate"`
	NeededDate     *civil.Date   `json:"neededDate,omitempty"`
	Purpose        string        `json:"purpose"`
	Total          money.Amount  `json:"total"`
	Status         string        `json:"status"`
	ApprovedAmount *money.Amount `json:"approvedAmount,omitempty"`
	DecidedAt      *time.Time    `json:"decidedAt,omitempty"`
	DecidedByName  string        `json:"decidedByName,omitempty"`
	DecisionNote   string        `json:"decisionNote,omitempty"`
	PaidAt         *civil.Date   `json:"paidAt,omitempty"`
	PaidRef        string        `json:"paidRef,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
	CreatedBy      uuid.UUID     `json:"createdBy"`
	CreatedByName  string        `json:"createdByName"`
	Mine           bool          `json:"mine"` // diajukan oleh pembaca
	Version        int           `json:"version"`
}

type Detail struct {
	Request
	Lines []Line `json:"lines"`
}

type Input struct {
	DepartmentID uuid.UUID   `json:"departmentId"`
	NeededDate   *civil.Date `json:"neededDate,omitempty"`
	Purpose      string      `json:"purpose"`
	Lines        []LineInput `json:"lines"`
	Version      int         `json:"version"` // untuk ubah
}

type LineInput struct {
	Description string       `json:"description"`
	AccountID   *uuid.UUID   `json:"accountId,omitempty"` // akun perkiraan, opsional
	Amount      money.Amount `json:"amount"`
}

// Decision adalah keputusan finance. Amount kosong = disetujui sebesar pengajuan.
type Decision struct {
	Approve bool          `json:"approve"`
	Amount  *money.Amount `json:"amount,omitempty"`
	Note    string        `json:"note"`
	Version int           `json:"version"`
}

// Payment menandai pengajuan yang disetujui sudah dibayar, dengan rujukan bukti pembayarannya
// (mis. nomor kas keluar atau tagihan pembelian).
type Payment struct {
	Date    civil.Date `json:"date"`
	Ref     string     `json:"ref"`
	Version int        `json:"version"`
}

// Viewer adalah pengguna request. All = peninjau (izin lihat semua atau memutuskan): melihat semua
// pengajuan dan boleh mengajukan untuk unit mana pun. Selain itu (kepala unit) hanya unit yang
// ditetapkan untuknya: mengajukan untuk unit itu, melihat pengajuan unit itu dan pengajuannya
// sendiri, serta membaca anggaran unit itu.
type Viewer struct {
	UserID uuid.UUID
	All    bool
}

type Filter struct {
	Status       string
	DepartmentID *uuid.UUID
	From, To     civil.Date
}

type List struct {
	Items   []Request `json:"items"`
	Pending int       `json:"pending"` // menunggu keputusan di seluruh PT (hanya untuk peninjau)
	// Restricted: pembaca dibatasi ke Units, unit yang ditetapkan untuknya.
	Restricted bool        `json:"restricted"`
	Units      []uuid.UUID `json:"units,omitempty"`
}

// UnitHead adalah pengguna yang dibatasi ke unit tertentu (peran tanpa izin lihat semua dan tanpa
// izin memutuskan, mis. Kepala unit) beserta unit yang dikelolanya.
type UnitHead struct {
	UserID        uuid.UUID   `json:"userId"`
	Name          string      `json:"name"`
	Email         string      `json:"email"`
	Active        bool        `json:"active"`
	RoleName      string      `json:"roleName"`
	DepartmentIDs []uuid.UUID `json:"departmentIds"`
}

// UnitBudget adalah posisi anggaran beban satu departemen di tahun buku berjalan, sebagai bahan
// pertimbangan saat mengajukan dan memutuskan panjar.
type UnitBudget struct {
	DepartmentID uuid.UUID    `json:"departmentId"`
	FiscalYear   int          `json:"fiscalYear"`
	To           string       `json:"to"`        // realisasi s.d. bulan ini (yyyy-mm)
	HasBudget    bool         `json:"hasBudget"` // unit punya anggaran beban di versi aktif
	YearBudget   money.Amount `json:"yearBudget"`
	Actual       money.Amount `json:"actual"`    // realisasi beban departemen sejak awal tahun buku
	Committed    money.Amount `json:"committed"` // panjar disetujui yang belum dibayar
	Remaining    money.Amount `json:"remaining"` // anggaran − realisasi − panjar disetujui
}

type Service interface {
	Requests(ctx context.Context, companyID uuid.UUID, v Viewer, f Filter) (List, error)
	Request(ctx context.Context, companyID, id uuid.UUID, v Viewer) (Detail, error)
	Create(ctx context.Context, companyID uuid.UUID, in Input, v Viewer, actor audit.Actor) (Detail, error)
	Update(ctx context.Context, companyID, id uuid.UUID, in Input, v Viewer, actor audit.Actor) (Detail, error)
	Cancel(ctx context.Context, companyID, id uuid.UUID, version int, actor audit.Actor) (Detail, error)
	Decide(ctx context.Context, companyID, id uuid.UUID, in Decision, actor audit.Actor) (Detail, error)
	MarkPaid(ctx context.Context, companyID, id uuid.UUID, in Payment, actor audit.Actor) (Detail, error)
	UnitBudget(ctx context.Context, companyID, departmentID uuid.UUID, v Viewer, today civil.Date) (UnitBudget, error)
	UnitHeads(ctx context.Context, companyID uuid.UUID) ([]UnitHead, error)
	// SetUnits mengganti unit yang dikelola seorang kepala unit (Manager atau Pemilik).
	SetUnits(ctx context.Context, companyID, userID uuid.UUID, departmentIDs []uuid.UUID, actor audit.Actor) (UnitHead, error)
}

type Deps struct {
	Pool   *pgxpool.Pool
	Ledger ledger.Service
	Core   core.Service
	Budget budget.Service
	// Today: tanggal hari ini (WIB); tes boleh menggantinya.
	Today func() civil.Date
}

func New(d Deps) Service {
	if d.Today == nil {
		d.Today = func() civil.Date { return civil.Today(civil.Jakarta) }
	}
	return &service{d}
}

type service struct{ Deps }

func (s *service) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, s.Pool)) }

func notFound() error {
	return httpx.NotFound("panjar.not_found", "Pengajuan panjar tidak ditemukan.")
}

func stale() error {
	return httpx.Conflict("conflict.version", "Pengajuan ini baru saja diubah orang lain. Muat ulang lalu coba lagi.")
}

func invalid(field, msg string) error {
	return httpx.Unprocessable("panjar.invalid", msg).WithFields(map[string]string{field: msg})
}

// wrongStatus menjelaskan kenapa aksi tidak bisa dilakukan pada status pengajuan sekarang.
func wrongStatus(status, action string) error {
	label := map[string]string{
		Submitted: "masih menunggu keputusan", Approved: "sudah disetujui", Rejected: "sudah ditolak",
		Cancelled: "sudah dibatalkan", Paid: "sudah dibayar",
	}[status]
	return httpx.Conflict("panjar.status", fmt.Sprintf("Pengajuan ini %s, sehingga tidak bisa %s.", label, action))
}

// units mengembalikan unit yang dikelola pembaca terbatas; nil untuk peninjau (tidak dibatasi).
func (s *service) units(ctx context.Context, companyID uuid.UUID, v Viewer) ([]uuid.UUID, error) {
	if v.All {
		return nil, nil
	}
	ids, err := s.q(ctx).UserUnits(ctx, store.UserUnitsParams{CompanyID: companyID, UserID: v.UserID})
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return ids, err
}

func notAssigned(field string) error {
	msg := "Anda belum ditetapkan sebagai kepala unit ini. Minta Manager atau Pemilik menetapkannya di Panjar unit → Atur kepala unit."
	return httpx.Forbidden("panjar.unit_not_assigned", msg).WithFields(map[string]string{field: msg})
}

func toRequest(r store.GetRequestRow, v Viewer) Request {
	out := Request{
		ID: r.ID, Number: r.Number, DepartmentID: r.DepartmentID, DepartmentCode: r.DepartmentCode, DepartmentName: r.DepartmentName,
		RequestDate: r.RequestDate, NeededDate: r.NeededDate, Purpose: r.Purpose, Total: r.Total, Status: r.Status,
		ApprovedAmount: r.ApprovedAmount, DecidedAt: r.DecidedAt, DecisionNote: r.DecisionNote, PaidAt: r.PaidAt, PaidRef: r.PaidRef,
		CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy, CreatedByName: r.CreatedByName, Mine: r.CreatedBy == v.UserID, Version: int(r.Version),
	}
	if r.DecidedByName != nil {
		out.DecidedByName = *r.DecidedByName
	}
	return out
}

func (s *service) Requests(ctx context.Context, companyID uuid.UUID, v Viewer, f Filter) (List, error) {
	if f.Status != "" && f.Status != Submitted && f.Status != Approved && f.Status != Rejected && f.Status != Cancelled && f.Status != Paid {
		return List{}, httpx.BadRequest("request.query", "Parameter status tidak valid.")
	}
	q := s.q(ctx)
	units, err := s.units(ctx, companyID, v)
	if err != nil {
		return List{}, err
	}
	p := store.ListRequestsParams{CompanyID: companyID, FromDate: f.From, ToDate: f.To, DepartmentID: f.DepartmentID, Units: units}
	if f.Status != "" {
		p.Status = &f.Status
	}
	if !v.All {
		p.Viewer = &v.UserID
	}
	rows, err := q.ListRequests(ctx, p)
	if err != nil {
		return List{}, err
	}
	out := List{Items: make([]Request, len(rows)), Restricted: !v.All, Units: units}
	for i, r := range rows {
		out.Items[i] = toRequest(store.GetRequestRow{
			ID: r.ID, CompanyID: r.CompanyID, Number: r.Number, DepartmentID: r.DepartmentID, RequestDate: r.RequestDate,
			NeededDate: r.NeededDate, Purpose: r.Purpose, Total: r.Total, Status: r.Status, ApprovedAmount: r.ApprovedAmount,
			DecidedAt: r.DecidedAt, DecidedBy: r.DecidedBy, DecisionNote: r.DecisionNote, PaidAt: r.PaidAt, PaidRef: r.PaidRef,
			CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy, Version: r.Version, DepartmentCode: r.DepartmentCode,
			DepartmentName: r.DepartmentName, CreatedByName: r.CreatedByName,
		}, v)
	}
	if v.All {
		n, err := q.CountPending(ctx, companyID)
		if err != nil {
			return List{}, err
		}
		out.Pending = int(n)
	}
	return out, nil
}

func (s *service) Request(ctx context.Context, companyID, id uuid.UUID, v Viewer) (Detail, error) {
	q := s.q(ctx)
	r, err := q.GetRequest(ctx, store.GetRequestParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, notFound()
	}
	if err != nil {
		return Detail{}, err
	}
	if !v.All && r.CreatedBy != v.UserID {
		units, err := s.units(ctx, companyID, v)
		if err != nil {
			return Detail{}, err
		}
		if !slices.Contains(units, r.DepartmentID) {
			return Detail{}, notFound() // pengajuan unit lain tidak terlihat
		}
	}
	rows, err := q.RequestLines(ctx, store.RequestLinesParams{CompanyID: companyID, RequestID: id})
	if err != nil {
		return Detail{}, err
	}
	out := Detail{Request: toRequest(r, v), Lines: make([]Line, len(rows))}
	for i, l := range rows {
		out.Lines[i] = Line{LineNo: int(l.LineNo), Description: l.Description, AccountID: l.AccountID, Amount: l.Amount}
		if l.AccountCode != nil {
			out.Lines[i].AccountCode, out.Lines[i].AccountName = *l.AccountCode, *l.AccountName
		}
	}
	return out, nil
}

// check memeriksa isian pengajuan dan mengembalikan totalnya. Kepala unit hanya boleh memakai unit
// yang ditetapkan untuknya.
func (s *service) check(ctx context.Context, companyID uuid.UUID, in *Input, v Viewer, today civil.Date) (money.Amount, error) {
	in.Purpose = strings.TrimSpace(in.Purpose)
	if n := utf8.RuneCountInString(in.Purpose); n < 1 || n > 300 {
		return money.Amount{}, invalid("purpose", "Keperluan wajib diisi (maksimal 300 karakter).")
	}
	if in.DepartmentID == uuid.Nil {
		return money.Amount{}, invalid("departmentId", "Pilih unit (departemen) yang mengajukan.")
	}
	if err := s.Core.CheckDimensions(ctx, companyID, nil, &in.DepartmentID); err != nil {
		return money.Amount{}, err
	}
	units, err := s.units(ctx, companyID, v)
	if err != nil {
		return money.Amount{}, err
	}
	if units != nil && !slices.Contains(units, in.DepartmentID) {
		return money.Amount{}, notAssigned("departmentId")
	}
	if in.NeededDate != nil && in.NeededDate.Before(today) {
		return money.Amount{}, invalid("neededDate", "Tanggal dibutuhkan tidak boleh sebelum hari ini.")
	}
	if len(in.Lines) == 0 || len(in.Lines) > maxLines {
		return money.Amount{}, invalid("lines", fmt.Sprintf("Isi 1–%d rincian kebutuhan.", maxLines))
	}
	var accounts map[uuid.UUID]ledger.Account
	total := money.Amount{}
	for i := range in.Lines {
		l := &in.Lines[i]
		key := fmt.Sprintf("lines.%d", i)
		l.Description = strings.TrimSpace(l.Description)
		if n := utf8.RuneCountInString(l.Description); n < 1 || n > 200 {
			return money.Amount{}, invalid(key+".description", fmt.Sprintf("Rincian baris %d wajib diisi (maksimal 200 karakter).", i+1))
		}
		if l.Amount.Sign() <= 0 {
			return money.Amount{}, invalid(key+".amount", fmt.Sprintf("Nominal baris %d harus lebih dari nol.", i+1))
		}
		if l.AccountID != nil {
			if accounts == nil {
				list, err := s.Ledger.Accounts(ctx, companyID)
				if err != nil {
					return money.Amount{}, err
				}
				accounts = make(map[uuid.UUID]ledger.Account, len(list))
				for _, a := range list {
					accounts[a.ID] = a
				}
			}
			a, ok := accounts[*l.AccountID]
			if !ok || !a.Postable || !a.Active {
				return money.Amount{}, invalid(key+".accountId", fmt.Sprintf("Akun baris %d harus akun detail yang aktif.", i+1))
			}
		}
		total = total.Add(l.Amount)
	}
	return total, nil
}

func (s *service) insertLines(ctx context.Context, q *store.Queries, companyID, id uuid.UUID, lines []LineInput) error {
	for i, l := range lines {
		if err := q.InsertLine(ctx, store.InsertLineParams{
			ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, RequestID: id, LineNo: int16(i + 1),
			Description: l.Description, AccountID: l.AccountID, Amount: l.Amount,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) detail(ctx context.Context, companyID, id uuid.UUID, actor audit.Actor) (Detail, error) {
	return s.Request(ctx, companyID, id, Viewer{UserID: actor.UserID, All: true})
}

// Create mencatat pengajuan baru bertanggal hari ini dengan nomor seri PNJ.
func (s *service) Create(ctx context.Context, companyID uuid.UUID, in Input, v Viewer, actor audit.Actor) (Detail, error) {
	today := s.Today()
	total, err := s.check(ctx, companyID, &in, v, today)
	if err != nil {
		return Detail{}, err
	}
	var out Detail
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		number, err := s.Ledger.NextNumber(ctx, companyID, "PNJ", today)
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7())
		if err := q.InsertRequest(ctx, store.InsertRequestParams{
			ID: id, CompanyID: companyID, Number: number, DepartmentID: in.DepartmentID, RequestDate: today,
			NeededDate: in.NeededDate, Purpose: in.Purpose, Total: total, CreatedBy: actor.UserID,
		}); err != nil {
			return err
		}
		if err := s.insertLines(ctx, q, companyID, id, in.Lines); err != nil {
			return err
		}
		if out, err = s.detail(ctx, companyID, id, actor); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "panjar.create", ObjectType: "panjar", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": number, "departmentId": in.DepartmentID, "purpose": in.Purpose, "total": total.String()},
		})
		return err
	})
	return out, err
}

// lock mengambil pengajuan untuk diubah dan memastikan versinya masih sama.
func (s *service) lock(ctx context.Context, q *store.Queries, companyID, id uuid.UUID, version int) (store.PanjarRequest, error) {
	cur, err := q.GetRequestForUpdate(ctx, store.GetRequestForUpdateParams{CompanyID: companyID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return cur, notFound()
	}
	if err != nil {
		return cur, err
	}
	if int(cur.Version) != version {
		return cur, stale()
	}
	return cur, nil
}

// Update mengubah pengajuan yang masih menunggu keputusan; hanya oleh pengajunya.
func (s *service) Update(ctx context.Context, companyID, id uuid.UUID, in Input, v Viewer, actor audit.Actor) (Detail, error) {
	total, err := s.check(ctx, companyID, &in, v, s.Today())
	if err != nil {
		return Detail{}, err
	}
	var out Detail
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := s.lock(ctx, q, companyID, id, in.Version)
		if err != nil {
			return err
		}
		if cur.CreatedBy != actor.UserID {
			return httpx.Forbidden("panjar.not_owner", "Hanya pengaju yang bisa mengubah pengajuan ini.")
		}
		if cur.Status != Submitted {
			return wrongStatus(cur.Status, "diubah")
		}
		n, err := q.UpdateRequest(ctx, store.UpdateRequestParams{
			CompanyID: companyID, ID: id, DepartmentID: in.DepartmentID, NeededDate: in.NeededDate, Purpose: in.Purpose,
			Total: total, Version: cur.Version,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return stale()
		}
		if err := q.DeleteLines(ctx, store.DeleteLinesParams{CompanyID: companyID, RequestID: id}); err != nil {
			return err
		}
		if err := s.insertLines(ctx, q, companyID, id, in.Lines); err != nil {
			return err
		}
		if out, err = s.detail(ctx, companyID, id, actor); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "panjar.update", ObjectType: "panjar", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"departmentId": cur.DepartmentID, "purpose": cur.Purpose, "total": cur.Total.String()},
			After:  map[string]any{"departmentId": in.DepartmentID, "purpose": in.Purpose, "total": total.String()},
		})
		return err
	})
	return out, err
}

// Cancel membatalkan pengajuan yang masih menunggu keputusan; hanya oleh pengajunya.
func (s *service) Cancel(ctx context.Context, companyID, id uuid.UUID, version int, actor audit.Actor) (Detail, error) {
	var out Detail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := s.lock(ctx, q, companyID, id, version)
		if err != nil {
			return err
		}
		if cur.CreatedBy != actor.UserID {
			return httpx.Forbidden("panjar.not_owner", "Hanya pengaju yang bisa membatalkan pengajuan ini.")
		}
		if cur.Status != Submitted {
			return wrongStatus(cur.Status, "dibatalkan")
		}
		if n, err := q.CancelRequest(ctx, store.CancelRequestParams{CompanyID: companyID, ID: id, Version: cur.Version}); err != nil {
			return err
		} else if n == 0 {
			return stale()
		}
		if out, err = s.detail(ctx, companyID, id, actor); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "panjar.cancel", ObjectType: "panjar", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"number": cur.Number, "status": cur.Status},
		})
		return err
	})
	return out, err
}

// Decide menyetujui atau menolak pengajuan. Pengaju tidak bisa memutuskan pengajuannya sendiri;
// penolakan wajib diberi alasan.
func (s *service) Decide(ctx context.Context, companyID, id uuid.UUID, in Decision, actor audit.Actor) (Detail, error) {
	in.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Note) > 300 {
		return Detail{}, invalid("note", "Catatan maksimal 300 karakter.")
	}
	if !in.Approve && in.Note == "" {
		return Detail{}, invalid("note", "Tulis alasan penolakan supaya pengaju tahu apa yang perlu diperbaiki.")
	}
	var out Detail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := s.lock(ctx, q, companyID, id, in.Version)
		if err != nil {
			return err
		}
		if cur.Status != Submitted {
			return wrongStatus(cur.Status, "diputuskan lagi")
		}
		if cur.CreatedBy == actor.UserID {
			return httpx.Forbidden("panjar.self_approval", "Pengajuan Anda sendiri harus diputuskan pengguna finance lain.")
		}
		p := store.DecideParams{CompanyID: companyID, ID: id, Status: Rejected, DecidedBy: actor.UserRef(), DecisionNote: in.Note, Version: cur.Version}
		if in.Approve {
			amount := cur.Total
			if in.Amount != nil {
				amount = *in.Amount
			}
			if amount.Sign() <= 0 || amount.Cmp(cur.Total) > 0 {
				return invalid("amount", "Nominal disetujui harus lebih dari nol dan tidak melebihi nominal pengajuan.")
			}
			p.Status, p.ApprovedAmount = Approved, &amount
		}
		if n, err := q.Decide(ctx, p); err != nil {
			return err
		} else if n == 0 {
			return stale()
		}
		if out, err = s.detail(ctx, companyID, id, actor); err != nil {
			return err
		}
		after := map[string]any{"number": cur.Number, "status": p.Status, "note": in.Note}
		if p.ApprovedAmount != nil {
			after["approvedAmount"] = p.ApprovedAmount.String()
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "panjar.decide", ObjectType: "panjar", ObjectID: id.String(), IP: actor.IP,
			Before: map[string]any{"status": cur.Status, "total": cur.Total.String()}, After: after,
		})
		return err
	})
	return out, err
}

// MarkPaid menandai pengajuan yang disetujui sudah dibayar. Pembayarannya sendiri dicatat di Kas
// dan bank atau Pembelian; di sini cukup tanggal dan nomor buktinya.
func (s *service) MarkPaid(ctx context.Context, companyID, id uuid.UUID, in Payment, actor audit.Actor) (Detail, error) {
	in.Ref = strings.TrimSpace(in.Ref)
	if n := utf8.RuneCountInString(in.Ref); n < 1 || n > 60 {
		return Detail{}, invalid("ref", "Isi nomor bukti pembayaran, mis. nomor kas keluar (maksimal 60 karakter).")
	}
	if in.Date.IsZero() {
		return Detail{}, invalid("date", "Isi tanggal pembayaran.")
	}
	if in.Date.After(s.Today()) {
		return Detail{}, invalid("date", "Tanggal pembayaran tidak boleh setelah hari ini.")
	}
	var out Detail
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := s.lock(ctx, q, companyID, id, in.Version)
		if err != nil {
			return err
		}
		if cur.Status != Approved {
			return wrongStatus(cur.Status, "ditandai dibayar")
		}
		if in.Date.Before(cur.RequestDate) {
			return invalid("date", "Tanggal pembayaran tidak boleh sebelum tanggal pengajuan.")
		}
		if n, err := q.MarkPaid(ctx, store.MarkPaidParams{CompanyID: companyID, ID: id, PaidAt: &in.Date, PaidRef: in.Ref, Version: cur.Version}); err != nil {
			return err
		} else if n == 0 {
			return stale()
		}
		if out, err = s.detail(ctx, companyID, id, actor); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "panjar.paid", ObjectType: "panjar", ObjectID: id.String(), IP: actor.IP,
			After: map[string]any{"number": cur.Number, "paidAt": in.Date.String(), "paidRef": in.Ref},
		})
		return err
	})
	return out, err
}

// UnitBudget membaca anggaran beban departemen di tahun buku berjalan (versi aktif), realisasinya
// sejak awal tahun buku s.d. bulan ini, dan panjar yang sudah disetujui tetapi belum dibayar.
func (s *service) UnitBudget(ctx context.Context, companyID, departmentID uuid.UUID, v Viewer, today civil.Date) (UnitBudget, error) {
	units, err := s.units(ctx, companyID, v)
	if err != nil {
		return UnitBudget{}, err
	}
	if units != nil && !slices.Contains(units, departmentID) {
		return UnitBudget{}, notAssigned("department")
	}
	if err := s.Core.CheckDimensions(ctx, companyID, nil, &departmentID); err != nil {
		var p *httpx.Problem
		if errors.As(err, &p) && p.Code == "dimension.inactive" {
			err = nil // unit nonaktif tetap boleh dilihat posisinya
		}
		if err != nil {
			return UnitBudget{}, err
		}
	}
	st, err := s.Ledger.Settings(ctx, companyID)
	if err != nil {
		return UnitBudget{}, err
	}
	start := st.FiscalYearStartMonth
	if start < 1 || start > 12 {
		start = 1
	}
	fy := today.Year()
	if int(today.Month()) < start {
		fy--
	}
	month := (int(today.Month())-start+12)%12 + 1
	c, err := s.Budget.Compare(ctx, companyID, budget.CompareFilter{
		FiscalYear: fy, FromMonth: 1, ToMonth: month, Slice: budget.Slice{DepartmentID: &departmentID},
	})
	if err != nil {
		return UnitBudget{}, err
	}
	committed, err := s.q(ctx).Committed(ctx, store.CommittedParams{CompanyID: companyID, DepartmentID: departmentID})
	if err != nil {
		return UnitBudget{}, err
	}
	out := UnitBudget{
		DepartmentID: departmentID, FiscalYear: fy, To: c.To, YearBudget: c.Expense.YearBudget, Actual: c.Expense.YearActual, Committed: committed,
	}
	// Versi anggaran ada tetapi unit ini tidak punya baris beban: dianggap belum beranggaran.
	out.HasBudget = c.Budget != nil && out.YearBudget.Sign() > 0
	out.Remaining = out.YearBudget.Sub(out.Actual).Sub(committed)
	return out, nil
}

func (s *service) UnitHeads(ctx context.Context, companyID uuid.UUID) ([]UnitHead, error) {
	rows, err := s.q(ctx).UnitHeads(ctx, store.UnitHeadsParams{
		CompanyID: companyID, CreatePerm: rbac.PanjarCreate, ViewPerm: rbac.PanjarView, ApprovePerm: rbac.PanjarApprove,
	})
	if err != nil {
		return nil, err
	}
	out := make([]UnitHead, len(rows))
	for i, r := range rows {
		out[i] = UnitHead{UserID: r.ID, Name: r.Name, Email: r.Email, Active: r.Active, RoleName: r.RoleName, DepartmentIDs: r.DepartmentIds}
	}
	return out, nil
}

// SetUnits mengganti unit yang dikelola seorang kepala unit. Hanya untuk pengguna yang memang
// dibatasi ke unit (peninjau dan finance tidak perlu ditetapkan); unit harus departemen PT ini.
func (s *service) SetUnits(ctx context.Context, companyID, userID uuid.UUID, departmentIDs []uuid.UUID, actor audit.Actor) (UnitHead, error) {
	ids := make([]uuid.UUID, 0, len(departmentIDs))
	for _, id := range departmentIDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	depts, err := s.Core.Dimensions(ctx, companyID, core.DimDepartment)
	if err != nil {
		return UnitHead{}, err
	}
	for _, id := range ids {
		if !slices.ContainsFunc(depts, func(d core.Dimension) bool { return d.ID == id }) {
			return UnitHead{}, invalid("departmentIds", "Unit (departemen) tidak ditemukan di PT ini.")
		}
	}
	var out UnitHead
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		q := s.q(ctx)
		perms, err := q.MemberPerms(ctx, store.MemberPermsParams{CompanyID: companyID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("panjar.not_unit_head", "Pengguna ini tidak punya akses ke PT ini.")
		}
		if err != nil {
			return err
		}
		if !slices.Contains(perms, rbac.PanjarCreate) || slices.Contains(perms, rbac.PanjarView) || slices.Contains(perms, rbac.PanjarApprove) {
			return httpx.Unprocessable("panjar.not_unit_head", "Unit hanya ditetapkan untuk pengguna berperan Kepala unit. Peran pengguna ini sudah bisa mengajukan untuk semua unit.")
		}
		before, err := q.UserUnits(ctx, store.UserUnitsParams{CompanyID: companyID, UserID: userID})
		if err != nil {
			return err
		}
		if err := q.ClearUnits(ctx, store.ClearUnitsParams{CompanyID: companyID, UserID: userID}); err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := q.AddUnits(ctx, store.AddUnitsParams{CompanyID: companyID, UserID: userID, DepartmentIds: ids, CreatedBy: actor.UserRef()}); err != nil {
				return err
			}
		}
		heads, err := s.UnitHeads(ctx, companyID)
		if err != nil {
			return err
		}
		for _, h := range heads {
			if h.UserID == userID {
				out = h
			}
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "panjar.units", ObjectType: "user", ObjectID: userID.String(), IP: actor.IP,
			Before: map[string]any{"departmentIds": before}, After: map[string]any{"departmentIds": out.DepartmentIDs},
		})
		return err
	})
	return out, err
}
