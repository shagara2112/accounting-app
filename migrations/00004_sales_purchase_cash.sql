-- +goose Up
-- Tahap 3: kontak, kode pajak, penjualan (AR), pembelian (AP), dan kas-bank (TDD, bagian
-- Desain modul bisnis). Dokumen mengikuti siklus draft → posted → void; jurnalnya hanya
-- dibuat lewat ledger.Post, dan dokumen yang sudah diposting tidak bisa diubah.

-- PT non-PKP tidak boleh menerbitkan faktur ber-PPN (PRD, aturan perpajakan).
ALTER TABLE core.companies ADD COLUMN pkp BOOLEAN NOT NULL DEFAULT true;

-- Data posted tidak boleh berubah selain statusnya menjadi void. Dipakai beberapa tabel dokumen.
-- +goose StatementBegin
CREATE FUNCTION platform.guard_posted_document() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status = 'draft' THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'dokumen yang sudah diposting tidak bisa dihapus' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status = 'draft' OR (OLD.status = 'posted' AND NEW.status = 'void') THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'dokumen yang sudah diposting tidak bisa diubah; gunakan void' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

-- Kontak --------------------------------------------------------------------------

CREATE TABLE core.contacts (
  id                  UUID PRIMARY KEY,
  company_id          UUID NOT NULL REFERENCES core.companies (id),
  name                TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  is_customer         BOOLEAN NOT NULL DEFAULT false,
  is_supplier         BOOLEAN NOT NULL DEFAULT false,
  npwp                TEXT CHECK (npwp ~ '^[0-9]{16}$'),   -- NPWP 16 digit (TAX-05)
  nik                 TEXT CHECK (nik ~ '^[0-9]{16}$'),
  nitku               TEXT CHECK (nitku ~ '^[0-9]{22}$'),  -- NPWP + 6 digit kode tempat usaha
  pkp                 BOOLEAN NOT NULL DEFAULT false,
  address             TEXT NOT NULL DEFAULT '',
  email               TEXT NOT NULL DEFAULT '',
  phone               TEXT NOT NULL DEFAULT '',
  payment_terms_days  INT NOT NULL DEFAULT 30 CHECK (payment_terms_days BETWEEN 0 AND 365),
  bank_name           TEXT NOT NULL DEFAULT '',
  bank_account        TEXT NOT NULL DEFAULT '',
  active              BOOLEAN NOT NULL DEFAULT true,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by          UUID REFERENCES platform.users (id),
  version             INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  CHECK (is_customer OR is_supplier)
);
CREATE INDEX contacts_name_idx ON core.contacts (company_id, lower(name));

-- Kode pajak bertanggal berlaku (TAX-01). Mengubah tarif = menambah baris baru; transaksi
-- lama tetap memakai baris yang berlaku pada tanggalnya.
CREATE SCHEMA IF NOT EXISTS tax;

CREATE TABLE tax.tax_codes (
  code        TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('ppn', 'pph')),
  name        TEXT NOT NULL,
  rate_bp     INT NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000), -- basis poin: 1200 = 12%
  dpp_num     INT NOT NULL DEFAULT 1 CHECK (dpp_num > 0),       -- DPP nilai lain = DPP × num/den
  dpp_den     INT NOT NULL DEFAULT 1 CHECK (dpp_den > 0),
  article     TEXT CHECK (article IN ('23', '4(2)')),           -- hanya PPh
  valid_from  DATE NOT NULL,
  valid_to    DATE,
  PRIMARY KEY (code, valid_from),
  CHECK ((kind = 'pph') = (article IS NOT NULL)),
  CHECK (valid_to IS NULL OR valid_to >= valid_from)
);

