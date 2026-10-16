-- Tagihan ---------------------------------------------------------------------------

-- name: InsertBill :exec
INSERT INTO purchase.bills
  (id, company_id, supplier_id, supplier_invoice_no, bill_date, due_date, memo, subtotal, discount, dpp,
   other_dpp, vat, withholding_code, withholding, total, payable, created_by, opening, branch_id, department_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20);

-- name: UpdateDraftBill :execrows
UPDATE purchase.bills
SET supplier_id = @supplier_id, supplier_invoice_no = @supplier_invoice_no, bill_date = @bill_date,
    due_date = @due_date, memo = @memo, subtotal = @subtotal, discount = @discount, dpp = @dpp,
    other_dpp = @other_dpp, vat = @vat, withholding_code = sqlc.narg(withholding_code),
    withholding = @withholding, total = @total, payable = @payable, branch_id = sqlc.narg(branch_id),
    department_id = sqlc.narg(department_id), version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'draft' AND version = @version;

-- name: DeleteBillLines :exec
DELETE FROM purchase.bill_lines WHERE company_id = $1 AND bill_id = $2;

-- name: InsertBillLine :exec
INSERT INTO purchase.bill_lines
  (id, company_id, bill_id, line_no, description, account_id, qty, unit_price, discount_pct,
   discount, net, tax_code, other_dpp, vat, product_id, project_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

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
-- state: draft, open (diposting, belum lunas), paid, atau void. Nota debit ikut mengurangi sisa.
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid, coalesce(d.debited, 0)::numeric AS debited
FROM purchase.bills i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid
  FROM purchase.payment_allocations a
  JOIN purchase.payments r ON r.id = a.payment_id
  WHERE a.bill_id = i.id AND r.status = 'posted'
) p ON true
LEFT JOIN LATERAL (
  SELECT sum(n.applied) AS debited FROM purchase.debit_notes n WHERE n.bill_id = i.id AND n.status = 'posted'
) d ON true
WHERE i.company_id = @company_id
  AND i.bill_date >= @from_date AND i.bill_date <= @to_date
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR i.supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR i.branch_id = ANY(@branches::uuid[]))
  AND (sqlc.narg(search)::text IS NULL OR i.number ILIKE '%' || sqlc.narg(search)::text || '%'
       OR i.memo ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(state)::text IS NULL OR sqlc.narg(state)::text =
       CASE WHEN i.status <> 'posted' THEN i.status
            WHEN coalesce(p.paid, 0) + coalesce(d.debited, 0) >= i.payable THEN 'paid' ELSE 'open' END)
ORDER BY i.bill_date DESC, i.id DESC
LIMIT 500;

-- name: LockBills :many
SELECT * FROM purchase.bills
WHERE company_id = @company_id AND id = ANY(@ids::uuid[])
ORDER BY id
FOR UPDATE;

-- name: PaidByBill :many
-- Pembayaran dan nota debit yang sudah mengurangi sisa tagihan.
SELECT x.bill_id, sum(x.amount)::numeric AS paid
FROM (
  SELECT a.bill_id, a.amount
  FROM purchase.payment_allocations a
  JOIN purchase.payments r ON r.id = a.payment_id
  WHERE a.company_id = @company_id AND a.bill_id = ANY(@ids::uuid[]) AND r.status = 'posted'
  UNION ALL
  SELECT n.bill_id, n.applied
  FROM purchase.debit_notes n
  WHERE n.company_id = @company_id AND n.bill_id = ANY(@ids::uuid[]) AND n.status = 'posted'
) x
GROUP BY x.bill_id;

-- name: OpenBills :many
SELECT i.*, coalesce(p.paid, 0)::numeric AS paid, coalesce(d.debited, 0)::numeric AS debited
FROM purchase.bills i
LEFT JOIN LATERAL (
  SELECT sum(a.amount) AS paid
  FROM purchase.payment_allocations a
  JOIN purchase.payments r ON r.id = a.payment_id
  WHERE a.bill_id = i.id AND r.status = 'posted'
) p ON true
LEFT JOIN LATERAL (
  SELECT sum(n.applied) AS debited FROM purchase.debit_notes n WHERE n.bill_id = i.id AND n.status = 'posted'
) d ON true
WHERE i.company_id = @company_id AND i.status = 'posted'
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR i.supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR i.branch_id = ANY(@branches::uuid[]))
  AND coalesce(p.paid, 0) + coalesce(d.debited, 0) < i.payable
