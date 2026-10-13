-- Tagihan ---------------------------------------------------------------------------

-- name: InsertBill :exec
INSERT INTO purchase.bills
  (id, company_id, supplier_id, supplier_invoice_no, bill_date, due_date, memo, subtotal, discount, dpp,
   other_dpp, vat, withholding_code, withholding, total, payable, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: UpdateDraftBill :execrows
UPDATE purchase.bills
SET supplier_id = @supplier_id, supplier_invoice_no = @supplier_invoice_no, bill_date = @bill_date,
    due_date = @due_date, memo = @memo, subtotal = @subtotal, discount = @discount, dpp = @dpp,
    other_dpp = @other_dpp, vat = @vat, withholding_code = sqlc.narg(withholding_code),
    withholding = @withholding, total = @total, payable = @payable, version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'draft' AND version = @version;

-- name: DeleteBillLines :exec
DELETE FROM purchase.bill_lines WHERE company_id = $1 AND bill_id = $2;

-- name: InsertBillLine :exec
INSERT INTO purchase.bill_lines
  (id, company_id, bill_id, line_no, description, account_id, qty, unit_price, discount_pct,
   discount, net, tax_code, other_dpp, vat)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetBill :one
SELECT i.*, cu.name AS created_by_name, pu.name AS posted_by_name, vu.name AS voided_by_name
FROM purchase.bills i
LEFT JOIN platform.users cu ON cu.id = i.created_by
LEFT JOIN platform.users pu ON pu.id = i.posted_by
LEFT JOIN platform.users vu ON vu.id = i.voided_by
WHERE i.company_id = $1 AND i.id = $2;

-- name: GetBillForUpdate :one
SELECT * FROM purchase.bills WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: BillLines :many
SELECT * FROM purchase.bill_lines WHERE company_id = $1 AND bill_id = $2 ORDER BY line_no;

-- name: MarkBillPosted :execrows
UPDATE purchase.bills
SET status = 'posted', number = $3, journal_entry_id = $4, posted_at = now(), posted_by = $5, version = version + 1
WHERE company_id = $1 AND id = $2 AND status = 'draft';

-- name: MarkBillVoid :execrows
UPDATE purchase.bills
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4, version = version + 1
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: DeleteDraftBill :execrows
DELETE FROM purchase.bills WHERE company_id = $1 AND id = $2 AND status = 'draft';

-- name: ListBills :many
-- state: draft, open (diposting, belum lunas), paid, atau void.
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid
FROM purchase.bills i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid
  FROM purchase.payment_allocations a
  JOIN purchase.payments r ON r.id = a.payment_id
  WHERE a.bill_id = i.id AND r.status = 'posted'
) p ON true
WHERE i.company_id = @company_id
  AND i.bill_date >= @from_date AND i.bill_date <= @to_date
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR i.supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (sqlc.narg(search)::text IS NULL OR i.number ILIKE '%' || sqlc.narg(search)::text || '%'
       OR i.memo ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(state)::text IS NULL OR sqlc.narg(state)::text =
       CASE WHEN i.status <> 'posted' THEN i.status
            WHEN coalesce(p.paid, 0) >= i.payable THEN 'paid' ELSE 'open' END)
ORDER BY i.bill_date DESC, i.id DESC
LIMIT 500;

-- name: LockBills :many
SELECT * FROM purchase.bills
WHERE company_id = @company_id AND id = ANY(@ids::uuid[])
ORDER BY id
FOR UPDATE;

-- name: PaidByBill :many
SELECT a.bill_id, sum(a.amount)::numeric AS paid
FROM purchase.payment_allocations a
JOIN purchase.payments r ON r.id = a.payment_id
WHERE a.company_id = @company_id AND a.bill_id = ANY(@ids::uuid[]) AND r.status = 'posted'
GROUP BY a.bill_id;

-- name: OpenBills :many
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid
FROM purchase.bills i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid
  FROM purchase.payment_allocations a
  JOIN purchase.payments r ON r.id = a.payment_id
  WHERE a.bill_id = i.id AND r.status = 'posted'
) p ON true
WHERE i.company_id = @company_id AND i.status = 'posted'
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR i.supplier_id = sqlc.narg(supplier_id)::uuid)
  AND coalesce(p.paid, 0) < i.payable
