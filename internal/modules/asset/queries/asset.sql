-- Kategori ------------------------------------------------------------------------------

-- name: ListCategories :many
SELECT * FROM asset.categories WHERE company_id = $1 ORDER BY lower(name);

-- name: GetCategory :one
SELECT * FROM asset.categories WHERE company_id = $1 AND id = $2;

-- name: InsertCategory :exec
INSERT INTO asset.categories (id, company_id, name, asset_account_id, accum_account_id, expense_account_id,
  method, life_months, fiscal_group, fiscal_method)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: UpdateCategory :execrows
UPDATE asset.categories
SET name = $3, asset_account_id = $4, accum_account_id = $5, expense_account_id = $6, method = $7,
    life_months = $8, fiscal_group = $9, fiscal_method = $10, active = $11, version = version + 1
WHERE company_id = $1 AND id = $2 AND version = @expected;

-- name: AccountIDsByCode :many
SELECT id, code FROM ledger.accounts
WHERE company_id = $1 AND code = ANY(@codes::text[]) AND postable AND active;

-- Aset ---------------------------------------------------------------------------------

-- name: ListAssets :many
SELECT a.*, c.name AS category_name, coalesce(d.total, 0)::numeric AS depreciated
FROM asset.assets a
JOIN asset.categories c ON c.id = a.category_id
LEFT JOIN LATERAL (
  SELECT sum(l.amount) AS total FROM asset.depreciation_lines l WHERE l.asset_id = a.id
) d ON true
WHERE a.company_id = $1
ORDER BY a.status, lower(a.code);

-- name: GetAsset :one
SELECT a.*, c.name AS category_name, u.name AS created_by_name, b.code AS branch_code, dp.code AS department_code
FROM asset.assets a
JOIN asset.categories c ON c.id = a.category_id
LEFT JOIN platform.users u ON u.id = a.created_by
LEFT JOIN core.branches b ON b.id = a.branch_id
LEFT JOIN core.departments dp ON dp.id = a.department_id
WHERE a.company_id = $1 AND a.id = $2;

-- name: GetAssetForUpdate :one
SELECT * FROM asset.assets WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: AssetCodeTaken :one
SELECT EXISTS (
  SELECT 1 FROM asset.assets WHERE company_id = $1 AND lower(code) = lower(@code) AND id <> @exclude
);

-- name: CountAssets :one
SELECT count(*) FROM asset.assets WHERE company_id = $1;

-- name: InsertAsset :exec
INSERT INTO asset.assets (id, company_id, code, name, category_id, acquired_on, cost, residual, method, life_months,
  depreciation_start, asset_account_id, accum_account_id, expense_account_id, fiscal_group, fiscal_method,
  opening_accum, opening_month, location, custodian, notes, branch_id, department_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24);

-- name: UpdateAsset :execrows
UPDATE asset.assets
SET code = $3, name = $4, category_id = $5, acquired_on = $6, cost = $7, residual = $8, method = $9,
    life_months = $10, depreciation_start = $11, asset_account_id = $12, accum_account_id = $13,
    expense_account_id = $14, fiscal_group = $15, fiscal_method = $16, opening_accum = $17, opening_month = $18,
    location = $19, custodian = $20, notes = $21, branch_id = $22, department_id = $23, version = version + 1
WHERE company_id = $1 AND id = $2 AND version = @expected;

-- name: DeleteAsset :execrows
DELETE FROM asset.assets WHERE company_id = $1 AND id = $2 AND version = @expected;

-- name: SetAssetStatus :exec
UPDATE asset.assets SET status = $3, disposed_on = $4, version = version + 1
WHERE company_id = $1 AND id = $2;

-- name: DepreciableAssets :many
-- Aset aktif yang sudah mulai disusutkan s.d. bulan itu, dikunci supaya tidak dilepas bersamaan.
SELECT * FROM asset.assets
WHERE company_id = $1 AND status = 'aktif' AND method <> 'tidak_disusutkan' AND depreciation_start <= @month
ORDER BY lower(code)
FOR UPDATE;

-- name: DepreciableAssetsPreview :many
-- Sama dengan DepreciableAssets tanpa kunci, untuk pratinjau (transaksi baca saja).
SELECT * FROM asset.assets
WHERE company_id = $1 AND status = 'aktif' AND method <> 'tidak_disusutkan' AND depreciation_start <= @month
ORDER BY lower(code);

-- name: CompaniesWithDepreciableAssets :many
SELECT DISTINCT company_id FROM asset.assets WHERE status = 'aktif' AND method <> 'tidak_disusutkan';