ORDER BY i.due_date, i.number;

-- name: AgingBills :many
-- Tagihan yang belum lunas per tanggal @as_of: pembayaran dan nota debit yang dihitung hanya
-- yang bertanggal sampai tanggal itu (AP-06).
SELECT i.id, i.number, i.supplier_id, i.bill_date, i.due_date, i.payable,
  (coalesce((SELECT sum(a.amount) FROM purchase.payment_allocations a
             JOIN purchase.payments r ON r.id = a.payment_id
             WHERE a.bill_id = i.id AND r.status = 'posted' AND r.payment_date <= @as_of), 0)
   + coalesce((SELECT sum(n.applied) FROM purchase.debit_notes n
               WHERE n.bill_id = i.id AND n.status = 'posted' AND n.note_date <= @as_of), 0))::numeric AS paid
FROM purchase.bills i
WHERE i.company_id = @company_id AND i.status = 'posted' AND i.bill_date <= @as_of
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR i.branch_id = ANY(@branches::uuid[]))
ORDER BY i.due_date, i.number;

-- Pembayaran -------------------------------------------------------------------------

-- name: InsertPayment :exec
INSERT INTO purchase.payments
  (id, company_id, number, supplier_id, payment_date, cash_account_id, amount, memo, journal_entry_id, created_by,
   kind, advance, from_advance)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: LockAdvance :exec
-- Mengunci saldo uang muka satu kontak sampai transaksi selesai (kunci dihitung di Go).
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: SupplierAdvanceBalance :one
-- Saldo uang muka pembelian: uang muka dibayar − dipakai/dikembalikan + kelebihan nota debit.
SELECT (coalesce((SELECT sum(p.advance - p.from_advance) FROM purchase.payments p
                  WHERE p.company_id = @company_id AND p.supplier_id = @supplier_id AND p.status = 'posted'), 0)
      + coalesce((SELECT sum(n.payable - n.applied) FROM purchase.debit_notes n
                  WHERE n.company_id = @company_id AND n.supplier_id = @supplier_id AND n.status = 'posted'), 0))::numeric AS balance;

-- name: SupplierAdvanceBalances :many
SELECT x.supplier_id, sum(x.amount)::numeric AS balance
FROM (
  SELECT p.supplier_id, p.advance - p.from_advance AS amount FROM purchase.payments p
  WHERE p.company_id = @company_id AND p.status = 'posted' AND (p.advance > 0 OR p.from_advance > 0)
  UNION ALL
  SELECT n.supplier_id, n.payable - n.applied FROM purchase.debit_notes n
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.payable > n.applied
) x
GROUP BY x.supplier_id
HAVING sum(x.amount) <> 0;

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
SELECT p.* FROM purchase.payments p
WHERE p.company_id = @company_id AND p.payment_date >= @from_date AND p.payment_date <= @to_date
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR p.supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR EXISTS (
       SELECT 1 FROM purchase.payment_allocations a JOIN purchase.bills b ON b.id = a.bill_id
       WHERE a.payment_id = p.id AND b.branch_id = ANY(@branches::uuid[])))
ORDER BY p.payment_date DESC, p.id DESC
LIMIT 500;

-- name: DuplicateSupplierInvoice :many
-- Tagihan lain dari pemasok yang sama dengan nomor faktur pemasok yang sama (peringatan, bukan penolakan).
SELECT id, number, status FROM purchase.bills
WHERE company_id = @company_id AND supplier_id = @supplier_id AND id <> @id
  AND supplier_invoice_no <> '' AND lower(supplier_invoice_no) = lower(@supplier_invoice_no::text)
  AND status <> 'void';

