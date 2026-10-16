-- +goose Up
-- Saldo awal piutang dan utang per dokumen (GL-06): faktur dan tagihan saldo awal tanpa PPN
-- yang melawan akun ekuitas saldo awal, sehingga umur piutang/utang dan pelunasan langsung
-- berjalan. Rekap penjualan harian retail (IND-07) dengan PPN digunggung (TAX-12).

ALTER TABLE sales.invoices
  DROP CONSTRAINT invoices_kind_check,
  DROP CONSTRAINT invoices_check3,
  ADD CONSTRAINT invoices_kind_check CHECK (kind IN ('biasa', 'uang_muka', 'termin', 'retensi', 'saldo_awal')),
  ADD CONSTRAINT invoices_project_check CHECK (kind IN ('biasa', 'saldo_awal') OR project_id IS NOT NULL);

ALTER TABLE purchase.bills ADD COLUMN opening BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE sales.daily_sales (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  number            TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  sale_date         DATE NOT NULL,
  store             TEXT NOT NULL DEFAULT '',
  memo              TEXT NOT NULL DEFAULT '',
  dpp               NUMERIC(19,4) NOT NULL,
  other_dpp         NUMERIC(19,4) NOT NULL,
  vat               NUMERIC(19,4) NOT NULL,
  total             NUMERIC(19,4) NOT NULL CHECK (total > 0), -- uang diterima, termasuk PPN
  journal_entry_id  UUID NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by        UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK (total = dpp + vat)
);
-- Satu rekap aktif per tanggal dan toko.
CREATE UNIQUE INDEX daily_sales_day_idx ON sales.daily_sales (company_id, sale_date, store) WHERE status = 'posted';
CREATE INDEX daily_sales_date_idx ON sales.daily_sales (company_id, sale_date DESC, id DESC);

CREATE TABLE sales.daily_sale_lines (
  id             UUID PRIMARY KEY,
  company_id     UUID NOT NULL,
  daily_sale_id  UUID NOT NULL,
  line_no        SMALLINT NOT NULL CHECK (line_no > 0),
  product_id     UUID,
  description    TEXT NOT NULL DEFAULT '',
  account_id     UUID NOT NULL,          -- akun pendapatan
  qty            NUMERIC(19,4) NOT NULL CHECK (qty > 0),
  amount         NUMERIC(19,4) NOT NULL CHECK (amount > 0), -- nilai jual termasuk PPN
  tax_code       TEXT NOT NULL,
  net            NUMERIC(19,4) NOT NULL, -- DPP
  other_dpp      NUMERIC(19,4) NOT NULL DEFAULT 0,
  vat            NUMERIC(19,4) NOT NULL DEFAULT 0,
  UNIQUE (daily_sale_id, line_no),
  FOREIGN KEY (company_id, daily_sale_id) REFERENCES sales.daily_sales (company_id, id),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id),
  CHECK (amount = net + vat)
);

CREATE TABLE sales.daily_sale_payments (
  id             UUID PRIMARY KEY,
  company_id     UUID NOT NULL,
  daily_sale_id  UUID NOT NULL,
  account_id     UUID NOT NULL,          -- kas toko, bank, atau piutang kartu/QRIS
  amount         NUMERIC(19,4) NOT NULL CHECK (amount > 0),
  UNIQUE (daily_sale_id, account_id),
  FOREIGN KEY (company_id, daily_sale_id) REFERENCES sales.daily_sales (company_id, id),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

CREATE TRIGGER daily_sales_guard
  BEFORE UPDATE OR DELETE ON sales.daily_sales
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER daily_sale_lines_append_only
  BEFORE UPDATE OR DELETE ON sales.daily_sale_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

CREATE TRIGGER daily_sale_payments_append_only
  BEFORE UPDATE OR DELETE ON sales.daily_sale_payments
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- +goose Down
DROP TABLE sales.daily_sale_payments;
DROP TABLE sales.daily_sale_lines;
DROP TABLE sales.daily_sales;
ALTER TABLE purchase.bills DROP COLUMN opening;
ALTER TABLE sales.invoices
  DROP CONSTRAINT invoices_kind_check,
  DROP CONSTRAINT invoices_project_check,
  ADD CONSTRAINT invoices_kind_check CHECK (kind IN ('biasa', 'uang_muka', 'termin', 'retensi')),
  ADD CONSTRAINT invoices_check3 CHECK (kind = 'biasa' OR project_id IS NOT NULL);
