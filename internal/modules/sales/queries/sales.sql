-- Faktur ---------------------------------------------------------------------------

-- name: InsertInvoice :exec
INSERT INTO sales.invoices
  (id, company_id, customer_id, invoice_date, due_date, memo, subtotal, discount, dpp, other_dpp, vat, total, created_by,
   kind, project_id, progress_pct, retention, branch_id, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19);

-- name: UpdateDraftInvoice :execrows
UPDATE sales.invoices
SET customer_id = @customer_id, invoice_date = @invoice_date, due_date = @due_date, memo = @memo,
    subtotal = @subtotal, discount = @discount, dpp = @dpp, other_dpp = @other_dpp, vat = @vat,
    total = @total, project_id = @project_id, branch_id = sqlc.narg(branch_id), department_id = sqlc.narg(department_id),
    version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'draft' AND version = @version;

-- name: DeleteInvoiceLines :exec
DELETE FROM sales.invoice_lines WHERE company_id = $1 AND invoice_id = $2;

-- name: InsertInvoiceLine :exec
INSERT INTO sales.invoice_lines
  (id, company_id, invoice_id, line_no, description, account_id, qty, unit_price, discount_pct,
   discount, net, tax_code, other_dpp, vat, product_id, discount_fixed)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

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
SET status = 'posted', number = @number, journal_entry_id = @journal_entry_id, posted_at = now(), posted_by = @posted_by,
    vat_collector = @vat_collector, vat_account_id = sqlc.narg(vat_account_id), version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'draft';

-- name: MarkInvoiceVoid :execrows
UPDATE sales.invoices
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4, version = version + 1
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: DeleteDraftInvoice :execrows
DELETE FROM sales.invoices WHERE company_id = $1 AND id = $2 AND status = 'draft';

-- name: ListInvoices :many
-- state: draft, open (diposting, belum lunas), paid, atau void. Nota kredit ikut mengurangi sisa.
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid, coalesce(c.credited, 0)::numeric AS credited,
       coalesce(p.vat_collected, 0)::numeric AS vat_collected_total