-- Pajak ---------------------------------------------------------------------------

-- name: VATByMonth :many
-- PPN masukan per masa dari tagihan yang diposting (TAX-06), dikurangi PPN retur pembelian
-- (nota debit) di masa returnya.
SELECT x.month::date AS month, count(*) AS bills,
       coalesce(sum(x.dpp), 0)::numeric AS dpp, coalesce(sum(x.vat), 0)::numeric AS vat
FROM (
  SELECT date_trunc('month', b.bill_date) AS month, b.dpp, b.vat
  FROM purchase.bills b
  WHERE b.company_id = @company_id AND b.status = 'posted' AND b.vat > 0
    AND b.bill_date >= @from_date AND b.bill_date < @to_date
  UNION ALL
  SELECT date_trunc('month', n.note_date), -n.dpp, -n.vat
  FROM purchase.debit_notes n
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.vat > 0
    AND n.note_date >= @from_date AND n.note_date < @to_date
) x
GROUP BY 1
ORDER BY 1;

-- name: WithheldFromSuppliers :many
-- PPh yang kita potong dari pemasok; dasar bukti potong dan SPT Masa PPh Unifikasi. Nota debit
-- membatalkan sebagian potongannya dan tampil sebagai baris negatif.
SELECT x.id, x.number, x.bill_date, x.supplier_id, x.supplier_invoice_no, x.dpp, x.withholding_code, x.withholding
FROM (
  SELECT b.id, b.number, b.bill_date, b.supplier_id, b.supplier_invoice_no, b.dpp, b.withholding_code, b.withholding
  FROM purchase.bills b
  WHERE b.company_id = @company_id AND b.status = 'posted' AND b.withholding > 0
    AND b.bill_date >= @from_date AND b.bill_date < @to_date
  UNION ALL
  SELECT n.id, n.number, n.note_date, n.supplier_id, b.supplier_invoice_no, -n.dpp, b.withholding_code, -n.withholding
  FROM purchase.debit_notes n
  JOIN purchase.bills b ON b.id = n.bill_id
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.withholding > 0
    AND n.note_date >= @from_date AND n.note_date < @to_date
) x
ORDER BY x.bill_date, x.number;

-- Laporan -------------------------------------------------------------------------

-- name: PurchasesBySupplier :many
-- Pembelian bersih: tagihan dikurangi nota debit di periode yang sama.
SELECT x.supplier_id, count(*) FILTER (WHERE x.bill) AS bills, sum(x.dpp)::numeric AS dpp, sum(x.vat)::numeric AS vat,
       sum(x.withholding)::numeric AS withholding, sum(x.total)::numeric AS total
FROM (
  SELECT true AS bill, b.supplier_id, b.dpp, b.vat, b.withholding, b.total
  FROM purchase.bills b
  WHERE b.company_id = @company_id AND b.status = 'posted' AND NOT b.opening
    AND b.bill_date >= @from_date AND b.bill_date <= @to_date
  UNION ALL
  SELECT false, n.supplier_id, -n.dpp, -n.vat, -n.withholding, -n.total
  FROM purchase.debit_notes n
  WHERE n.company_id = @company_id AND n.status = 'posted' AND n.note_date >= @from_date AND n.note_date <= @to_date
) x
GROUP BY x.supplier_id
ORDER BY sum(x.dpp) DESC;

-- name: SupplierCardOpening :one
SELECT (coalesce((SELECT sum(b.payable) FROM purchase.bills b
                  WHERE b.company_id = @company_id AND b.supplier_id = @supplier_id AND b.status = 'posted'
                    AND b.bill_date < @from_date), 0)
      - coalesce((SELECT sum(a.amount) FROM purchase.payment_allocations a
                  JOIN purchase.payments p ON p.id = a.payment_id
                  WHERE p.company_id = @company_id AND p.supplier_id = @supplier_id AND p.status = 'posted'
                    AND p.payment_date < @from_date), 0)
      - coalesce((SELECT sum(n.applied) FROM purchase.debit_notes n
                  WHERE n.company_id = @company_id AND n.supplier_id = @supplier_id AND n.status = 'posted'
                    AND n.note_date < @from_date), 0))::numeric AS opening;

