// Package ledger adalah mesin buku besar (TDD, bagian Mesin buku besar). Semua angka di
// Lajur lewat Post: memvalidasi, memberi nomor, menulis jurnal, dan memperbarui saldo dalam
// satu transaksi. Modul lain memanggil Post di transaksi request yang sedang berjalan.
package ledger

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/ledger/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/money"
)

// Kind adalah jenis jurnal; menentukan seri nomor dan kolom di neraca lajur.
type Kind string

const (
	General    Kind = "umum"
	Adjustment Kind = "penyesuaian"
	Reversal   Kind = "pembalik"
	Closing    Kind = "penutup"
)

// Source untuk jurnal yang dibuat mesin buku besar sendiri.
const (
	SourceManual       = "manual"        // diinput pengguna (jurnal umum dan penyesuaian)
	SourceVoid         = "void"          // pembalik karena void
	SourceAutoReversal = "auto_reversal" // pembalik otomatis jurnal penyesuaian
)

// Actor adalah pengguna yang melakukan aksi; dicatat di jurnal dan audit trail.
type Actor = audit.Actor

type LineDraft struct {
	AccountID uuid.UUID    `json:"accountId"`
	Debit     money.Amount `json:"debit"`
	Credit    money.Amount `json:"credit"`
	Memo      string       `json:"memo"`
}

// EntryDraft adalah jurnal yang akan diposting.
type EntryDraft struct {
	CompanyID   uuid.UUID
	Kind        Kind // umum atau penyesuaian
	Date        civil.Date
	Memo        string
	AutoReverse bool   // hanya penyesuaian: dibalik otomatis tanggal 1 bulan berikutnya (GL-08, GL-11)
	SourceType  string // "manual", atau jenis dokumen modul pemosting, mis. "sales.invoice"
	SourceID    *uuid.UUID
	Number      string // nomor dokumen sumber (mis. FJ/2026/10/0001); kosong = nomor seri jurnal
	Lines       []LineDraft
	Actor       Actor
}

// Ref menunjuk satu jurnal.
type Ref struct {
	ID     uuid.UUID `json:"id"`
	Number string    `json:"number"`
}

type Posted struct {
	Ref
	AutoReversal *Ref `json:"autoReversal,omitempty"`
}

type Voided struct {
	Reversal  Ref   `json:"reversal"`
	AlsoVoids []Ref `json:"alsoVoided,omitempty"` // jurnal pembalik otomatis yang ikut dibatalkan
}

type Account struct {
	ID       uuid.UUID    `json:"id"`
	Code     string       `json:"code"`
	Name     string       `json:"name"`
	Class    int          `json:"class"`
	ParentID *uuid.UUID   `json:"parentId,omitempty"`
	Level    int          `json:"level"`
	Postable bool         `json:"postable"`
	Active   bool         `json:"active"`
	Version  int          `json:"version"`
	Balance  money.Amount `json:"balance"` // menurut sisi saldo normal; akun header = jumlah anaknya
}

type NewAccount struct {
	Code     string     `json:"code"`
	Name     string     `json:"name"`
	ParentID *uuid.UUID `json:"parentId"`
	Postable bool       `json:"postable"`
}

type AccountChange struct {
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Version int    `json:"version"`
}

type Settings struct {
	FiscalYearStartMonth int  `json:"fiscalYearStartMonth"`
	HasEntries           bool `json:"hasEntries"`
}

type Period struct {
	Month       string     `json:"month"` // yyyy-mm
	Status      string     `json:"status"`
	ChangedAt   *time.Time `json:"changedAt,omitempty"`
	ChangedBy   *string    `json:"changedBy,omitempty"`
	Reason      *string    `json:"reason,omitempty"`
	Entries     int64      `json:"entries"`
	Adjustments int64      `json:"adjustments"`
}

type FiscalYear struct {
	Year       int      `json:"year"` // tahun kalender saat tahun buku dimulai
	StartMonth int      `json:"startMonth"`
	Periods    []Period `json:"periods"`
}

