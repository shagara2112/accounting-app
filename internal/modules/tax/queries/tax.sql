-- name: ListTaxCodes :many
SELECT * FROM tax.tax_codes ORDER BY kind DESC, code, valid_from;

-- name: GetTaxSettings :one
SELECT * FROM tax.settings WHERE company_id = $1;

-- name: UpsertTaxSettings :one
INSERT INTO tax.settings (company_id, goods_opt, goods_code, unit_code, updated_at, updated_by)
VALUES ($1, $2, $3, $4, now(), $5)
ON CONFLICT (company_id) DO UPDATE
SET goods_opt = EXCLUDED.goods_opt, goods_code = EXCLUDED.goods_code, unit_code = EXCLUDED.unit_code,
    updated_at = now(), updated_by = EXCLUDED.updated_by
RETURNING *;

-- name: OutputInvoiceStates :many
SELECT * FROM tax.output_invoices WHERE company_id = @company_id AND invoice_id = ANY(@ids::uuid[]);

-- name: GetOutputInvoiceForUpdate :one
SELECT * FROM tax.output_invoices WHERE company_id = $1 AND invoice_id = $2 FOR UPDATE;

-- name: UpsertOutputInvoice :one
INSERT INTO tax.output_invoices
  (invoice_id, company_id, trx_code, status, tax_invoice_number, approved_date, last_batch_id, replaced_by, note, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10)
ON CONFLICT (invoice_id) DO UPDATE
SET trx_code = EXCLUDED.trx_code, status = EXCLUDED.status, tax_invoice_number = EXCLUDED.tax_invoice_number,
    approved_date = EXCLUDED.approved_date, last_batch_id = EXCLUDED.last_batch_id,
    replaced_by = EXCLUDED.replaced_by, note = EXCLUDED.note, updated_at = now(), updated_by = EXCLUDED.updated_by
RETURNING *;

-- name: InsertExportBatch :exec
INSERT INTO tax.export_batches (id, company_id, period, format, invoice_count, file_name, content, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetExportBatch :one
SELECT * FROM tax.export_batches WHERE company_id = $1 AND id = $2;

-- name: ListExportBatches :many
SELECT b.id, b.period, b.format, b.invoice_count, b.file_name, b.created_at, u.name AS created_by_name
FROM tax.export_batches b
LEFT JOIN platform.users u ON u.id = b.created_by
WHERE b.company_id = $1 AND b.period = $2
ORDER BY b.created_at DESC;