FROM sales.invoices i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid, sum(a.vat_collected) AS vat_collected
  FROM sales.receipt_allocations a
  JOIN sales.receipts r ON r.id = a.receipt_id
  WHERE a.invoice_id = i.id AND r.status = 'posted'
) p ON true
LEFT JOIN LATERAL (
  SELECT sum(n.applied) AS credited FROM sales.credit_notes n WHERE n.invoice_id = i.id AND n.status = 'posted'
) c ON true
WHERE i.company_id = @company_id
  AND i.invoice_date >= @from_date AND i.invoice_date <= @to_date
  AND (sqlc.narg(customer_id)::uuid IS NULL OR i.customer_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(project_id)::uuid IS NULL OR i.project_id = sqlc.narg(project_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR i.branch_id = ANY(@branches::uuid[]))
  AND (sqlc.narg(search)::text IS NULL OR i.number ILIKE '%' || sqlc.narg(search)::text || '%'
       OR i.memo ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(state)::text IS NULL OR sqlc.narg(state)::text =
       CASE WHEN i.status <> 'posted' THEN i.status
            WHEN coalesce(p.paid, 0) + coalesce(c.credited, 0) >= i.receivable THEN 'paid' ELSE 'open' END)
ORDER BY i.invoice_date DESC, i.id DESC
LIMIT 500;

-- name: LockInvoices :many
SELECT * FROM sales.invoices
WHERE company_id = @company_id AND id = ANY(@ids::uuid[])
ORDER BY id
FOR UPDATE;

-- name: PaidByInvoice :many
-- Pelunasan dan nota kredit yang sudah mengurangi sisa faktur.
SELECT x.invoice_id, sum(x.amount)::numeric AS paid
FROM (
  SELECT a.invoice_id, a.amount
  FROM sales.receipt_allocations a
  JOIN sales.receipts r ON r.id = a.receipt_id
  WHERE a.company_id = @company_id AND a.invoice_id = ANY(@ids::uuid[]) AND r.status = 'posted'
  UNION ALL
  SELECT n.invoice_id, n.applied
  FROM sales.credit_notes n
  WHERE n.company_id = @company_id AND n.invoice_id = ANY(@ids::uuid[]) AND n.status = 'posted'
) x
GROUP BY x.invoice_id;

-- name: CollectedByInvoice :many
-- PPN dipungut pelanggan yang sudah dicatat per faktur (pelunasan yang masih berlaku).
SELECT a.invoice_id, sum(a.vat_collected)::numeric AS collected
FROM sales.receipt_allocations a
JOIN sales.receipts r ON r.id = a.receipt_id
WHERE a.company_id = @company_id AND a.invoice_id = ANY(@ids::uuid[]) AND r.status = 'posted' AND a.vat_collected > 0
GROUP BY a.invoice_id;

-- name: OpenInvoices :many
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid, coalesce(c.credited, 0)::numeric AS credited,
       coalesce(p.vat_collected, 0)::numeric AS vat_collected_total
FROM sales.invoices i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid, sum(a.vat_collected) AS vat_collected
  FROM sales.receipt_allocations a
  JOIN sales.receipts r ON r.id = a.receipt_id
  WHERE a.invoice_id = i.id AND r.status = 'posted'
) p ON true
LEFT JOIN LATERAL (
  SELECT sum(n.applied) AS credited FROM sales.credit_notes n WHERE n.invoice_id = i.id AND n.status = 'posted'
) c ON true
WHERE i.company_id = @company_id AND i.status = 'posted'
  AND (sqlc.narg(customer_id)::uuid IS NULL OR i.customer_id = sqlc.narg(customer_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR i.branch_id = ANY(@branches::uuid[]))
  AND coalesce(p.paid, 0) + coalesce(c.credited, 0) < i.receivable
ORDER BY i.due_date, i.number;

-- name: AgingInvoices :many
-- Faktur yang belum lunas per tanggal @as_of: pelunasan dan nota kredit yang dihitung hanya
-- yang bertanggal sampai tanggal itu (AR-06).
SELECT i.id, i.number, i.customer_id, i.invoice_date, i.due_date, i.receivable,
  (coalesce((SELECT sum(a.amount) FROM sales.receipt_allocations a
             JOIN sales.receipts r ON r.id = a.receipt_id
             WHERE a.invoice_id = i.id AND r.status = 'posted' AND r.receipt_date <= @as_of), 0)
   + coalesce((SELECT sum(n.applied) FROM sales.credit_notes n
               WHERE n.invoice_id = i.id AND n.status = 'posted' AND n.note_date <= @as_of), 0))::numeric AS paid
FROM sales.invoices i
WHERE i.company_id = @company_id AND i.status = 'posted' AND i.invoice_date <= @as_of
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR i.branch_id = ANY(@branches::uuid[]))
ORDER BY i.due_date, i.number;

-- Pelunasan -------------------------------------------------------------------------

-- name: InsertReceipt :exec
INSERT INTO sales.receipts
  (id, company_id, number, customer_id, receipt_date, cash_account_id, amount, memo, journal_entry_id, created_by,
   kind, advance, from_advance, vat_collected)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: LockAdvance :exec
-- Mengunci saldo uang muka satu kontak sampai transaksi selesai (kunci dihitung di Go).
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: CustomerAdvanceBalance :one
-- Saldo uang muka pelanggan: uang muka diterima − dipakai/dikembalikan + kelebihan nota kredit.
SELECT (coalesce((SELECT sum(r.advance - r.from_advance) FROM sales.receipts r
                  WHERE r.company_id = @company_id AND r.customer_id = @customer_id AND r.status = 'posted'), 0)
      + coalesce((SELECT sum(n.total - n.applied) FROM sales.credit_notes n
                  WHERE n.company_id = @company_id AND n.customer_id = @customer_id AND n.status = 'posted'), 0))::numeric AS balance;