type EntrySummary struct {
	ID         uuid.UUID    `json:"id"`
	Number     string       `json:"number"`
	Kind       Kind         `json:"kind"`
	Date       civil.Date   `json:"date"`
	Memo       string       `json:"memo"`
	SourceType string       `json:"sourceType"`
	SourceID   *uuid.UUID   `json:"sourceId,omitempty"`
	Total      money.Amount `json:"total"`
	Status     string       `json:"status"`
	ReversalOf *uuid.UUID   `json:"reversalOf,omitempty"`
}

type EntryFilter struct {
	From, To civil.Date
	Kind     string
	Search   string
	Cursor   string
	Limit    int
}

type EntryPage struct {
	Items      []EntrySummary `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type EntryLine struct {
	LineNo      int          `json:"lineNo"`
	AccountID   uuid.UUID    `json:"accountId"`
	AccountCode string       `json:"accountCode"`
	AccountName string       `json:"accountName"`
	Debit       money.Amount `json:"debit"`
	Credit      money.Amount `json:"credit"`
	Memo        string       `json:"memo"`
}

type RelatedEntry struct {
	Ref
	SourceType string     `json:"sourceType"`
	Date       civil.Date `json:"date"`
	Status     string     `json:"status"`
}

type EntryDetail struct {
	EntrySummary
	AutoReverse   bool           `json:"autoReverse"`
	PostedAt      time.Time      `json:"postedAt"`
	PostedBy      *string        `json:"postedBy,omitempty"`
	VoidedAt      *time.Time     `json:"voidedAt,omitempty"`
	VoidedBy      *string        `json:"voidedBy,omitempty"`
	VoidReason    *string        `json:"voidReason,omitempty"`
	Lines         []EntryLine    `json:"lines"`
	ReversalOfRef *Ref           `json:"reversalOfRef,omitempty"`
	Reversals     []RelatedEntry `json:"reversals"`
}

// Pair adalah satu pasang kolom debit dan kredit di neraca lajur.
type Pair struct {
	Debit  money.Amount `json:"debit"`
	Credit money.Amount `json:"credit"`
}

type WorksheetRow struct {
	AccountID    *uuid.UUID `json:"accountId,omitempty"` // kosong untuk baris laba tahun lalu
	Code         string     `json:"code"`
	Name         string     `json:"name"`
	Class        int        `json:"class"`
	Unadjusted   Pair       `json:"unadjusted"`
	Adjustment   Pair       `json:"adjustment"`
	Adjusted     Pair       `json:"adjusted"`
	ProfitLoss   Pair       `json:"profitLoss"`
	BalanceSheet Pair       `json:"balanceSheet"`
}

type WorksheetTotals struct {
	Unadjusted   Pair `json:"unadjusted"`
	Adjustment   Pair `json:"adjustment"`
	Adjusted     Pair `json:"adjusted"`
	ProfitLoss   Pair `json:"profitLoss"`
	BalanceSheet Pair `json:"balanceSheet"`
}

// Worksheet adalah neraca lajur satu bulan (GL-12).
type Worksheet struct {
	Month       string          `json:"month"`
	FiscalStart civil.Date      `json:"fiscalStart"`
	MonthEnd    civil.Date      `json:"monthEnd"`
	Rows        []WorksheetRow  `json:"rows"`
	Totals      WorksheetTotals `json:"totals"`
	NetIncome   money.Amount    `json:"netIncome"` // positif = laba
	Final       WorksheetTotals `json:"final"`     // setelah baris laba (rugi) bersih
	Balanced    bool            `json:"balanced"`
}

type LedgerLine struct {
	EntryID uuid.UUID    `json:"entryId"`
	Number  string       `json:"number"`
	Kind    Kind         `json:"kind"`
	Status  string       `json:"status"`
	Date    civil.Date   `json:"date"`
	Memo    string       `json:"memo"`
	Debit   money.Amount `json:"debit"`
	Credit  money.Amount `json:"credit"`
	Balance money.Amount `json:"balance"` // saldo berjalan menurut sisi normal
}

// AccountLedger adalah buku besar satu akun untuk rentang tanggal (GL-04).
type AccountLedger struct {
	Account     Account      `json:"account"`
	Normal      Side         `json:"normal"`
	From        civil.Date   `json:"from"`
	To          civil.Date   `json:"to"`
	Opening     money.Amount `json:"opening"`
	Lines       []LedgerLine `json:"lines"`
	TotalDebit  money.Amount `json:"totalDebit"`
	TotalCredit money.Amount `json:"totalCredit"`
	Closing     money.Amount `json:"closing"`
	Truncated   bool         `json:"truncated"`
}

// Service adalah satu-satunya pintu buku besar bagi modul lain dan handler HTTP.
type Service interface {
	Post(ctx context.Context, d EntryDraft) (Posted, error)
	Void(ctx context.Context, companyID, entryID uuid.UUID, date civil.Date, reason string, actor Actor) (Voided, error)
	// VoidSource membatalkan jurnal milik dokumen modul; sourceType harus cocok dengan jurnalnya.
	VoidSource(ctx context.Context, companyID, entryID uuid.UUID, sourceType string, date civil.Date, reason string, actor Actor) (Voided, error)
	// NextNumber mengambil nomor dokumen tanpa celah, mis. FJ/2026/10/0001 (GL-07).
	NextNumber(ctx context.Context, companyID uuid.UUID, series string, date civil.Date) (string, error)
	// RoleAccount mengembalikan akun sistem, mis. piutang usaha untuk faktur penjualan.
	RoleAccount(ctx context.Context, companyID uuid.UUID, role Role) (Account, error)
	Entry(ctx context.Context, companyID, entryID uuid.UUID) (EntryDetail, error)
	Entries(ctx context.Context, companyID uuid.UUID, f EntryFilter) (EntryPage, error)

	Accounts(ctx context.Context, companyID uuid.UUID) ([]Account, error)
	AccountByCode(ctx context.Context, companyID uuid.UUID, code string) (Account, error)
	CreateAccount(ctx context.Context, companyID uuid.UUID, a NewAccount, actor Actor) (Account, error)
	UpdateAccount(ctx context.Context, companyID, accountID uuid.UUID, c AccountChange, actor Actor) (Account, error)
	ApplyTemplate(ctx context.Context, companyID uuid.UUID, template string, actor Actor) (int, error)

	Settings(ctx context.Context, companyID uuid.UUID) (Settings, error)
	SetFiscalYearStart(ctx context.Context, companyID uuid.UUID, month int, actor Actor) error
	Periods(ctx context.Context, companyID uuid.UUID, year int) (FiscalYear, error)
	SetPeriodStatus(ctx context.Context, companyID uuid.UUID, month civil.Date, status, reason string, actor Actor) error

	Worksheet(ctx context.Context, companyID uuid.UUID, month civil.Date) (Worksheet, error)
	AccountLedger(ctx context.Context, companyID, accountID uuid.UUID, from, to civil.Date) (AccountLedger, error)
	VerifyBalances(ctx context.Context, companyID uuid.UUID) (int64, error)
}

func New(pool *pgxpool.Pool) Service {
	return &ledger{pool: pool, today: func() civil.Date { return civil.Today(civil.Jakarta) }}
}

type ledger struct {
	pool  *pgxpool.Pool
	today func() civil.Date
}

func (l *ledger) q(ctx context.Context) *store.Queries { return store.New(db.Conn(ctx, l.pool)) }

// fiscalStart adalah tanggal 1 tahun buku yang memuat d.
func fiscalStart(startMonth int, d civil.Date) civil.Date {
	if int(d.Month()) >= startMonth {
		return civil.New(d.Year(), time.Month(startMonth), 1)
	}
	return civil.New(d.Year()-1, time.Month(startMonth), 1)
}

func (l *ledger) fiscalStartMonth(ctx context.Context, companyID uuid.UUID) (int, error) {
	m, err := l.q(ctx).GetSettings(ctx, companyID)
	if isNoRows(err) {
		return 1, nil
	}
	return int(m), err
}