-- name: SupplierCardLines :many
SELECT x.kind, x.id, x.number, x.doc_date, x.memo, x.debit, x.credit, x.withholding
FROM (
  SELECT 'tagihan'::text AS kind, b.id, coalesce(b.number, '') AS number, b.bill_date AS doc_date, b.memo,
         0::numeric AS debit, b.payable AS credit, b.withholding
  FROM purchase.bills b
  WHERE b.company_id = @company_id AND b.supplier_id = @supplier_id AND b.status = 'posted'
    AND b.bill_date >= @from_date AND b.bill_date <= @to_date
  UNION ALL
  SELECT 'pembayaran'::text, p.id, p.number, p.payment_date, p.memo, sum(a.amount)::numeric, 0::numeric, 0::numeric
  FROM purchase.payments p
  JOIN purchase.payment_allocations a ON a.payment_id = p.id
  WHERE p.company_id = @company_id AND p.supplier_id = @supplier_id AND p.status = 'posted'
    AND p.payment_date >= @from_date AND p.payment_date <= @to_date
  GROUP BY p.id
  UNION ALL
  SELECT 'nota_debit'::text, n.id, n.number, n.note_date, n.memo, n.applied, 0::numeric, 0::numeric
  FROM purchase.debit_notes n
  WHERE n.company_id = @company_id AND n.supplier_id = @supplier_id AND n.status = 'posted' AND n.applied > 0
    AND n.note_date >= @from_date AND n.note_date <= @to_date
) x
ORDER BY x.doc_date, x.kind DESC, x.number;

-- name: CostByAccount :many
SELECT x.account_id, sum(x.net)::numeric AS amount
FROM (
  SELECT l.account_id, l.net
  FROM purchase.bill_lines l
  JOIN purchase.bills b ON b.id = l.bill_id
  WHERE b.company_id = @company_id AND b.supplier_id = @supplier_id AND b.status = 'posted' AND NOT b.opening
    AND b.bill_date >= @from_date AND b.bill_date <= @to_date
  UNION ALL
  SELECT l.account_id, -l.net
  FROM purchase.debit_note_lines l
  JOIN purchase.debit_notes n ON n.id = l.debit_note_id
  WHERE n.company_id = @company_id AND n.supplier_id = @supplier_id AND n.status = 'posted'
    AND n.note_date >= @from_date AND n.note_date <= @to_date
) x
GROUP BY x.account_id;

-- Nota debit dan retur pembelian (AP-05) --------------------------------------------

-- name: InsertDebitNote :exec
INSERT INTO purchase.debit_notes
  (id, company_id, number, supplier_id, bill_id, note_date, memo, subtotal, discount, dpp, other_dpp, vat,
   withholding, total, payable, applied, journal_entry_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18);

-- name: InsertDebitNoteLine :exec
INSERT INTO purchase.debit_note_lines
  (id, company_id, debit_note_id, line_no, bill_line_id, description, account_id, product_id, project_id, qty,
   unit_price, discount_pct, discount, net, tax_code, other_dpp, vat)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: GetDebitNote :one
SELECT n.*, b.number AS bill_number, b.supplier_invoice_no, cu.name AS created_by_name, vu.name AS voided_by_name
FROM purchase.debit_notes n
JOIN purchase.bills b ON b.id = n.bill_id
LEFT JOIN platform.users cu ON cu.id = n.created_by
LEFT JOIN platform.users vu ON vu.id = n.voided_by
WHERE n.company_id = $1 AND n.id = $2;

-- name: GetDebitNoteForUpdate :one
SELECT * FROM purchase.debit_notes WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: DebitNoteLines :many
SELECT * FROM purchase.debit_note_lines WHERE company_id = $1 AND debit_note_id = $2 ORDER BY line_no;

