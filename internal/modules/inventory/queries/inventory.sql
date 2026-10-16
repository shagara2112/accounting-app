-- Produk ----------------------------------------------------------------------------

-- name: InsertProduct :exec
INSERT INTO inventory.products
  (id, company_id, code, name, kind, unit, sale_price, purchase_price, revenue_account_id,
   expense_account_id, inventory_account_id, active, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: UpdateProduct :execrows
UPDATE inventory.products
SET code = @code, name = @name, kind = @kind, unit = @unit, sale_price = @sale_price,
    purchase_price = @purchase_price, revenue_account_id = @revenue_account_id,
    expense_account_id = @expense_account_id, inventory_account_id = @inventory_account_id,
    active = @active, version = version + 1
WHERE company_id = @company_id AND id = @id AND version = @version;

-- name: GetProduct :one
SELECT * FROM inventory.products WHERE company_id = $1 AND id = $2;

-- name: ProductCodeTaken :one
SELECT EXISTS (
  SELECT 1 FROM inventory.products
  WHERE company_id = @company_id AND lower(code) = lower(@code) AND id <> @id
);

-- name: ListProducts :many
SELECT p.*, coalesce(s.qty, 0)::numeric AS stock_qty, coalesce(s.value, 0)::numeric AS stock_value
FROM inventory.products p
LEFT JOIN inventory.stock s ON s.company_id = p.company_id AND s.product_id = p.id
WHERE p.company_id = @company_id
  AND (sqlc.narg(kind)::text IS NULL OR p.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(active)::boolean IS NULL OR p.active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(search)::text IS NULL OR p.code ILIKE '%' || sqlc.narg(search)::text || '%'
       OR p.name ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY p.code
LIMIT 1000;

-- name: ProductsByIDs :many
SELECT * FROM inventory.products WHERE company_id = @company_id AND id = ANY(@ids::uuid[]);

-- name: InventoryAccountIDs :many
SELECT DISTINCT inventory_account_id::uuid AS account_id
FROM inventory.products
WHERE company_id = $1 AND inventory_account_id IS NOT NULL;

-- Stok dan pergerakan ---------------------------------------------------------------

-- name: EnsureStock :exec
INSERT INTO inventory.stock (company_id, product_id)
SELECT @company_id, unnest(@product_ids::uuid[])
ON CONFLICT DO NOTHING;

-- name: LockStock :many
SELECT * FROM inventory.stock
WHERE company_id = @company_id AND product_id = ANY(@product_ids::uuid[])
ORDER BY product_id
FOR UPDATE;

-- name: LockFreeStock :many
SELECT * FROM inventory.stock WHERE company_id = @company_id AND product_id = ANY(@product_ids::uuid[]);

-- name: HasMovements :one
SELECT EXISTS (SELECT 1 FROM inventory.movements WHERE company_id = $1 AND product_id = $2);

-- name: UpdateStock :exec
UPDATE inventory.stock SET qty = $3, value = $4 WHERE company_id = $1 AND product_id = $2;

-- name: InsertMovement :one
INSERT INTO inventory.movements
  (id, company_id, product_id, move_date, qty, valuation, fixed_value, mirror_of, owned,
   counter_account_id, project_id, source_type, source_id, source_number, memo, cost_of, branch_id, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
RETURNING *;

-- name: MovementBefore :one
-- Saldo stok sebelum tanggal @before: baris terakhir sebelum tanggal itu.
SELECT qty_after, value_after FROM inventory.movements
WHERE company_id = @company_id AND product_id = @product_id AND move_date < @before
ORDER BY move_date DESC, seq DESC
LIMIT 1;

-- name: MovementsFrom :many
SELECT * FROM inventory.movements
WHERE company_id = @company_id AND product_id = @product_id AND move_date >= @from_date
ORDER BY move_date, seq;

-- name: MovementValue :one
SELECT value, qty FROM inventory.movements WHERE company_id = $1 AND id = $2;

-- name: UpdateMovementCalc :exec
UPDATE inventory.movements SET value = $3, qty_after = $4, value_after = $5
WHERE company_id = $1 AND id = $2;

-- name: MarkMovementPosted :exec
UPDATE inventory.movements SET posted_value = $3 WHERE company_id = $1 AND id = $2;

-- name: MovementsBySource :many
SELECT m.*, EXISTS (SELECT 1 FROM inventory.movements r WHERE r.mirror_of = m.id) AS reversed
FROM inventory.movements m
WHERE m.company_id = @company_id AND m.source_type = @source_type AND m.source_id = @source_id
  AND m.mirror_of IS NULL
ORDER BY m.seq;

-- name: CardMovements :many
SELECT * FROM inventory.movements
WHERE company_id = @company_id AND product_id = @product_id
  AND move_date >= @from_date AND move_date <= @to_date
ORDER BY move_date, seq
LIMIT @row_limit;

-- name: StockAsOf :many
-- Saldo stok setiap produk per tanggal @as_of, dari pergerakan terakhir sampai tanggal itu.
SELECT DISTINCT ON (product_id) product_id, qty_after, value_after
FROM inventory.movements
WHERE company_id = @company_id AND move_date <= @as_of
ORDER BY product_id, move_date DESC, seq DESC;

-- Dokumen persediaan ---------------------------------------------------------------

-- name: InsertAdjustment :exec
INSERT INTO inventory.adjustments (id, company_id, number, kind, adj_date, account_id, project_id, memo, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: InsertAdjustmentLine :exec
INSERT INTO inventory.adjustment_lines
  (id, company_id, adjustment_id, line_no, product_id, system_qty, counted_qty, qty, unit_cost, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetAdjustment :one
SELECT a.*, cu.name AS created_by_name, vu.name AS voided_by_name
FROM inventory.adjustments a
LEFT JOIN platform.users cu ON cu.id = a.created_by
LEFT JOIN platform.users vu ON vu.id = a.voided_by
WHERE a.company_id = $1 AND a.id = $2;

-- name: GetAdjustmentForUpdate :one
SELECT * FROM inventory.adjustments WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: AdjustmentLines :many
SELECT l.*, p.code AS product_code, p.name AS product_name, p.unit AS product_unit
FROM inventory.adjustment_lines l
JOIN inventory.products p ON p.id = l.product_id
WHERE l.company_id = $1 AND l.adjustment_id = $2
ORDER BY l.line_no;

-- name: ListAdjustments :many
SELECT a.*, (SELECT count(*) FROM inventory.adjustment_lines l WHERE l.adjustment_id = a.id) AS line_count
FROM inventory.adjustments a
WHERE a.company_id = @company_id AND a.adj_date >= @from_date AND a.adj_date <= @to_date
ORDER BY a.adj_date DESC, a.id DESC
LIMIT 500;

-- name: MarkAdjustmentVoid :execrows
UPDATE inventory.adjustments
SET status = 'void', voided_at = now(), voided_by = $3, void_date = $4, void_reason = $5
WHERE company_id = $1 AND id = $2 AND status = 'posted';
