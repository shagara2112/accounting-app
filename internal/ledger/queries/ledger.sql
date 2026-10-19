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
SET code = @code, name = @name, active = @active, version = version + 1
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
INSERT INTO ledger.journal_lines (id, company_id, entry_id, entry_date, line_no, account_id, debit, credit, memo, project_id,
  branch_id, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: Dimensions :many
-- Cabang dan departemen yang dirujuk baris jurnal baru, untuk validasi.
SELECT 'cabang'::text AS kind, b.id, b.code, b.name, b.active FROM core.branches b
WHERE b.company_id = @company_id AND b.id = ANY(@branch_ids::uuid[])
UNION ALL
SELECT 'departemen'::text, d.id, d.code, d.name, d.active FROM core.departments d
WHERE d.company_id = @company_id AND d.id = ANY(@department_ids::uuid[]);

-- name: GetEntry :one
SELECT e.*, pu.name AS posted_by_name, vu.name AS voided_by_name, ru.name AS revised_by_name
FROM ledger.journal_entries e
LEFT JOIN platform.users pu ON pu.id = e.posted_by
LEFT JOIN platform.users vu ON vu.id = e.voided_by
LEFT JOIN platform.users ru ON ru.id = e.revised_by
WHERE e.company_id = $1 AND e.id = $2;

-- name: GetEntryForUpdate :one
SELECT * FROM ledger.journal_entries WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: EntryLines :many
SELECT l.line_no, l.account_id, a.code AS account_code, a.name AS account_name, l.debit, l.credit, l.memo, l.project_id,
       l.branch_id, l.department_id, b.code AS branch_code, d.code AS department_code
FROM ledger.journal_lines l
JOIN ledger.accounts a ON a.id = l.account_id
LEFT JOIN core.branches b ON b.id = l.branch_id
LEFT JOIN core.departments d ON d.id = l.department_id
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

-- name: EntriesBySource :many
SELECT id, number, kind, entry_date, memo, source_type, source_id, total, status, reversal_of
FROM ledger.journal_entries
WHERE company_id = @company_id AND source_type = @source_type AND status = 'posted'
ORDER BY entry_date, number;

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
-- Saldo satu akun sebelum before_date, bisa disaring cabang/departemen (GL-09); *_null memilih
-- baris tanpa dimensi itu.
SELECT coalesce(sum(debit - credit), 0)::numeric AS net
FROM ledger.journal_lines
WHERE company_id = @company_id AND account_id = @account_id
  AND entry_date < @before_date AND entry_date >= @since_date
  AND (sqlc.narg(branch_id)::uuid IS NULL OR branch_id = sqlc.narg(branch_id)::uuid)
  AND (NOT @branch_null::boolean OR branch_id IS NULL)
  AND (sqlc.narg(department_id)::uuid IS NULL OR department_id = sqlc.narg(department_id)::uuid)
  AND (NOT @department_null::boolean OR department_id IS NULL);

-- name: AccountLines :many
SELECT l.entry_id, e.number, e.kind, e.status, e.source_type, l.entry_date, l.line_no,
       e.memo AS entry_memo, l.memo, l.debit, l.credit, l.project_id, l.branch_id, l.department_id
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
WHERE l.company_id = @company_id AND l.account_id = @account_id
  AND l.entry_date >= @from_date AND l.entry_date <= @to_date
  AND (sqlc.narg(branch_id)::uuid IS NULL OR l.branch_id = sqlc.narg(branch_id)::uuid)
  AND (NOT @branch_null::boolean OR l.branch_id IS NULL)
  AND (sqlc.narg(department_id)::uuid IS NULL OR l.department_id = sqlc.narg(department_id)::uuid)
  AND (NOT @department_null::boolean OR l.department_id IS NULL)
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

-- name: ProjectBalanceMismatches :one
-- Sama seperti BalanceMismatches, untuk saldo per proyek.
WITH l AS (
  SELECT jl.project_id, jl.account_id, date_trunc('month', jl.entry_date)::date AS month,
         sum(jl.debit) AS debit, sum(jl.credit) AS credit
  FROM ledger.journal_lines jl
  WHERE jl.company_id = @company_id AND jl.project_id IS NOT NULL
  GROUP BY 1, 2, 3
), b AS (
  SELECT pb.project_id, pb.account_id, pb.month, pb.debit, pb.credit
  FROM ledger.project_balances pb WHERE pb.company_id = @company_id
)
SELECT count(*) FROM l FULL JOIN b USING (project_id, account_id, month)
WHERE l.debit IS DISTINCT FROM b.debit OR l.credit IS DISTINCT FROM b.credit;

-- Proyek ------------------------------------------------------------------------

-- name: ProjectNet :many
-- Mutasi debit dan kredit per proyek dan akun untuk bulan @from_month s.d. @to_month.
SELECT project_id, account_id, sum(debit)::numeric AS debit, sum(credit)::numeric AS credit
FROM ledger.project_balances
WHERE company_id = @company_id AND month >= @from_month AND month <= @to_month
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
GROUP BY project_id, account_id;

-- name: ProjectLines :many
SELECT l.entry_id, e.number, e.status, e.source_type, e.source_id, l.entry_date, l.line_no,
       e.memo AS entry_memo, l.memo, l.account_id, a.code AS account_code, a.name AS account_name,
       l.debit, l.credit
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
JOIN ledger.accounts a ON a.id = l.account_id
WHERE l.company_id = @company_id AND l.project_id = @project_id
  AND (sqlc.narg(account_id)::uuid IS NULL OR l.account_id = sqlc.narg(account_id)::uuid)
  AND l.entry_date >= @from_date AND l.entry_date <= @to_date
ORDER BY l.entry_date, e.posted_at, e.number, l.line_no
LIMIT @row_limit;

-- Penomoran dokumen (GL-07) ---------------------------------------------------------

-- name: GetNumberFormat :one
SELECT * FROM ledger.number_formats WHERE company_id = $1 AND series = $2;

-- name: ListNumberFormats :many
SELECT * FROM ledger.number_formats WHERE company_id = $1 ORDER BY series;

-- name: UpsertNumberFormat :exec
INSERT INTO ledger.number_formats (company_id, series, prefix, reset, digits, per_branch, updated_by, updated_at)
VALUES (@company_id, @series, @prefix, @reset, @digits, @per_branch, @updated_by, now())
ON CONFLICT (company_id, series) DO UPDATE
SET prefix = EXCLUDED.prefix, reset = EXCLUDED.reset, digits = EXCLUDED.digits, per_branch = EXCLUDED.per_branch,
    updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: BranchCode :one
SELECT code FROM core.branches WHERE company_id = $1 AND id = $2;

-- Jurnal berulang (GL-08) -----------------------------------------------------------

-- name: InsertRecurring :exec
INSERT INTO ledger.recurring_journals
  (id, company_id, name, kind, memo, auto_reverse, day_of_month, next_date, end_date, source_entry_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: InsertRecurringLine :exec
INSERT INTO ledger.recurring_lines
  (id, company_id, recurring_id, line_no, account_id, debit, credit, memo, project_id, branch_id, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListRecurring :many
SELECT r.*, coalesce((SELECT sum(l.debit) FROM ledger.recurring_lines l WHERE l.recurring_id = r.id), 0)::numeric AS total
FROM ledger.recurring_journals r
WHERE r.company_id = $1
ORDER BY r.active DESC, r.next_date, r.name;

-- name: GetRecurring :one
SELECT * FROM ledger.recurring_journals WHERE company_id = $1 AND id = $2;

-- name: LockRecurring :one
SELECT * FROM ledger.recurring_journals WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: RecurringLines :many
SELECT l.*, a.code AS account_code, a.name AS account_name
FROM ledger.recurring_lines l
JOIN ledger.accounts a ON a.id = l.account_id
WHERE l.company_id = $1 AND l.recurring_id = $2
ORDER BY l.line_no;

-- name: RecurringRuns :many
SELECT rr.run_date, rr.entry_id, e.number, e.status
FROM ledger.recurring_runs rr
JOIN ledger.journal_entries e ON e.id = rr.entry_id
WHERE rr.recurring_id = $1
ORDER BY rr.run_date DESC
LIMIT 24;

-- name: UpdateRecurring :execrows
UPDATE ledger.recurring_journals
SET name = @name, day_of_month = @day_of_month, next_date = @next_date, end_date = sqlc.narg(end_date),
    active = @active, last_error = NULL, version = version + 1
WHERE company_id = @company_id AND id = @id AND version = @version;

-- name: AdvanceRecurring :exec
UPDATE ledger.recurring_journals
SET next_date = @next_date, last_error = NULL, last_run_at = now(), version = version + 1
WHERE id = @id;

-- name: FailRecurring :exec
UPDATE ledger.recurring_journals SET last_error = @last_error, last_run_at = now() WHERE id = @id;

-- name: DeleteRecurring :execrows
DELETE FROM ledger.recurring_journals WHERE company_id = $1 AND id = $2;

-- name: InsertRecurringRun :execrows
INSERT INTO ledger.recurring_runs (recurring_id, run_date, entry_id) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: DueRecurring :many
SELECT company_id, id, created_by FROM ledger.recurring_journals
WHERE active AND next_date <= @today AND (end_date IS NULL OR next_date <= end_date)
ORDER BY next_date;

-- Revisi jurnal manual (revisi klien) ---------------------------------------------------

-- name: PeriodStatus :one
SELECT coalesce((SELECT status FROM ledger.periods WHERE company_id = $1 AND month = $2), 'open')::text AS status;

-- name: SetRevising :exec
-- Menandai jurnal yang boleh direvisi di transaksi ini (dibaca trigger guard_entry/guard_line).
SELECT set_config('lajur.revise_entry', @entry_id::text, true);

-- name: DeleteEntryLines :exec
DELETE FROM ledger.journal_lines WHERE company_id = $1 AND entry_id = $2;

-- name: ReviseEntry :execrows
UPDATE ledger.journal_entries
SET entry_date = $3, memo = $4, total = $5, revision = revision + 1, revised_at = now(), revised_by = $6
WHERE company_id = $1 AND id = $2 AND status = 'posted' AND revision = @expected;

-- name: InsertRevision :exec
INSERT INTO ledger.journal_revisions (id, company_id, entry_id, revision, entry_date, memo, total, lines, reason, revised_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: EntryRevisions :many
SELECT r.revision, r.entry_date, r.memo, r.total, r.lines, r.reason, r.revised_at, u.name AS revised_by_name
FROM ledger.journal_revisions r
LEFT JOIN platform.users u ON u.id = r.revised_by
WHERE r.company_id = $1 AND r.entry_id = $2
ORDER BY r.revision DESC;
