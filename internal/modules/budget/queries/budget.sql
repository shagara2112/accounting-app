-- Anggaran (BUD-01/02). Modul ini memiliki skema budget dan hanya membaca tabel buku besar
-- untuk realisasi (TDD, aturan antar modul nomor 3).

-- name: ListBudgets :many
SELECT b.*, (SELECT count(*) FROM budget.lines l WHERE l.budget_id = b.id)::int AS line_count
FROM budget.budgets b
WHERE b.company_id = $1
ORDER BY b.fiscal_year DESC, b.created_at;

-- name: GetBudget :one
SELECT * FROM budget.budgets WHERE company_id = $1 AND id = $2;

-- name: GetBudgetForUpdate :one
SELECT * FROM budget.budgets WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: ActiveBudget :one
SELECT * FROM budget.budgets WHERE company_id = $1 AND fiscal_year = $2 AND active;

-- name: CountBudgetsInYear :one
SELECT count(*) FROM budget.budgets WHERE company_id = $1 AND fiscal_year = $2;

-- name: InsertBudget :exec
INSERT INTO budget.budgets (id, company_id, fiscal_year, name, active, created_by)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: RenameBudget :execrows
UPDATE budget.budgets SET name = @name, updated_at = now(), version = version + 1
WHERE company_id = @company_id AND id = @id AND version = @version;

-- name: DeactivateYear :exec
UPDATE budget.budgets SET active = false, updated_at = now()
WHERE company_id = $1 AND fiscal_year = $2 AND active;

-- name: ActivateBudget :exec
UPDATE budget.budgets SET active = true, updated_at = now(), version = version + 1
WHERE company_id = $1 AND id = $2;

-- name: DeleteBudget :execrows
DELETE FROM budget.budgets WHERE company_id = $1 AND id = $2 AND NOT active;

-- name: TouchBudget :execrows
UPDATE budget.budgets SET updated_at = now(), version = version + 1
WHERE company_id = @company_id AND id = @id AND version = @version;

-- name: BudgetLines :many
SELECT * FROM budget.lines WHERE company_id = $1 AND budget_id = $2;

-- name: DeleteSlice :exec
-- Menghapus baris satu irisan anggaran: tingkat PT (tanpa dimensi), satu departemen, atau satu proyek.
DELETE FROM budget.lines
WHERE company_id = @company_id AND budget_id = @budget_id
  AND department_id IS NOT DISTINCT FROM sqlc.narg(department_id)::uuid
  AND project_id IS NOT DISTINCT FROM sqlc.narg(project_id)::uuid;

-- name: InsertLine :exec
INSERT INTO budget.lines
  (id, company_id, budget_id, account_id, department_id, project_id, m01, m02, m03, m04, m05, m06, m07, m08, m09, m10, m11, m12)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18);

-- name: CopyLines :exec
INSERT INTO budget.lines
  (id, company_id, budget_id, account_id, department_id, project_id, m01, m02, m03, m04, m05, m06, m07, m08, m09, m10, m11, m12)
SELECT gen_random_uuid(), src.company_id, @to_budget::uuid, src.account_id, src.department_id, src.project_id,
       src.m01, src.m02, src.m03, src.m04, src.m05, src.m06, src.m07, src.m08, src.m09, src.m10, src.m11, src.m12
FROM budget.lines src
WHERE src.company_id = @src_company_id::uuid AND src.budget_id = @from_budget::uuid;

-- name: ActualAll :many
-- Realisasi akun laba rugi (kelas 4–9) per bulan dari saldo agregat, tanpa jurnal penutup.
SELECT b.account_id, b.month, sum(b.debit - b.credit)::numeric AS net
FROM ledger.balances b
JOIN ledger.accounts a ON a.id = b.account_id
WHERE b.company_id = @company_id AND b.month >= @from_month AND b.month < @to_month
  AND b.kind <> 'penutup' AND a.class >= 4
GROUP BY 1, 2;

-- name: ActualDepartment :many
-- Realisasi satu departemen per bulan dari baris jurnal, tanpa jurnal penutup.
SELECT l.account_id, date_trunc('month', l.entry_date)::date AS month, sum(l.debit - l.credit)::numeric AS net
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
JOIN ledger.accounts a ON a.id = l.account_id
WHERE l.company_id = @company_id AND l.entry_date >= @from_month AND l.entry_date < @to_month
  AND e.kind <> 'penutup' AND a.class >= 4 AND l.department_id = @department_id
GROUP BY 1, 2;

-- name: ActualProject :many
-- Realisasi satu proyek per bulan dari saldo agregat proyek.
SELECT pb.account_id, pb.month, sum(pb.debit - pb.credit)::numeric AS net
FROM ledger.project_balances pb
JOIN ledger.accounts a ON a.id = pb.account_id
WHERE pb.company_id = @company_id AND pb.project_id = @project_id
  AND pb.month >= @from_month AND pb.month < @to_month AND a.class >= 4
GROUP BY 1, 2;