ORDER BY i.due_date, i.number;

-- name: AgingBills :many
-- Tagihan yang belum lunas per tanggal @as_of: pembayaran yang dihitung hanya yang bertanggal
-- sampai tanggal itu (AP-06).
SELECT i.id, i.number, i.supplier_id, i.bill_date, i.due_date, i.payable,
  coalesce((SELECT sum(a.amount) FROM purchase.payment_allocations a
            JOIN purchase.payments r ON r.id = a.payment_id
            WHERE a.bill_id = i.id AND r.status = 'posted' AND r.payment_date <= @as_of), 0)::numeric AS paid
FROM purchase.bills i
WHERE i.company_id = @company_id AND i.status = 'posted' AND i.bill_date <= @as_of
ORDER BY i.due_date, i.number;

-- Pembayaran -------------------------------------------------------------------------

-- name: InsertPayment :exec
INSERT INTO purchase.payments
  (id, company_id, number, supplier_id, payment_date, cash_account_id, amount, memo, journal_entry_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertAllocation :exec
INSERT INTO purchase.payment_allocations (id, company_id, payment_id, bill_id, amount)
VALUES ($1, $2, $3, $4, $5);

-- name: GetPayment :one
SELECT r.*, u.name AS created_by_name, vu.name AS voided_by_name
FROM purchase.payments r
LEFT JOIN platform.users u ON u.id = r.created_by
LEFT JOIN platform.users vu ON vu.id = r.voided_by
WHERE r.company_id = $1 AND r.id = $2;

-- name: GetPaymentForUpdate :one
SELECT * FROM purchase.payments WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: PaymentAllocations :many
SELECT a.*, i.number AS bill_number, i.payable AS bill_payable
FROM purchase.payment_allocations a
JOIN purchase.bills i ON i.id = a.bill_id
WHERE a.company_id = $1 AND a.payment_id = $2
ORDER BY i.number;

-- name: BillPayments :many
SELECT r.id, r.number, r.payment_date, r.status, a.amount
FROM purchase.payment_allocations a
JOIN purchase.payments r ON r.id = a.payment_id
WHERE a.company_id = $1 AND a.bill_id = $2
ORDER BY r.payment_date, r.number;

-- name: MarkPaymentVoid :execrows
UPDATE purchase.payments
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- name: ListPayments :many
SELECT * FROM purchase.payments
WHERE company_id = @company_id AND payment_date >= @from_date AND payment_date <= @to_date
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR supplier_id = sqlc.narg(supplier_id)::uuid)
ORDER BY payment_date DESC, id DESC
LIMIT 500;

-- name: DuplicateSupplierInvoice :many
-- Tagihan lain dari pemasok yang sama dengan nomor faktur pemasok yang sama (peringatan, bukan penolakan).
SELECT id, number, status FROM purchase.bills
WHERE company_id = @company_id AND supplier_id = @supplier_id AND id <> @id
  AND supplier_invoice_no <> '' AND lower(supplier_invoice_no) = lower(@supplier_invoice_no::text)
  AND status <> 'void';

-- Pajak ---------------------------------------------------------------------------

-- name: VATByMonth :many
-- PPN masukan per masa dari tagihan yang diposting (TAX-06).
SELECT date_trunc('month', bill_date)::date AS month, count(*) AS bills,
       coalesce(sum(dpp), 0)::numeric AS dpp, coalesce(sum(vat), 0)::numeric AS vat
FROM purchase.bills
WHERE company_id = @company_id AND status = 'posted' AND vat > 0
  AND bill_date >= @from_date AND bill_date < @to_date
GROUP BY 1
ORDER BY 1;

-- name: WithheldFromSuppliers :many
-- PPh yang kita potong dari pemasok; dasar bukti potong dan SPT Masa PPh Unifikasi.
SELECT id, number, bill_date, supplier_id, supplier_invoice_no, dpp, withholding_code, withholding
FROM purchase.bills
WHERE company_id = @company_id AND status = 'posted' AND withholding > 0
  AND bill_date >= @from_date AND bill_date < @to_date
ORDER BY bill_date, number;
