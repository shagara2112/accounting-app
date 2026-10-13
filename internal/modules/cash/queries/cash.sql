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

-- name: InsertTransaction :exec
INSERT INTO cash.transactions
  (id, company_id, number, kind, trx_date, cash_account_id, to_account_id, amount, memo, reference,
   journal_entry_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: InsertTransactionLine :exec
INSERT INTO cash.transaction_lines (id, company_id, transaction_id, line_no, account_id, amount, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetTransaction :one
SELECT t.*, u.name AS created_by_name
FROM cash.transactions t
LEFT JOIN platform.users u ON u.id = t.created_by
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
SELECT * FROM cash.transactions
WHERE company_id = @company_id AND trx_date >= @from_date AND trx_date <= @to_date
  AND (sqlc.narg(account_id)::uuid IS NULL
       OR cash_account_id = sqlc.narg(account_id)::uuid OR to_account_id = sqlc.narg(account_id)::uuid)
ORDER BY trx_date DESC, id DESC
LIMIT 500;
