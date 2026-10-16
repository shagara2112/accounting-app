-- name: ListCashAccounts :many
SELECT * FROM cash.accounts WHERE company_id = $1 ORDER BY created_at;

-- name: GetCashAccount :one
SELECT * FROM cash.accounts WHERE company_id = $1 AND account_id = $2;

-- name: UpsertCashAccount :one
INSERT INTO cash.accounts (company_id, account_id, kind, bank_name, account_number, active)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (company_id, account_id) DO UPDATE
SET kind = EXCLUDED.kind, bank_name = EXCLUDED.bank_name,
    account_number = EXCLUDED.account_number, active = EXCLUDED.active
RETURNING *;

-- name: InsertDraft :exec
INSERT INTO cash.transactions
  (id, company_id, kind, trx_date, cash_account_id, to_account_id, amount, memo, reference, created_by, status,
   branch_id, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'draft', $11, $12);

-- name: UpdateDraft :execrows
UPDATE cash.transactions
SET kind = @kind, trx_date = @trx_date, cash_account_id = @cash_account_id, to_account_id = @to_account_id,
    amount = @amount, memo = @memo, reference = @reference, branch_id = sqlc.narg(branch_id),
    department_id = sqlc.narg(department_id), version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'draft' AND version = @version;

-- name: MarkPosted :execrows
UPDATE cash.transactions
SET status = 'posted', number = @number, journal_entry_id = @journal_entry_id, posted_at = now(),
    posted_by = @posted_by, version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'draft';

-- name: DeleteLines :exec
DELETE FROM cash.transaction_lines WHERE company_id = $1 AND transaction_id = $2;

-- name: DeleteDraft :execrows
DELETE FROM cash.transactions WHERE company_id = $1 AND id = $2 AND status = 'draft';

-- name: DraftSummary :one
SELECT count(*) AS drafts, coalesce(sum(amount), 0)::numeric AS amount
FROM cash.transactions WHERE company_id = $1 AND status = 'draft';

