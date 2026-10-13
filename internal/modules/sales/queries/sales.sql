-- Faktur ---------------------------------------------------------------------------

-- name: InsertInvoice :exec
INSERT INTO sales.invoices
  (id, company_id, customer_id, invoice_date, due_date, memo, subtotal, discount, dpp, other_dpp, vat, total, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: UpdateDraftInvoice :execrows
UPDATE sales.invoices
SET customer_id = @customer_id, invoice_date = @invoice_date, due_date = @due_date, memo = @memo,
    subtotal = @subtotal, discount = @discount, dpp = @dpp, other_dpp = @other_dpp, vat = @vat,
    total = @total, version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'draft' AND version = @version;

-- name: DeleteInvoiceLines :exec
DELETE FROM sales.invoice_lines WHERE company_id = $1 AND invoice_id = $2;

-- name: InsertInvoiceLine :exec
INSERT INTO sales.invoice_lines
  (id, company_id, invoice_id, line_no, description, account_id, qty, unit_price, discount_pct,
   discount, net, tax_code, other_dpp, vat)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetInvoice :one
SELECT i.*, cu.name AS created_by_name, pu.name AS posted_by_name, vu.name AS voided_by_name
FROM sales.invoices i
LEFT JOIN platform.users cu ON cu.id = i.created_by
LEFT JOIN platform.users pu ON pu.id = i.posted_by
LEFT JOIN platform.users vu ON vu.id = i.voided_by
WHERE i.company_id = $1 AND i.id = $2;

-- name: GetInvoiceForUpdate :one
SELECT * FROM sales.invoices WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: InvoiceLines :many
SELECT * FROM sales.invoice_lines WHERE company_id = $1 AND invoice_id = $2 ORDER BY line_no;

-- name: MarkInvoicePosted :execrows
UPDATE sales.invoices
SET status = 'posted', number = $3, journal_entry_id = $4, posted_at = now(), posted_by = $5, version = version + 1
WHERE company_id = $1 AND id = $2 AND status = 'draft';

-- name: MarkInvoiceVoid :execrows
UPDATE sales.invoices
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4, version = version + 1
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: DeleteDraftInvoice :execrows
DELETE FROM sales.invoices WHERE company_id = $1 AND id = $2 AND status = 'draft';

-- name: ListInvoices :many
-- state: draft, open (diposting, belum lunas), paid, atau void.
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid
FROM sales.invoices i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid
  FROM sales.receipt_allocations a
  JOIN sales.receipts r ON r.id = a.receipt_id
  WHERE a.invoice_id = i.id AND r.status = 'posted'
) p ON true
WHERE i.company_id = @company_id
  AND i.invoice_date >= @from_date AND i.invoice_date <= @to_date
  AND (sqlc.narg(customer_id)::uuid IS NULL OR i.customer_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(search)::text IS NULL OR i.number ILIKE '%' || sqlc.narg(search)::text || '%'
       OR i.memo ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(state)::text IS NULL OR sqlc.narg(state)::text =
       CASE WHEN i.status <> 'posted' THEN i.status
            WHEN coalesce(p.paid, 0) >= i.total THEN 'paid' ELSE 'open' END)
ORDER BY i.invoice_date DESC, i.id DESC
LIMIT 500;

-- name: LockInvoices :many
SELECT * FROM sales.invoices
WHERE company_id = @company_id AND id = ANY(@ids::uuid[])
ORDER BY id
FOR UPDATE;

-- name: PaidByInvoice :many
SELECT a.invoice_id, sum(a.amount)::numeric AS paid
FROM sales.receipt_allocations a
JOIN sales.receipts r ON r.id = a.receipt_id
WHERE a.company_id = @company_id AND a.invoice_id = ANY(@ids::uuid[]) AND r.status = 'posted'
GROUP BY a.invoice_id;

-- name: OpenInvoices :many
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid
FROM sales.invoices i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid
  FROM sales.receipt_allocations a
  JOIN sales.receipts r ON r.id = a.receipt_id
  WHERE a.invoice_id = i.id AND r.status = 'posted'
) p ON true
WHERE i.company_id = @company_id AND i.status = 'posted'
  AND (sqlc.narg(customer_id)::uuid IS NULL OR i.customer_id = sqlc.narg(customer_id)::uuid)
  AND coalesce(p.paid, 0) < i.total