INSERT INTO tax.tax_codes (code, kind, name, rate_bp, dpp_num, dpp_den, article, valid_from, valid_to) VALUES
  ('PPN',         'ppn', 'PPN 11%',                                      1100,  1,  1, NULL,   '2022-04-01', '2024-12-31'),
  ('PPN',         'ppn', 'PPN 12% × DPP nilai lain 11/12 (non-mewah)',   1200, 11, 12, NULL,   '2025-01-01', NULL),
  ('PPN-MEWAH',   'ppn', 'PPN 12% × DPP penuh (barang mewah)',           1200,  1,  1, NULL,   '2025-01-01', NULL),
  ('NON-PPN',     'ppn', 'Tidak dikenakan atau dibebaskan PPN',             0,  1,  1, NULL,   '2000-01-01', NULL),
  ('PPH23',       'pph', 'PPh 23 jasa 2%',                                200,  1,  1, '23',   '2009-01-01', NULL),
  ('PPH42-K175',  'pph', 'PPh 4(2) konstruksi, kualifikasi kecil 1,75%',  175,  1,  1, '4(2)', '2022-02-21', NULL),
  ('PPH42-K265',  'pph', 'PPh 4(2) konstruksi, menengah/besar 2,65%',     265,  1,  1, '4(2)', '2022-02-21', NULL),
  ('PPH42-K4',    'pph', 'PPh 4(2) konstruksi, tanpa sertifikat 4%',      400,  1,  1, '4(2)', '2022-02-21', NULL),
  ('PPH42-SEWA',  'pph', 'PPh 4(2) sewa tanah/bangunan 10%',             1000,  1,  1, '4(2)', '2009-01-01', NULL);

-- Kas dan bank ----------------------------------------------------------------------

CREATE SCHEMA IF NOT EXISTS cash;

-- Akun buku besar yang dipakai sebagai rekening kas atau bank (CB-01).
CREATE TABLE cash.accounts (
  company_id      UUID NOT NULL,
  account_id      UUID NOT NULL,
  kind            TEXT NOT NULL CHECK (kind IN ('kas', 'bank')),
  bank_name       TEXT NOT NULL DEFAULT '',
  account_number  TEXT NOT NULL DEFAULT '',
  active          BOOLEAN NOT NULL DEFAULT true,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (company_id, account_id),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

-- Penerimaan, pengeluaran, dan transfer manual (CB-02, CB-03).
CREATE TABLE cash.transactions (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  number            TEXT NOT NULL,
  kind              TEXT NOT NULL CHECK (kind IN ('in', 'out', 'transfer')),
  trx_date          DATE NOT NULL,
  cash_account_id   UUID NOT NULL,
  to_account_id     UUID,              -- hanya transfer
  amount            NUMERIC(19,4) NOT NULL CHECK (amount > 0),
  memo              TEXT NOT NULL DEFAULT '',
  reference         TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  journal_entry_id  UUID NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by        UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, cash_account_id) REFERENCES cash.accounts (company_id, account_id),
  FOREIGN KEY (company_id, to_account_id) REFERENCES cash.accounts (company_id, account_id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK ((kind = 'transfer') = (to_account_id IS NOT NULL)),
  CHECK (to_account_id IS DISTINCT FROM cash_account_id)
);
CREATE INDEX cash_transactions_date_idx ON cash.transactions (company_id, trx_date DESC, id DESC);

CREATE TABLE cash.transaction_lines (
  id              UUID PRIMARY KEY,
  company_id      UUID NOT NULL,
  transaction_id  UUID NOT NULL,
  line_no         SMALLINT NOT NULL CHECK (line_no > 0),
  account_id      UUID NOT NULL,
  amount          NUMERIC(19,4) NOT NULL CHECK (amount > 0),
  memo            TEXT NOT NULL DEFAULT '',
  UNIQUE (transaction_id, line_no),
  FOREIGN KEY (company_id, transaction_id) REFERENCES cash.transactions (company_id, id),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

CREATE TRIGGER cash_transactions_guard
  BEFORE UPDATE OR DELETE ON cash.transactions
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER cash_transaction_lines_append_only
  BEFORE UPDATE OR DELETE ON cash.transaction_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- Penjualan (AR) --------------------------------------------------------------------

CREATE SCHEMA IF NOT EXISTS sales;

CREATE TABLE sales.invoices (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  number            TEXT,                -- diberikan saat posting, sehingga tanpa celah
  status            TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'void')),
  customer_id       UUID NOT NULL,
  invoice_date      DATE NOT NULL,
  due_date          DATE NOT NULL,
  memo              TEXT NOT NULL DEFAULT '',
  subtotal          NUMERIC(19,4) NOT NULL DEFAULT 0, -- Σ qty × harga
  discount          NUMERIC(19,4) NOT NULL DEFAULT 0,
  dpp               NUMERIC(19,4) NOT NULL DEFAULT 0, -- Σ nilai baris setelah diskon
  other_dpp         NUMERIC(19,4) NOT NULL DEFAULT 0, -- DPP nilai lain
  vat               NUMERIC(19,4) NOT NULL DEFAULT 0,
  total             NUMERIC(19,4) NOT NULL DEFAULT 0, -- dpp + vat
  journal_entry_id  UUID,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by        UUID REFERENCES platform.users (id),
  posted_at         TIMESTAMPTZ,
  posted_by         UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  version           INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, customer_id) REFERENCES core.contacts (company_id, id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK (due_date >= invoice_date),
  CHECK ((status = 'draft') = (number IS NULL)),
  CHECK ((status = 'draft') = (journal_entry_id IS NULL))
);
CREATE INDEX invoices_date_idx ON sales.invoices (company_id, invoice_date DESC, id DESC);
CREATE INDEX invoices_customer_idx ON sales.invoices (company_id, customer_id, status, due_date);

