-- name: InsertProject :exec
INSERT INTO project.projects
  (id, company_id, code, name, customer_id, contract_value, dp_pct, retention_pct, pph_code,
   revenue_account_id, start_date, end_date, status, notes, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: UpdateProject :execrows
UPDATE project.projects
SET code = @code, name = @name, customer_id = @customer_id, contract_value = @contract_value,
    dp_pct = @dp_pct, retention_pct = @retention_pct, pph_code = @pph_code,
    revenue_account_id = @revenue_account_id, start_date = @start_date, end_date = @end_date,
    status = @status, notes = @notes, version = version + 1
WHERE company_id = @company_id AND id = @id AND version = @version;

-- name: GetProject :one
SELECT * FROM project.projects WHERE company_id = $1 AND id = $2;

-- name: GetProjectForUpdate :one
SELECT * FROM project.projects WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: ProjectCodeTaken :one
SELECT EXISTS (
  SELECT 1 FROM project.projects
  WHERE company_id = @company_id AND lower(code) = lower(@code) AND id <> @id
);

-- name: ListProjects :many
SELECT * FROM project.projects
WHERE company_id = @company_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(search)::text IS NULL OR code ILIKE '%' || sqlc.narg(search)::text || '%'
       OR name ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY code
LIMIT 1000;

-- name: ProjectsByIDs :many
SELECT id, code, name, status FROM project.projects
WHERE company_id = @company_id AND id = ANY(@ids::uuid[]);

-- name: DeleteBudget :exec
DELETE FROM project.budget_lines WHERE company_id = $1 AND project_id = $2;

-- name: InsertBudgetLine :exec
INSERT INTO project.budget_lines (id, company_id, project_id, line_no, account_id, description, amount)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: BudgetLines :many
SELECT * FROM project.budget_lines WHERE company_id = $1 AND project_id = $2 ORDER BY line_no;

-- name: BudgetTotals :many
SELECT project_id, sum(amount)::numeric AS amount
FROM project.budget_lines
WHERE company_id = $1
GROUP BY project_id;