-- name: CustomerAdvanceBalances :many
-- Saldo uang muka semua pelanggan yang bersaldo.
SELECT x.customer_id, sum(x.amount)::numeric AS balance
FROM (
  SELECT r.customer_id, r.advance - r.from_advance AS amount FROM sales.receipts r
  WHERE r.company_id = @company_id AND r.status = 'posted' AND (r.advance > 0 OR r.from_advance > 0)
  UNION ALL
  SELECT n.customer_id, n.total - n.applied FROM sales.credit_notes n
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.total > n.applied
) x
GROUP BY x.customer_id
HAVING sum(x.amount) <> 0;

-- name: InsertAllocation :exec
INSERT INTO sales.receipt_allocations (id, company_id, receipt_id, invoice_id, amount, withholding_code, withholding, vat_collected)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetReceipt :one
SELECT r.*, u.name AS created_by_name, vu.name AS voided_by_name
FROM sales.receipts r
LEFT JOIN platform.users u ON u.id = r.created_by
LEFT JOIN platform.users vu ON vu.id = r.voided_by
WHERE r.company_id = $1 AND r.id = $2;

-- name: GetReceiptForUpdate :one
SELECT * FROM sales.receipts WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: ReceiptAllocations :many
SELECT a.*, i.number AS invoice_number, i.receivable AS invoice_total
FROM sales.receipt_allocations a
JOIN sales.invoices i ON i.id = a.invoice_id
WHERE a.company_id = $1 AND a.receipt_id = $2
ORDER BY i.number;

-- name: InvoiceReceipts :many
SELECT r.id, r.number, r.receipt_date, r.status, a.amount, a.withholding, a.withholding_code, a.vat_collected
FROM sales.receipt_allocations a
JOIN sales.receipts r ON r.id = a.receipt_id
WHERE a.company_id = $1 AND a.invoice_id = $2
ORDER BY r.receipt_date, r.number;