CREATE TABLE sales.invoice_lines (
  id            UUID PRIMARY KEY,
  company_id    UUID NOT NULL,
  invoice_id    UUID NOT NULL,
  line_no       SMALLINT NOT NULL CHECK (line_no > 0),
  description   TEXT NOT NULL DEFAULT '',
  account_id    UUID NOT NULL,          -- akun pendapatan
  qty           NUMERIC(19,4) NOT NULL CHECK (qty > 0),
  unit_price    NUMERIC(19,4) NOT NULL CHECK (unit_price >= 0),
  discount_pct  NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (discount_pct BETWEEN 0 AND 100),
  discount      NUMERIC(19,4) NOT NULL DEFAULT 0,
  net           NUMERIC(19,4) NOT NULL,  -- DPP baris
  tax_code      TEXT NOT NULL,
  other_dpp     NUMERIC(19,4) NOT NULL DEFAULT 0,
  vat           NUMERIC(19,4) NOT NULL DEFAULT 0,
  UNIQUE (invoice_id, line_no),
  FOREIGN KEY (company_id, invoice_id) REFERENCES sales.invoices (company_id, id) ON DELETE CASCADE,
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

-- Baris faktur hanya bisa diubah selama fakturnya masih draf.
-- +goose StatementBegin
CREATE FUNCTION sales.guard_invoice_line() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s TEXT;
BEGIN
  SELECT status INTO s FROM sales.invoices WHERE id = COALESCE(NEW.invoice_id, OLD.invoice_id);
  IF s IS NOT NULL AND s <> 'draft' THEN
    RAISE EXCEPTION 'baris faktur yang sudah diposting tidak bisa diubah' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END
$$;
-- +goose StatementEnd

CREATE TRIGGER invoices_guard
  BEFORE UPDATE OR DELETE ON sales.invoices
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER invoice_lines_guard
  BEFORE INSERT OR UPDATE OR DELETE ON sales.invoice_lines
  FOR EACH ROW EXECUTE FUNCTION sales.guard_invoice_line();

-- Pelunasan piutang (AR-04): satu penerimaan bisa melunasi banyak faktur, sebagian atau
-- penuh, dengan PPh 23 atau PPh final 4(2) yang dipotong pelanggan per alokasi.
CREATE TABLE sales.receipts (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  number            TEXT NOT NULL,
  customer_id       UUID NOT NULL,
  receipt_date      DATE NOT NULL,
  cash_account_id   UUID NOT NULL,
  amount            NUMERIC(19,4) NOT NULL CHECK (amount > 0), -- uang yang diterima
  memo              TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  journal_entry_id  UUID NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by        UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, customer_id) REFERENCES core.contacts (company_id, id),
  FOREIGN KEY (company_id, cash_account_id) REFERENCES cash.accounts (company_id, account_id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id)
);
CREATE INDEX receipts_date_idx ON sales.receipts (company_id, receipt_date DESC, id DESC);

