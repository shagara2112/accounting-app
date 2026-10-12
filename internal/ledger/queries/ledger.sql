-- Pengaturan dan periode -------------------------------------------------------

-- name: GetSettings :one
SELECT fiscal_year_start_month FROM ledger.settings WHERE company_id = $1;

-- name: UpsertSettings :exec
INSERT INTO ledger.settings (company_id, fiscal_year_start_month, updated_at, updated_by)
VALUES ($1, $2, now(), $3)
ON CONFLICT (company_id) DO UPDATE
SET fiscal_year_start_month = EXCLUDED.fiscal_year_start_month,
    updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: CountEntries :one
SELECT count(*) FROM ledger.journal_entries WHERE company_id = $1;

-- name: EnsurePeriod :exec
INSERT INTO ledger.periods (company_id, month) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: LockPeriodShared :one
-- Dipakai saat posting: periode tidak bisa ditutup selagi ada posting yang berjalan.
SELECT status FROM ledger.periods WHERE company_id = $1 AND month = $2 FOR SHARE;

-- name: LockPeriodForUpdate :one
SELECT status FROM ledger.periods WHERE company_id = $1 AND month = $2 FOR UPDATE;

-- name: SetPeriodStatus :exec
UPDATE ledger.periods
SET status = $3, changed_at = now(), changed_by = $4, reason = $5
WHERE company_id = $1 AND month = $2;

-- name: ListPeriods :many
SELECT p.month, p.status, p.changed_at, p.reason, u.name AS changed_by_name
FROM ledger.periods p
LEFT JOIN platform.users u ON u.id = p.changed_by
WHERE p.company_id = @company_id AND p.month >= @from_month AND p.month <= @to_month
ORDER BY p.month;

-- name: EntryCountsByMonth :many
SELECT date_trunc('month', entry_date)::date AS month, kind, count(*) AS entries
FROM ledger.journal_entries
WHERE company_id = @company_id AND entry_date >= @from_date AND entry_date < @to_date
GROUP BY 1, 2;

-- name: NextNumber :one
INSERT INTO ledger.sequences (company_id, series, month, last_no)
VALUES ($1, $2, $3, 1)
ON CONFLICT (company_id, series, month) DO UPDATE SET last_no = ledger.sequences.last_no + 1
RETURNING last_no;

-- Bagan akun --------------------------------------------------------------------

-- name: ListAccounts :many
SELECT * FROM ledger.accounts WHERE company_id = $1 ORDER BY code;

-- name: GetAccount :one
SELECT * FROM ledger.accounts WHERE company_id = $1 AND id = $2;

-- name: GetAccountByCode :one
SELECT * FROM ledger.accounts WHERE company_id = $1 AND code = $2;

-- name: CountAccounts :one
SELECT count(*) FROM ledger.accounts WHERE company_id = $1;

-- name: CountActiveChildren :one
SELECT count(*) FROM ledger.accounts WHERE company_id = $1 AND parent_id = $2 AND active;

-- name: AccountsByIDs :many
SELECT * FROM ledger.accounts WHERE company_id = @company_id AND id = ANY(@ids::uuid[]);