-- name: MarkReceiptVoid :execrows
UPDATE sales.receipts
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: ListReceipts :many
SELECT r.* FROM sales.receipts r
WHERE r.company_id = @company_id AND r.receipt_date >= @from_date AND r.receipt_date <= @to_date
  AND (sqlc.narg(customer_id)::uuid IS NULL OR r.customer_id = sqlc.narg(customer_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR EXISTS (
       SELECT 1 FROM sales.receipt_allocations a JOIN sales.invoices i ON i.id = a.invoice_id
       WHERE a.receipt_id = r.id AND i.branch_id = ANY(@branches::uuid[])))
ORDER BY r.receipt_date DESC, r.id DESC
LIMIT 500;

-- Pajak ---------------------------------------------------------------------------

-- name: VATByMonth :many
-- PPN keluaran per masa dari faktur yang diposting (TAX-06), dikurangi PPN retur penjualan
-- (nota kredit) di masa returnya. collected: bagian yang dipungut pelanggan pemungut (kode
-- transaksi 02/03): faktur yang pelanggannya pemungut saat diposting, kecuali kodenya diubah
-- staf pajak (@not_collected_ids), ditambah faktur yang kodenya diubah ke 02/03 (@collected_ids).
SELECT x.month::date AS month, count(*) AS invoices,
       coalesce(sum(x.dpp), 0)::numeric AS dpp, coalesce(sum(x.vat), 0)::numeric AS vat,
       coalesce(sum(x.vat) FILTER (WHERE x.collected), 0)::numeric AS collected
FROM (
  SELECT date_trunc('month', i.invoice_date) AS month, i.dpp, i.vat,
         (i.id = ANY(coalesce(@collected_ids::uuid[], '{}')) OR (i.vat_collector <> '' AND NOT i.id = ANY(coalesce(@not_collected_ids::uuid[], '{}')))) AS collected
  FROM sales.invoices i
  WHERE i.company_id = @company_id AND i.status = 'posted' AND i.vat > 0
    AND i.invoice_date >= @from_date AND i.invoice_date < @to_date
  UNION ALL
  SELECT date_trunc('month', n.note_date), -n.dpp, -n.vat,
         coalesce(ni.id = ANY(coalesce(@collected_ids::uuid[], '{}')) OR (ni.vat_collector <> '' AND NOT ni.id = ANY(coalesce(@not_collected_ids::uuid[], '{}'))), false)
  FROM sales.credit_notes n
  LEFT JOIN sales.invoices ni ON ni.id = n.invoice_id
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.vat > 0
    AND n.note_date >= @from_date AND n.note_date < @to_date
  UNION ALL
  SELECT date_trunc('month', d.sale_date), d.dpp, d.vat, false
  FROM sales.daily_sales d
  WHERE d.company_id = @company_id AND d.status = 'posted' AND d.vat > 0
    AND d.sale_date >= @from_date AND d.sale_date < @to_date
) x
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

-- Laporan -------------------------------------------------------------------------

-- name: SalesByCustomer :many
-- Penjualan bersih: faktur dikurangi nota kredit di periode yang sama.
SELECT x.customer_id, count(*) FILTER (WHERE x.invoice) AS invoices, sum(x.dpp)::numeric AS dpp,
       sum(x.vat)::numeric AS vat, sum(x.total)::numeric AS total
FROM (
  SELECT true AS invoice, i.customer_id, i.dpp, i.vat, i.total
  FROM sales.invoices i
  WHERE i.company_id = @company_id AND i.status = 'posted' AND i.kind <> 'saldo_awal'
    AND i.invoice_date >= @from_date AND i.invoice_date <= @to_date
  UNION ALL
  SELECT false, n.customer_id, -n.dpp, -n.vat, -n.total
  FROM sales.credit_notes n
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.note_date >= @from_date AND n.note_date <= @to_date
) x
GROUP BY x.customer_id
ORDER BY sum(x.dpp) DESC;

-- name: SalesByProduct :many
-- Penjualan bersih per produk; kuantitas hanya berkurang oleh barang yang diretur ke stok.
SELECT x.product_id, count(DISTINCT x.invoice_id) AS invoices, sum(x.qty)::numeric AS qty,
       sum(x.net)::numeric AS dpp, sum(x.vat)::numeric AS vat
FROM (
  SELECT l.product_id, i.id AS invoice_id, l.qty, l.net, l.vat
  FROM sales.invoice_lines l
  JOIN sales.invoices i ON i.id = l.invoice_id
  WHERE i.company_id = @company_id AND i.status = 'posted' AND i.kind <> 'saldo_awal'
    AND i.invoice_date >= @from_date AND i.invoice_date <= @to_date
  UNION ALL
  SELECT l.product_id, NULL::uuid, CASE WHEN l.restock THEN -l.qty ELSE 0 END, -l.net, -l.vat
  FROM sales.credit_note_lines l
  JOIN sales.credit_notes n ON n.id = l.credit_note_id
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.note_date >= @from_date AND n.note_date <= @to_date
  UNION ALL
  SELECT l.product_id, NULL::uuid, l.qty, l.net, l.vat
  FROM sales.daily_sale_lines l
  JOIN sales.daily_sales d ON d.id = l.daily_sale_id
  WHERE d.company_id = @company_id AND d.status = 'posted' AND d.sale_date >= @from_date AND d.sale_date <= @to_date
) x
GROUP BY x.product_id
ORDER BY sum(x.net) DESC;

-- name: CustomerCardOpening :one
-- Saldo piutang pelanggan sebelum @from_date dari dokumen yang tidak di-void.
SELECT (coalesce((SELECT sum(i.receivable) FROM sales.invoices i
                  WHERE i.company_id = @company_id AND i.customer_id = @customer_id AND i.status = 'posted'
                    AND i.invoice_date < @from_date), 0)
      - coalesce((SELECT sum(a.amount) FROM sales.receipt_allocations a
                  JOIN sales.receipts r ON r.id = a.receipt_id
                  WHERE r.company_id = @company_id AND r.customer_id = @customer_id AND r.status = 'posted'
                    AND r.receipt_date < @from_date), 0)
      - coalesce((SELECT sum(n.applied) FROM sales.credit_notes n
                  WHERE n.company_id = @company_id AND n.customer_id = @customer_id AND n.status = 'posted'
                    AND n.note_date < @from_date), 0))::numeric AS opening;

-- name: CustomerCardLines :many
SELECT x.kind, x.id, x.number, x.doc_date, x.memo, x.debit, x.credit, x.withholding
FROM (
  SELECT 'faktur'::text AS kind, i.id, coalesce(i.number, '') AS number, i.invoice_date AS doc_date, i.memo,
         coalesce(i.receivable, 0)::numeric AS debit, 0::numeric AS credit, 0::numeric AS withholding
  FROM sales.invoices i
  WHERE i.company_id = @company_id AND i.customer_id = @customer_id AND i.status = 'posted'
    AND i.invoice_date >= @from_date AND i.invoice_date <= @to_date
  UNION ALL
  SELECT 'pelunasan'::text, r.id, r.number, r.receipt_date, r.memo,
         0::numeric, sum(a.amount)::numeric, sum(a.withholding)::numeric
  FROM sales.receipts r
  JOIN sales.receipt_allocations a ON a.receipt_id = r.id
  WHERE r.company_id = @company_id AND r.customer_id = @customer_id AND r.status = 'posted'
    AND r.receipt_date >= @from_date AND r.receipt_date <= @to_date
  GROUP BY r.id
  UNION ALL
  SELECT 'nota_kredit'::text, n.id, n.number, n.note_date, n.memo, 0::numeric, n.applied, 0::numeric
  FROM sales.credit_notes n
  WHERE n.company_id = @company_id AND n.customer_id = @customer_id AND n.status = 'posted' AND n.applied > 0
    AND n.note_date >= @from_date AND n.note_date <= @to_date
) x
ORDER BY x.doc_date, x.kind, x.number;

-- name: RevenueByAccount :many
SELECT x.account_id, sum(x.net)::numeric AS amount
FROM (
  SELECT l.account_id, l.net
  FROM sales.invoice_lines l
  JOIN sales.invoices i ON i.id = l.invoice_id
  WHERE i.company_id = @company_id AND i.customer_id = @customer_id AND i.status = 'posted' AND i.kind <> 'saldo_awal'
    AND i.invoice_date >= @from_date AND i.invoice_date <= @to_date
  UNION ALL
  SELECT l.account_id, -l.net
  FROM sales.credit_note_lines l
  JOIN sales.credit_notes n ON n.id = l.credit_note_id
  WHERE n.company_id = @company_id AND n.customer_id = @customer_id AND n.status = 'posted'
    AND n.note_date >= @from_date AND n.note_date <= @to_date
) x
GROUP BY x.account_id;

-- Nota kredit dan retur penjualan (AR-05) -------------------------------------------

-- name: InsertCreditNote :exec
INSERT INTO sales.credit_notes
  (id, company_id, number, customer_id, invoice_id, note_date, memo, subtotal, discount, dpp, other_dpp, vat,
   total, applied, journal_entry_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

-- name: InsertCreditNoteLine :exec
INSERT INTO sales.credit_note_lines
  (id, company_id, credit_note_id, line_no, invoice_line_id, description, account_id, product_id, qty, unit_price,
   discount_pct, discount, net, tax_code, other_dpp, vat, restock)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: GetCreditNote :one
SELECT n.*, i.number AS invoice_number, cu.name AS created_by_name, vu.name AS voided_by_name
FROM sales.credit_notes n
JOIN sales.invoices i ON i.id = n.invoice_id
LEFT JOIN platform.users cu ON cu.id = n.created_by
LEFT JOIN platform.users vu ON vu.id = n.voided_by
WHERE n.company_id = $1 AND n.id = $2;

-- name: GetCreditNoteForUpdate :one
SELECT * FROM sales.credit_notes WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: CreditNoteLines :many
SELECT * FROM sales.credit_note_lines WHERE company_id = $1 AND credit_note_id = $2 ORDER BY line_no;

-- name: ListCreditNotes :many
SELECT n.*, i.number AS invoice_number
FROM sales.credit_notes n
JOIN sales.invoices i ON i.id = n.invoice_id
WHERE n.company_id = @company_id AND n.note_date >= @from_date AND n.note_date <= @to_date
  AND (sqlc.narg(customer_id)::uuid IS NULL OR n.customer_id = sqlc.narg(customer_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR i.branch_id = ANY(@branches::uuid[]))
ORDER BY n.note_date DESC, n.id DESC
LIMIT 500;

-- name: InvoiceCreditNotes :many
SELECT id, number, note_date, status, total, applied
FROM sales.credit_notes
WHERE company_id = $1 AND invoice_id = $2
ORDER BY note_date, number;

-- name: CreditedByLine :many
-- Nilai yang sudah dikreditkan dan barang yang sudah kembali ke stok per baris faktur.
SELECT l.invoice_line_id, sum(l.net)::numeric AS net,
       coalesce(sum(l.qty) FILTER (WHERE l.restock), 0)::numeric AS restocked
FROM sales.credit_note_lines l
JOIN sales.credit_notes n ON n.id = l.credit_note_id
WHERE n.company_id = @company_id AND n.invoice_id = @invoice_id AND n.status = 'posted'
GROUP BY l.invoice_line_id;

-- name: MarkCreditNoteVoid :execrows
UPDATE sales.credit_notes
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- Rekap penjualan harian retail (IND-07, TAX-12) ------------------------------------

-- name: InsertDailySale :exec
INSERT INTO sales.daily_sales
  (id, company_id, number, sale_date, store, memo, dpp, other_dpp, vat, total, journal_entry_id, created_by, branch_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: InsertDailySaleLine :exec
INSERT INTO sales.daily_sale_lines
  (id, company_id, daily_sale_id, line_no, product_id, description, account_id, qty, amount, tax_code, net, other_dpp, vat)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: InsertDailySalePayment :exec
INSERT INTO sales.daily_sale_payments (id, company_id, daily_sale_id, account_id, amount)
VALUES ($1, $2, $3, $4, $5);

-- name: GetDailySale :one
SELECT d.*, cu.name AS created_by_name, vu.name AS voided_by_name
FROM sales.daily_sales d
LEFT JOIN platform.users cu ON cu.id = d.created_by
LEFT JOIN platform.users vu ON vu.id = d.voided_by
WHERE d.company_id = $1 AND d.id = $2;

-- name: GetDailySaleForUpdate :one
SELECT * FROM sales.daily_sales WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: DailySaleOn :one
-- Rekap aktif untuk satu tanggal dan toko.
SELECT id, number FROM sales.daily_sales
WHERE company_id = $1 AND sale_date = $2 AND store = $3 AND status = 'posted';

-- name: DailySaleLines :many
SELECT * FROM sales.daily_sale_lines WHERE company_id = $1 AND daily_sale_id = $2 ORDER BY line_no;

-- name: DailySalePayments :many
SELECT * FROM sales.daily_sale_payments WHERE company_id = $1 AND daily_sale_id = $2 ORDER BY amount DESC;

-- name: ListDailySales :many
SELECT * FROM sales.daily_sales
WHERE company_id = @company_id AND sale_date >= @from_date AND sale_date <= @to_date
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR branch_id = ANY(@branches::uuid[]))
ORDER BY sale_date DESC, store, id DESC
LIMIT 1000;

-- name: DailyTotals :one
SELECT count(*) AS docs, coalesce(sum(dpp), 0)::numeric AS dpp, coalesce(sum(vat), 0)::numeric AS vat,
       coalesce(sum(total), 0)::numeric AS total
FROM sales.daily_sales
WHERE company_id = @company_id AND status = 'posted' AND sale_date >= @from_date AND sale_date <= @to_date;

-- name: MarkDailySaleVoid :execrows
UPDATE sales.daily_sales
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';