CREATE TABLE sales.receipt_allocations (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL,
  receipt_id        UUID NOT NULL,
  invoice_id        UUID NOT NULL,
  amount            NUMERIC(19,4) NOT NULL CHECK (amount > 0),     -- mengurangi sisa faktur
  withholding_code  TEXT,
  withholding       NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (withholding >= 0),
  UNIQUE (receipt_id, invoice_id),
  FOREIGN KEY (company_id, receipt_id) REFERENCES sales.receipts (company_id, id),
  FOREIGN KEY (company_id, invoice_id) REFERENCES sales.invoices (company_id, id),
  CHECK (withholding < amount),
  CHECK ((withholding = 0) = (withholding_code IS NULL))
);
CREATE INDEX receipt_allocations_invoice_idx ON sales.receipt_allocations (invoice_id);

CREATE TRIGGER receipts_guard
  BEFORE UPDATE OR DELETE ON sales.receipts
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER receipt_allocations_append_only
  BEFORE UPDATE OR DELETE ON sales.receipt_allocations
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- Pembelian (AP) --------------------------------------------------------------------

CREATE SCHEMA IF NOT EXISTS purchase;

CREATE TABLE purchase.bills (
  id                   UUID PRIMARY KEY,
  company_id           UUID NOT NULL REFERENCES core.companies (id),
  number               TEXT,
  status               TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted', 'void')),
  supplier_id          UUID NOT NULL,
  supplier_invoice_no  TEXT NOT NULL DEFAULT '',
  bill_date            DATE NOT NULL,
  due_date             DATE NOT NULL,
  memo                 TEXT NOT NULL DEFAULT '',
  subtotal             NUMERIC(19,4) NOT NULL DEFAULT 0,
  discount             NUMERIC(19,4) NOT NULL DEFAULT 0,
  dpp                  NUMERIC(19,4) NOT NULL DEFAULT 0,
  other_dpp            NUMERIC(19,4) NOT NULL DEFAULT 0,
  vat                  NUMERIC(19,4) NOT NULL DEFAULT 0,
  withholding_code     TEXT,
  withholding          NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (withholding >= 0),
  total                NUMERIC(19,4) NOT NULL DEFAULT 0, -- dpp + vat
  payable              NUMERIC(19,4) NOT NULL DEFAULT 0, -- total − PPh dipotong = utang ke pemasok
  journal_entry_id     UUID,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by           UUID REFERENCES platform.users (id),
  posted_at            TIMESTAMPTZ,
  posted_by            UUID REFERENCES platform.users (id),
  voided_at            TIMESTAMPTZ,
  voided_by            UUID REFERENCES platform.users (id),
  void_reason          TEXT,
  version              INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, supplier_id) REFERENCES core.contacts (company_id, id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK (due_date >= bill_date),
  CHECK ((status = 'draft') = (number IS NULL)),
  CHECK ((status = 'draft') = (journal_entry_id IS NULL)),
  CHECK ((withholding = 0) = (withholding_code IS NULL))
);
CREATE INDEX bills_date_idx ON purchase.bills (company_id, bill_date DESC, id DESC);
CREATE INDEX bills_supplier_idx ON purchase.bills (company_id, supplier_id, status, due_date);