-- name: ListDebitNotes :many
SELECT n.*, b.number AS bill_number
FROM purchase.debit_notes n
JOIN purchase.bills b ON b.id = n.bill_id
WHERE n.company_id = @company_id AND n.note_date >= @from_date AND n.note_date <= @to_date
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR n.supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (coalesce(cardinality(@branches::uuid[]), 0) = 0 OR b.branch_id = ANY(@branches::uuid[]))
ORDER BY n.note_date DESC, n.id DESC
LIMIT 500;

-- name: BillDebitNotes :many
SELECT id, number, note_date, status, total, applied
FROM purchase.debit_notes
WHERE company_id = $1 AND bill_id = $2
ORDER BY note_date, number;

-- name: DebitedByLine :many
-- Nilai dan kuantitas yang sudah dikembalikan per baris tagihan.
SELECT l.bill_line_id, sum(l.net)::numeric AS net, sum(l.qty)::numeric AS qty
FROM purchase.debit_note_lines l
JOIN purchase.debit_notes n ON n.id = l.debit_note_id
WHERE n.company_id = @company_id AND n.bill_id = @bill_id AND n.status = 'posted'
GROUP BY l.bill_line_id;

-- name: MarkDebitNoteVoid :execrows
UPDATE purchase.debit_notes
SET status = 'void', voided_at = now(), voided_by = $3, void_reason = $4
WHERE company_id = $1 AND id = $2 AND status = 'posted';

-- Data pendukung pajak masukan dan bukti potong (TAX-07, TAX-08) --------------------------

-- name: InputVATBills :many
-- Tagihan ber-PPN masukan di rentang tanggal tagihan, dengan PPN yang sudah dikurangi nota debit.
SELECT b.id, b.number, b.bill_date, b.supplier_id, b.supplier_invoice_no, b.dpp, b.vat,
       coalesce((SELECT sum(n.vat) FROM purchase.debit_notes n WHERE n.bill_id = b.id AND n.status = 'posted'), 0)::numeric AS noted_vat
FROM purchase.bills b
WHERE b.company_id = @company_id AND b.status = 'posted' AND b.vat > 0
  AND b.bill_date >= @from_date AND b.bill_date < @to_date
ORDER BY b.bill_date, b.number;

-- name: DebitNoteVAT :many
-- PPN nota debit per tanggal nota; mengurangi PPN masukan pada masa notanya.
SELECT n.id, n.bill_id, n.note_date, n.vat
FROM purchase.debit_notes n
WHERE n.company_id = @company_id AND n.status = 'posted' AND n.vat > 0
  AND n.note_date >= @from_date AND n.note_date < @to_date;

-- name: BillVATLines :many
-- PPN per baris tagihan sesudah nota debit, untuk jurnal PPN masukan tidak dikreditkan.
SELECT l.account_id, l.project_id,
       (l.vat - coalesce((SELECT sum(dl.vat) FROM purchase.debit_note_lines dl
                          JOIN purchase.debit_notes n ON n.id = dl.debit_note_id
                          WHERE dl.bill_line_id = l.id AND n.status = 'posted'), 0))::numeric AS vat
FROM purchase.bill_lines l
WHERE l.company_id = @company_id AND l.bill_id = @bill_id AND l.vat > 0
ORDER BY l.line_no;

-- name: WithholdingBills :many
-- Tagihan yang memotong PPh di rentang tanggal tagihan, dengan DPP dan PPh nota debitnya.
SELECT b.id, b.number, b.bill_date, b.supplier_id, b.supplier_invoice_no, b.withholding_code, b.dpp, b.withholding,
       coalesce((SELECT sum(n.dpp) FROM purchase.debit_notes n WHERE n.bill_id = b.id AND n.status = 'posted'), 0)::numeric AS noted_dpp,
       coalesce((SELECT sum(n.withholding) FROM purchase.debit_notes n WHERE n.bill_id = b.id AND n.status = 'posted'), 0)::numeric AS noted_withholding
FROM purchase.bills b
WHERE b.company_id = @company_id AND b.status = 'posted' AND b.withholding > 0
  AND b.bill_date >= @from_date AND b.bill_date < @to_date
ORDER BY b.bill_date, b.number;