-- name: InsertTransactionLine :exec
INSERT INTO cash.transaction_lines (id, company_id, transaction_id, line_no, account_id, amount, memo, project_id, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: DepartmentsByID :many
SELECT id, code, active FROM core.departments WHERE company_id = $1 AND id = ANY(@ids::uuid[]);

-- name: GetTransaction :one
SELECT t.*, u.name AS created_by_name, pu.name AS posted_by_name
FROM cash.transactions t
LEFT JOIN platform.users u ON u.id = t.created_by
LEFT JOIN platform.users pu ON pu.id = t.posted_by
WHERE t.company_id = $1 AND t.id = $2;

-- name: GetTransactionForUpdate :one
SELECT * FROM cash.transactions WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: TransactionLines :many
SELECT * FROM cash.transaction_lines WHERE company_id = $1 AND transaction_id = $2 ORDER BY line_no;

-- name: VoidTransaction :execrows
UPDATE cash.transactions
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: ListTransactions :many
-- drafts_only menampilkan semua draf tanpa batas tanggal, supaya tidak ada yang terlewat.
SELECT * FROM cash.transactions
WHERE company_id = @company_id
  AND (CASE WHEN @drafts_only::boolean THEN status = 'draft'
            ELSE trx_date >= @from_date AND trx_date <= @to_date END)
  AND (sqlc.narg(account_id)::uuid IS NULL
       OR cash_account_id = sqlc.narg(account_id)::uuid OR to_account_id = sqlc.narg(account_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR branch_id = ANY(@branches::uuid[]))
ORDER BY trx_date DESC, id DESC
LIMIT 500;

-- name: ListReconciliations :many
SELECT r.*, (SELECT count(*) FROM cash.recon_items i WHERE i.reconciliation_id = r.id)::int AS items
FROM cash.reconciliations r
WHERE r.company_id = @company_id AND (sqlc.narg(account_id)::uuid IS NULL OR r.account_id = sqlc.narg(account_id))
ORDER BY r.statement_date DESC
LIMIT 100;

-- name: GetReconciliation :one
SELECT * FROM cash.reconciliations WHERE company_id = $1 AND id = $2;

-- name: LockReconciliation :one
SELECT * FROM cash.reconciliations WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: LastClosedReconciliation :one
SELECT * FROM cash.reconciliations
WHERE company_id = $1 AND account_id = $2 AND status = 'closed'
ORDER BY statement_date DESC LIMIT 1;

-- name: LatestReconciliation :one
SELECT * FROM cash.reconciliations
WHERE company_id = $1 AND account_id = $2
ORDER BY statement_date DESC LIMIT 1;

-- name: InsertReconciliation :exec
INSERT INTO cash.reconciliations (id, company_id, account_id, statement_date, statement_balance, opening_balance, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: UpdateReconciliation :execrows
UPDATE cash.reconciliations SET statement_date = $3, statement_balance = $4, opening_balance = $5, version = version + 1
WHERE company_id = $1 AND id = $2 AND version = $6 AND status = 'open';

-- name: SetReconciliationStatus :exec
UPDATE cash.reconciliations
SET status = $3, closed_at = CASE WHEN $3 = 'closed' THEN now() END, closed_by = CASE WHEN $3 = 'closed' THEN sqlc.narg(actor)::uuid END,
    version = version + 1
WHERE company_id = $1 AND id = $2;

-- name: DeleteReconciliation :execrows
DELETE FROM cash.reconciliations WHERE company_id = $1 AND id = $2 AND status = 'open';

-- name: ReconCandidates :many
-- Baris buku besar rekening ini sampai tanggal rekening koran yang belum dicocokkan di
-- rekonsiliasi lain. Jurnal void dan pembaliknya saling meniadakan dan tidak pernah muncul di
-- rekening koran, jadi disembunyikan, kecuali baris aslinya sudah terlanjur dicocokkan.
SELECT l.id, l.entry_id, l.entry_date, l.line_no, l.debit, l.credit, l.memo,
       e.number, e.memo AS entry_memo, e.source_type, e.source_id,
       (ri.reconciliation_id IS NOT NULL)::boolean AS cleared
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
LEFT JOIN cash.recon_items ri ON ri.company_id = l.company_id AND ri.journal_line_id = l.id
WHERE l.company_id = @company_id AND l.account_id = @account_id AND l.entry_date <= @until
  AND (ri.reconciliation_id IS NULL OR ri.reconciliation_id = @reconciliation_id)
  AND NOT (e.status = 'void' AND ri.reconciliation_id IS NULL)
  AND NOT (e.source_type = 'void' AND ri.reconciliation_id IS NULL AND NOT EXISTS (
        SELECT 1 FROM cash.recon_items r2
        JOIN ledger.journal_lines ol ON ol.id = r2.journal_line_id
        WHERE ol.entry_id = e.reversal_of AND ol.account_id = l.account_id))
ORDER BY l.entry_date, e.number, l.line_no;

-- name: ClearLine :exec
INSERT INTO cash.recon_items (reconciliation_id, company_id, journal_line_id) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: UnclearLine :exec
DELETE FROM cash.recon_items WHERE reconciliation_id = $1 AND journal_line_id = $2;

-- name: LineOnAccount :one
SELECT EXISTS (
  SELECT 1 FROM ledger.journal_lines WHERE company_id = $1 AND id = $2 AND account_id = $3 AND entry_date <= $4
)::boolean;

-- name: ClearedTotal :one
SELECT COALESCE(sum(l.debit - l.credit), 0)::numeric AS total
FROM cash.recon_items ri JOIN ledger.journal_lines l ON l.id = ri.journal_line_id
WHERE ri.reconciliation_id = $1;

-- name: BookBalanceAt :one
SELECT COALESCE(sum(l.debit - l.credit), 0)::numeric AS balance
FROM ledger.journal_lines l
WHERE l.company_id = $1 AND l.account_id = $2 AND l.entry_date <= $3;