CREATE TABLE purchase.bill_lines (
  id            UUID PRIMARY KEY,
  company_id    UUID NOT NULL,
  bill_id       UUID NOT NULL,
  line_no       SMALLINT NOT NULL CHECK (line_no > 0),
  description   TEXT NOT NULL DEFAULT '',
  account_id    UUID NOT NULL,          -- akun beban, persediaan, atau aset
  qty           NUMERIC(19,4) NOT NULL CHECK (qty > 0),
  unit_price    NUMERIC(19,4) NOT NULL CHECK (unit_price >= 0),
  discount_pct  NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (discount_pct BETWEEN 0 AND 100),
  discount      NUMERIC(19,4) NOT NULL DEFAULT 0,
  net           NUMERIC(19,4) NOT NULL,
  tax_code      TEXT NOT NULL,
  other_dpp     NUMERIC(19,4) NOT NULL DEFAULT 0,
  vat           NUMERIC(19,4) NOT NULL DEFAULT 0,
  UNIQUE (bill_id, line_no),
  FOREIGN KEY (company_id, bill_id) REFERENCES purchase.bills (company_id, id) ON DELETE CASCADE,
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

-- +goose StatementBegin
CREATE FUNCTION purchase.guard_bill_line() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s TEXT;
BEGIN
  SELECT status INTO s FROM purchase.bills WHERE id = COALESCE(NEW.bill_id, OLD.bill_id);
  IF s IS NOT NULL AND s <> 'draft' THEN
    RAISE EXCEPTION 'baris tagihan yang sudah diposting tidak bisa diubah' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END
$$;
-- +goose StatementEnd

CREATE TRIGGER bills_guard
  BEFORE UPDATE OR DELETE ON purchase.bills
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER bill_lines_guard
  BEFORE INSERT OR UPDATE OR DELETE ON purchase.bill_lines
  FOR EACH ROW EXECUTE FUNCTION purchase.guard_bill_line();

CREATE TABLE purchase.payments (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  number            TEXT NOT NULL,
  supplier_id       UUID NOT NULL,
  payment_date      DATE NOT NULL,
  cash_account_id   UUID NOT NULL,
  amount            NUMERIC(19,4) NOT NULL CHECK (amount > 0),
  memo              TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  journal_entry_id  UUID NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by        UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, supplier_id) REFERENCES core.contacts (company_id, id),
  FOREIGN KEY (company_id, cash_account_id) REFERENCES cash.accounts (company_id, account_id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id)
);
CREATE INDEX payments_date_idx ON purchase.payments (company_id, payment_date DESC, id DESC);

CREATE TABLE purchase.payment_allocations (
  id          UUID PRIMARY KEY,
  company_id  UUID NOT NULL,
  payment_id  UUID NOT NULL,
  bill_id     UUID NOT NULL,
  amount      NUMERIC(19,4) NOT NULL CHECK (amount > 0),
  UNIQUE (payment_id, bill_id),
  FOREIGN KEY (company_id, payment_id) REFERENCES purchase.payments (company_id, id),
  FOREIGN KEY (company_id, bill_id) REFERENCES purchase.bills (company_id, id)
);
CREATE INDEX payment_allocations_bill_idx ON purchase.payment_allocations (bill_id);

CREATE TRIGGER payments_guard
  BEFORE UPDATE OR DELETE ON purchase.payments
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER payment_allocations_append_only
  BEFORE UPDATE OR DELETE ON purchase.payment_allocations
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- +goose Down
DROP SCHEMA purchase CASCADE;
DROP SCHEMA sales CASCADE;
DROP SCHEMA cash CASCADE;
DROP SCHEMA tax CASCADE;
DROP TABLE core.contacts;
DROP FUNCTION platform.guard_posted_document();
ALTER TABLE core.companies DROP COLUMN pkp;