-- name: InsertAccount :one
INSERT INTO ledger.accounts (id, company_id, code, name, class, parent_id, level, postable, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateAccount :one
UPDATE ledger.accounts
SET name = @name, active = @active, version = version + 1
WHERE company_id = @company_id AND id = @id AND version = @version
RETURNING *;

-- name: AccountNetAllTime :one
SELECT coalesce(sum(debit - credit), 0)::numeric AS net
FROM ledger.balances WHERE company_id = $1 AND account_id = $2;

-- name: AccountNetBalances :many
-- Saldo (debit − kredit) per akun sampai @as_of_month: akun neraca sejak awal,
-- akun laba rugi sejak awal tahun buku.
SELECT b.account_id, coalesce(sum(b.debit - b.credit), 0)::numeric AS net
FROM ledger.balances b
JOIN ledger.accounts a ON a.id = b.account_id
WHERE b.company_id = @company_id AND b.month <= @as_of_month
  AND (a.class <= 3 OR b.month >= @fiscal_start)
GROUP BY b.account_id;

-- Jurnal ------------------------------------------------------------------------

-- name: InsertEntry :exec
INSERT INTO ledger.journal_entries
  (id, company_id, number, kind, entry_date, memo, source_type, source_id, reversal_of, auto_reverse, total, posted_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: InsertLines :copyfrom
INSERT INTO ledger.journal_lines (id, company_id, entry_id, entry_date, line_no, account_id, debit, credit, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetEntry :one
SELECT e.*, pu.name AS posted_by_name, vu.name AS voided_by_name
FROM ledger.journal_entries e
LEFT JOIN platform.users pu ON pu.id = e.posted_by
LEFT JOIN platform.users vu ON vu.id = e.voided_by
WHERE e.company_id = $1 AND e.id = $2;

-- name: GetEntryForUpdate :one
SELECT * FROM ledger.journal_entries WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: EntryLines :many
SELECT l.line_no, l.account_id, a.code AS account_code, a.name AS account_name, l.debit, l.credit, l.memo
FROM ledger.journal_lines l
JOIN ledger.accounts a ON a.id = l.account_id
WHERE l.company_id = $1 AND l.entry_id = $2
ORDER BY l.line_no;

-- name: ReversalsOf :many
SELECT id, number, source_type, entry_date, status
FROM ledger.journal_entries
WHERE company_id = $1 AND reversal_of = $2
ORDER BY posted_at, number;

-- name: VoidEntry :execrows
UPDATE ledger.journal_entries
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: ListEntries :many
SELECT id, number, kind, entry_date, memo, source_type, total, status, reversal_of
FROM ledger.journal_entries
WHERE company_id = @company_id
  AND entry_date >= @from_date AND entry_date <= @to_date
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(search)::text IS NULL
       OR number ILIKE '%' || sqlc.narg(search)::text || '%'
       OR memo ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(cursor_date)::date IS NULL
       OR (entry_date, id) < (sqlc.narg(cursor_date)::date, sqlc.narg(cursor_id)::uuid))
ORDER BY entry_date DESC, id DESC
LIMIT @row_limit;

-- Laporan -----------------------------------------------------------------------

-- name: WorksheetRows :many
-- Neraca lajur: saldo sebelum penyesuaian bulan @month, dan penyesuaian bulan itu.
SELECT a.id, a.code, a.name, a.class,
  coalesce(sum(b.debit - b.credit) FILTER (WHERE NOT (b.month = @month AND b.kind = 'penyesuaian')), 0)::numeric AS unadjusted,
  coalesce(sum(b.debit) FILTER (WHERE b.month = @month AND b.kind = 'penyesuaian'), 0)::numeric AS adj_debit,
  coalesce(sum(b.credit) FILTER (WHERE b.month = @month AND b.kind = 'penyesuaian'), 0)::numeric AS adj_credit
FROM ledger.balances b
JOIN ledger.accounts a ON a.id = b.account_id
WHERE b.company_id = @company_id AND b.month <= @month
  AND (a.class <= 3 OR b.month >= @fiscal_start)
GROUP BY a.id, a.code, a.name, a.class
ORDER BY a.code;

-- name: PriorYearsResult :one
-- Laba rugi tahun-tahun sebelumnya yang belum ditutup ke laba ditahan.
SELECT coalesce(sum(b.debit - b.credit), 0)::numeric AS net
FROM ledger.balances b
JOIN ledger.accounts a ON a.id = b.account_id
WHERE b.company_id = @company_id AND a.class >= 4 AND b.month < @fiscal_start;

-- name: AccountOpening :one
SELECT coalesce(sum(debit - credit), 0)::numeric AS net
FROM ledger.journal_lines
WHERE company_id = @company_id AND account_id = @account_id
  AND entry_date < @before_date AND entry_date >= @since_date;

-- name: AccountLines :many
SELECT l.entry_id, e.number, e.kind, e.status, e.source_type, l.entry_date, l.line_no,
       e.memo AS entry_memo, l.memo, l.debit, l.credit
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
WHERE l.company_id = @company_id AND l.account_id = @account_id
  AND l.entry_date >= @from_date AND l.entry_date <= @to_date
ORDER BY l.entry_date, e.posted_at, e.number, l.line_no
LIMIT @row_limit;

-- name: BalanceMismatches :one
-- Jumlah sel saldo yang tidak sama dengan agregat baris jurnal; seharusnya selalu 0.
WITH l AS (
  SELECT jl.account_id, date_trunc('month', jl.entry_date)::date AS month, e.kind,
         sum(jl.debit) AS debit, sum(jl.credit) AS credit
  FROM ledger.journal_lines jl
  JOIN ledger.journal_entries e ON e.id = jl.entry_id
  WHERE jl.company_id = @company_id
  GROUP BY 1, 2, 3
), b AS (
  SELECT account_id, month, kind, debit, credit FROM ledger.balances WHERE company_id = @company_id
)
SELECT count(*) FROM l FULL JOIN b USING (account_id, month, kind)
WHERE l.debit IS DISTINCT FROM b.debit OR l.credit IS DISTINCT FROM b.credit;
