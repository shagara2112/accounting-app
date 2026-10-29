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

-- name: OutputTrxCodes :many
-- Kode transaksi yang ditetapkan staf pajak per faktur, untuk rekap PPN dipungut pemungut.
SELECT invoice_id, trx_code FROM tax.output_invoices WHERE company_id = @company_id;

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
INSERT INTO tax.export_batches (id, company_id, period, format, invoice_count, file_name, content, created_by, kind)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetExportBatch :one
SELECT * FROM tax.export_batches WHERE company_id = $1 AND id = $2;

-- name: ListExportBatches :many
SELECT b.id, b.kind, b.period, b.format, b.invoice_count, b.file_name, b.created_at, u.name AS created_by_name
FROM tax.export_batches b
LEFT JOIN platform.users u ON u.id = b.created_by
WHERE b.company_id = $1 AND b.period = $2 AND b.kind = ANY(@kinds::text[])
ORDER BY b.created_at DESC;

-- name: SetWithholdingObjects :exec
INSERT INTO tax.settings (company_id, withholding_objects, updated_at, updated_by)
VALUES ($1, $2, now(), $3)
ON CONFLICT (company_id) DO UPDATE
SET withholding_objects = EXCLUDED.withholding_objects, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- Faktur pajak masukan (TAX-07) ---------------------------------------------------------

-- name: InputInvoiceStates :many
SELECT * FROM tax.input_invoices WHERE company_id = @company_id AND bill_id = ANY(@ids::uuid[]);

-- name: AllInputInvoiceStates :many
SELECT bill_id, creditable, credit_period FROM tax.input_invoices WHERE company_id = $1;

-- name: GetInputInvoiceForUpdate :one
SELECT * FROM tax.input_invoices WHERE company_id = $1 AND bill_id = $2 FOR UPDATE;

-- name: InputInvoiceByNumber :one
SELECT bill_id FROM tax.input_invoices WHERE company_id = $1 AND tax_invoice_number = $2;

-- name: UpsertInputInvoice :one
INSERT INTO tax.input_invoices
  (bill_id, company_id, tax_invoice_number, tax_invoice_date, creditable, credit_period, reclass_entry_id,
   coretax_vat, coretax_checked_at, note, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), $11)
ON CONFLICT (bill_id) DO UPDATE
SET tax_invoice_number = EXCLUDED.tax_invoice_number, tax_invoice_date = EXCLUDED.tax_invoice_date,
    creditable = EXCLUDED.creditable, credit_period = EXCLUDED.credit_period, reclass_entry_id = EXCLUDED.reclass_entry_id,
    coretax_vat = EXCLUDED.coretax_vat, coretax_checked_at = EXCLUDED.coretax_checked_at, note = EXCLUDED.note,
    updated_at = now(), updated_by = EXCLUDED.updated_by
RETURNING *;

-- Bukti potong (TAX-08) -----------------------------------------------------------------

-- name: WithholdingSlipStates :many
SELECT * FROM tax.withholding_slips WHERE company_id = @company_id AND bill_id = ANY(@ids::uuid[]);

-- name: GetWithholdingSlipForUpdate :one
SELECT * FROM tax.withholding_slips WHERE company_id = $1 AND bill_id = $2 FOR UPDATE;

-- name: UpsertWithholdingSlip :one
INSERT INTO tax.withholding_slips
  (bill_id, company_id, tax_object_code, facility, facility_number, deemed, document, document_number, document_date,
   withholding_date, status, slip_number, approved_date, exported_base, exported_amount, last_batch_id, note, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, now(), $18)
ON CONFLICT (bill_id) DO UPDATE
SET tax_object_code = EXCLUDED.tax_object_code, facility = EXCLUDED.facility, facility_number = EXCLUDED.facility_number,
    deemed = EXCLUDED.deemed, document = EXCLUDED.document, document_number = EXCLUDED.document_number,
    document_date = EXCLUDED.document_date, withholding_date = EXCLUDED.withholding_date, status = EXCLUDED.status,
    slip_number = EXCLUDED.slip_number, approved_date = EXCLUDED.approved_date, exported_base = EXCLUDED.exported_base,
    exported_amount = EXCLUDED.exported_amount, last_batch_id = EXCLUDED.last_batch_id, note = EXCLUDED.note,
    updated_at = now(), updated_by = EXCLUDED.updated_by
RETURNING *;

-- name: WithholdingSlipByNumber :one
SELECT bill_id FROM tax.withholding_slips WHERE company_id = $1 AND slip_number = $2;