ORDER BY i.due_date, i.number;

-- name: AgingInvoices :many
-- Faktur yang belum lunas per tanggal @as_of: pelunasan yang dihitung hanya yang bertanggal
-- sampai tanggal itu (AR-06).
SELECT i.id, i.number, i.customer_id, i.invoice_date, i.due_date, i.total,
  coalesce((SELECT sum(a.amount) FROM sales.receipt_allocations a
            JOIN sales.receipts r ON r.id = a.receipt_id
            WHERE a.invoice_id = i.id AND r.status = 'posted' AND r.receipt_date <= @as_of), 0)::numeric AS paid
FROM sales.invoices i
WHERE i.company_id = @company_id AND i.status = 'posted' AND i.invoice_date <= @as_of
ORDER BY i.due_date, i.number;

-- Pelunasan -------------------------------------------------------------------------

-- name: InsertReceipt :exec
INSERT INTO sales.receipts
  (id, company_id, number, customer_id, receipt_date, cash_account_id, amount, memo, journal_entry_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertAllocation :exec
INSERT INTO sales.receipt_allocations (id, company_id, receipt_id, invoice_id, amount, withholding_code, withholding)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetReceipt :one
SELECT r.*, u.name AS created_by_name, vu.name AS voided_by_name
FROM sales.receipts r
LEFT JOIN platform.users u ON u.id = r.created_by
LEFT JOIN platform.users vu ON vu.id = r.voided_by
WHERE r.company_id = $1 AND r.id = $2;

-- name: GetReceiptForUpdate :one
SELECT * FROM sales.receipts WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: ReceiptAllocations :many
SELECT a.*, i.number AS invoice_number, i.total AS invoice_total
FROM sales.receipt_allocations a
JOIN sales.invoices i ON i.id = a.invoice_id
WHERE a.company_id = $1 AND a.receipt_id = $2
ORDER BY i.number;

-- name: InvoiceReceipts :many
SELECT r.id, r.number, r.receipt_date, r.status, a.amount, a.withholding, a.withholding_code
FROM sales.receipt_allocations a
JOIN sales.receipts r ON r.id = a.receipt_id
WHERE a.company_id = $1 AND a.invoice_id = $2
ORDER BY r.receipt_date, r.number;

-- name: MarkReceiptVoid :execrows
UPDATE sales.receipts
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: ListReceipts :many
SELECT * FROM sales.receipts
WHERE company_id = @company_id AND receipt_date >= @from_date AND receipt_date <= @to_date
  AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)::uuid)
ORDER BY receipt_date DESC, id DESC
LIMIT 500;

-- Pajak ---------------------------------------------------------------------------

-- name: VATByMonth :many
-- PPN keluaran per masa dari faktur yang diposting (TAX-06).
SELECT date_trunc('month', invoice_date)::date AS month, count(*) AS invoices,
       coalesce(sum(dpp), 0)::numeric AS dpp, coalesce(sum(vat), 0)::numeric AS vat
FROM sales.invoices
WHERE company_id = @company_id AND status = 'posted' AND vat > 0
  AND invoice_date >= @from_date AND invoice_date < @to_date
GROUP BY 1
ORDER BY 1;

-- name: WithheldByCustomers :many
-- PPh yang dipotong pelanggan saat pelunasan, per masa pelunasan.
SELECT r.id AS receipt_id, r.number AS receipt_number, r.receipt_date, r.customer_id,
       i.number AS invoice_number, i.dpp, a.amount, a.withholding_code, a.withholding
FROM sales.receipt_allocations a
JOIN sales.receipts r ON r.id = a.receipt_id
JOIN sales.invoices i ON i.id = a.invoice_id
WHERE a.company_id = @company_id AND r.status = 'posted' AND a.withholding > 0
  AND r.receipt_date >= @from_date AND r.receipt_date < @to_date
ORDER BY r.receipt_date, r.number;