-- name: RegisterTotals :many
-- Total register per akun: harga perolehan aset aktif per akun aset, dan akumulasi
-- penyusutannya (saldo awal + yang sudah dijurnal) per akun akumulasi.
SELECT a.asset_account_id AS account_id, 'aset'::text AS side, sum(a.cost)::numeric AS amount
FROM asset.assets a WHERE a.company_id = $1 AND a.status = 'aktif'
GROUP BY a.asset_account_id
UNION ALL
SELECT a.accum_account_id AS account_id, 'akumulasi'::text AS side,
       sum(a.opening_accum + coalesce((SELECT sum(l.amount) FROM asset.depreciation_lines l WHERE l.asset_id = a.id), 0))::numeric AS amount
FROM asset.assets a WHERE a.company_id = $1 AND a.status = 'aktif' AND a.accum_account_id IS NOT NULL
GROUP BY a.accum_account_id;

-- Baris penyusutan -----------------------------------------------------------------------

-- name: RecordedMonths :many
SELECT asset_id, month FROM asset.depreciation_lines WHERE company_id = $1 AND asset_id = ANY(@ids::uuid[]);

-- name: InsertDepreciationLine :exec
INSERT INTO asset.depreciation_lines (id, company_id, asset_id, month, amount, run_id, disposal_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: AssetDepreciation :many
SELECT l.month, l.amount, l.run_id, l.disposal_id,
       coalesce(r.number, d.number)::text AS number,
       coalesce(r.journal_entry_id, d.journal_entry_id)::uuid AS journal_entry_id
FROM asset.depreciation_lines l
LEFT JOIN asset.depreciation_runs r ON r.id = l.run_id
LEFT JOIN asset.disposals d ON d.id = l.disposal_id
WHERE l.company_id = $1 AND l.asset_id = $2
ORDER BY l.month;

-- name: DeleteRunLines :exec
DELETE FROM asset.depreciation_lines WHERE company_id = $1 AND run_id = $2;

-- name: DeleteDisposalLines :exec
DELETE FROM asset.depreciation_lines WHERE company_id = $1 AND disposal_id = $2;

-- Jurnal penyusutan bulanan --------------------------------------------------------------

-- name: InsertRun :exec
INSERT INTO asset.depreciation_runs (id, company_id, month, number, journal_entry_id, total, assets, posted_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListRuns :many
SELECT r.*, u.name AS posted_by_name
FROM asset.depreciation_runs r
LEFT JOIN platform.users u ON u.id = r.posted_by
WHERE r.company_id = $1
ORDER BY r.month DESC, r.posted_at DESC
LIMIT 120;

-- name: GetRun :one
SELECT r.*, u.name AS posted_by_name, vu.name AS voided_by_name
FROM asset.depreciation_runs r
LEFT JOIN platform.users u ON u.id = r.posted_by
LEFT JOIN platform.users vu ON vu.id = r.voided_by
WHERE r.company_id = $1 AND r.id = $2;

-- name: GetRunForUpdate :one
SELECT * FROM asset.depreciation_runs WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: PostedRunForMonth :one
SELECT id FROM asset.depreciation_runs WHERE company_id = $1 AND month = $2 AND status = 'posted';

-- name: RunAssets :many
SELECT a.id, a.code, a.name, count(*)::int AS months, min(l.month)::date AS first_month, sum(l.amount)::numeric AS amount
FROM asset.depreciation_lines l
JOIN asset.assets a ON a.id = l.asset_id
WHERE l.company_id = $1 AND l.run_id = $2
GROUP BY a.id, a.code, a.name
ORDER BY lower(a.code);

-- name: RunDisposedAssets :many
-- Aset di jurnal penyusutan ini yang sudah dilepas: void penyusutannya harus menunggu
-- pelepasan dibatalkan, karena jurnal pelepasan memakai akumulasi penyusutan itu.
SELECT DISTINCT a.code FROM asset.depreciation_lines l
JOIN asset.assets a ON a.id = l.asset_id
WHERE l.company_id = $1 AND l.run_id = $2 AND a.status = 'dilepas';

-- name: VoidRun :execrows
UPDATE asset.depreciation_runs SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- Pelepasan ------------------------------------------------------------------------------

-- name: InsertDisposal :exec
INSERT INTO asset.disposals (id, company_id, asset_id, number, kind, disposal_date, proceeds, proceeds_account_id,
  cost, accumulated, book_value, gain, memo, journal_entry_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: AssetDisposals :many
SELECT d.*, u.name AS created_by_name, vu.name AS voided_by_name
FROM asset.disposals d
LEFT JOIN platform.users u ON u.id = d.created_by
LEFT JOIN platform.users vu ON vu.id = d.voided_by
WHERE d.company_id = $1 AND d.asset_id = $2
ORDER BY d.created_at DESC;

-- name: DisposalAsset :one
SELECT asset_id FROM asset.disposals WHERE company_id = $1 AND id = $2;

-- name: GetDisposalForUpdate :one
SELECT * FROM asset.disposals WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: VoidDisposal :execrows
UPDATE asset.disposals SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';
