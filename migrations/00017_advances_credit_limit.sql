-- +goose Up
-- Uang muka pelanggan dan pemasok di luar proyek (AR-04, AP-02), limit kredit dan kontak
-- penagihan (AR-01), dan cerminan pelunasan/pembayaran antar-PT (ME-03).

-- Pelunasan (BKM) dan pembayaran (BKK) bisa:
--   advance      : menerima/membayar uang muka (kelebihan di luar faktur atau tagihan),
--   from_advance : melunasi faktur/tagihan dengan saldo uang muka, atau mengembalikan uang muka
--                  (kind = 'pengembalian'), sehingga uang yang berpindah (amount) bisa nol.
ALTER TABLE sales.receipts
  DROP CONSTRAINT receipts_amount_check,
  ALTER COLUMN cash_account_id DROP NOT NULL,
  ADD COLUMN kind TEXT NOT NULL DEFAULT 'pelunasan' CHECK (kind IN ('pelunasan', 'pengembalian')),
  ADD COLUMN advance NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (advance >= 0),
  ADD COLUMN from_advance NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (from_advance >= 0),
  ADD CONSTRAINT receipts_amount_check CHECK (amount >= 0 AND amount + from_advance > 0),
  ADD CONSTRAINT receipts_cash_check CHECK (amount = 0 OR cash_account_id IS NOT NULL),
  ADD CONSTRAINT receipts_refund_check CHECK (kind = 'pelunasan' OR (advance = 0 AND amount = from_advance));

ALTER TABLE purchase.payments
  DROP CONSTRAINT payments_amount_check,
  ALTER COLUMN cash_account_id DROP NOT NULL,
  ADD COLUMN kind TEXT NOT NULL DEFAULT 'pembayaran' CHECK (kind IN ('pembayaran', 'pengembalian')),
  ADD COLUMN advance NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (advance >= 0),
  ADD COLUMN from_advance NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (from_advance >= 0),
  ADD CONSTRAINT payments_amount_check CHECK (amount >= 0 AND amount + from_advance > 0),
  ADD CONSTRAINT payments_cash_check CHECK (amount = 0 OR cash_account_id IS NOT NULL),
  ADD CONSTRAINT payments_refund_check CHECK (kind = 'pembayaran' OR (advance = 0 AND amount = from_advance));

CREATE INDEX receipts_customer_idx ON sales.receipts (company_id, customer_id) WHERE status = 'posted';
CREATE INDEX payments_supplier_idx ON purchase.payments (company_id, supplier_id) WHERE status = 'posted';

-- AR-01: limit kredit (kosong = tanpa limit) dan kontak penagihan. Rekening kas untuk
-- pelunasan antar-PT yang dicerminkan.
ALTER TABLE core.contacts
  ADD COLUMN credit_limit NUMERIC(19,4) CHECK (credit_limit IS NULL OR credit_limit >= 0),
  ADD COLUMN billing_contact TEXT NOT NULL DEFAULT '',
  ADD COLUMN ic_cash_account_id UUID,
  ADD FOREIGN KEY (company_id, ic_cash_account_id) REFERENCES cash.accounts (company_id, account_id);

ALTER TABLE consol.ic_links
  DROP CONSTRAINT ic_links_source_type_check,
  DROP CONSTRAINT ic_links_target_type_check,
  ADD CONSTRAINT ic_links_source_type_check CHECK (source_type IN
    ('sales.invoice', 'purchase.bill', 'sales.credit_note', 'purchase.debit_note', 'sales.receipt', 'purchase.payment')),
  ADD CONSTRAINT ic_links_target_type_check CHECK (target_type IN
    ('sales.invoice', 'purchase.bill', 'sales.credit_note', 'purchase.debit_note', 'sales.receipt', 'purchase.payment'));

-- +goose Down
ALTER TABLE consol.ic_links
  DROP CONSTRAINT ic_links_source_type_check,
  DROP CONSTRAINT ic_links_target_type_check,
  ADD CONSTRAINT ic_links_source_type_check CHECK (source_type IN ('sales.invoice', 'purchase.bill', 'sales.credit_note', 'purchase.debit_note')),
  ADD CONSTRAINT ic_links_target_type_check CHECK (target_type IN ('sales.invoice', 'purchase.bill', 'sales.credit_note', 'purchase.debit_note'));
ALTER TABLE core.contacts DROP COLUMN ic_cash_account_id, DROP COLUMN billing_contact, DROP COLUMN credit_limit;
DROP INDEX purchase.payments_supplier_idx;
DROP INDEX sales.receipts_customer_idx;
ALTER TABLE purchase.payments
  DROP CONSTRAINT payments_refund_check, DROP CONSTRAINT payments_cash_check, DROP CONSTRAINT payments_amount_check,
  DROP COLUMN from_advance, DROP COLUMN advance, DROP COLUMN kind,
  ALTER COLUMN cash_account_id SET NOT NULL,
  ADD CONSTRAINT payments_amount_check CHECK (amount > 0);
ALTER TABLE sales.receipts
  DROP CONSTRAINT receipts_refund_check, DROP CONSTRAINT receipts_cash_check, DROP CONSTRAINT receipts_amount_check,
  DROP COLUMN from_advance, DROP COLUMN advance, DROP COLUMN kind,
  ALTER COLUMN cash_account_id SET NOT NULL,
  ADD CONSTRAINT receipts_amount_check CHECK (amount > 0);
