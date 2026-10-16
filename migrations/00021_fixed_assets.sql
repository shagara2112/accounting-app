-- +goose Up
-- Aset tetap (FA-01–03): register aset, penyusutan bulanan dua buku (komersial dijurnal,
-- fiskal untuk koreksi fiskal), dan pelepasan aset dengan jurnal laba atau rugi.
--
-- Perolehan aset tidak dijurnal modul ini: aset dibeli lewat tagihan pembelian, transaksi kas,
-- jurnal, atau saldo awal (debit akun aset). Register mencatat rinciannya, dan halaman aset
-- mencocokkan total register dengan saldo akun aset dan akumulasi penyusutan di buku besar.
CREATE SCHEMA asset;

CREATE TABLE asset.categories (
  id                  UUID PRIMARY KEY,
  company_id          UUID NOT NULL REFERENCES core.companies (id),
  name                TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 100),
  asset_account_id    UUID NOT NULL,
  accum_account_id    UUID,          -- kosong untuk aset yang tidak disusutkan (tanah)
  expense_account_id  UUID,
  method              TEXT NOT NULL CHECK (method IN ('garis_lurus', 'saldo_menurun', 'tidak_disusutkan')),
  life_months         INT NOT NULL DEFAULT 0 CHECK (life_months BETWEEN 0 AND 600),
  fiscal_group        TEXT NOT NULL,
  fiscal_method       TEXT NOT NULL CHECK (fiscal_method IN ('garis_lurus', 'saldo_menurun')),
  active              BOOLEAN NOT NULL DEFAULT true,
  version             INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  FOREIGN KEY (company_id, asset_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, accum_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, expense_account_id) REFERENCES ledger.accounts (company_id, id),
  CHECK (method = 'tidak_disusutkan' OR (accum_account_id IS NOT NULL AND expense_account_id IS NOT NULL AND life_months > 0))
);
CREATE UNIQUE INDEX categories_name_idx ON asset.categories (company_id, lower(name));

CREATE TABLE asset.assets (
  id                  UUID PRIMARY KEY,
  company_id          UUID NOT NULL REFERENCES core.companies (id),
  code                TEXT NOT NULL CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9._/-]{0,29}$'),
  name                TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  category_id         UUID NOT NULL,
  acquired_on         DATE NOT NULL,
  cost                NUMERIC(19,4) NOT NULL CHECK (cost > 0),
  residual            NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (residual >= 0),
  method              TEXT NOT NULL CHECK (method IN ('garis_lurus', 'saldo_menurun', 'tidak_disusutkan')),
  life_months         INT NOT NULL DEFAULT 0 CHECK (life_months BETWEEN 0 AND 600),
  depreciation_start  DATE NOT NULL CHECK (extract(day FROM depreciation_start) = 1),
  asset_account_id    UUID NOT NULL,
  accum_account_id    UUID,
  expense_account_id  UUID,
  fiscal_group        TEXT NOT NULL,
  fiscal_method       TEXT NOT NULL CHECK (fiscal_method IN ('garis_lurus', 'saldo_menurun')),
  -- Aset lama yang sudah disusutkan sebelum memakai Lajur: akumulasi s.d. akhir opening_month.
  opening_accum       NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (opening_accum >= 0),
  opening_month       DATE CHECK (extract(day FROM opening_month) = 1),
  location            TEXT NOT NULL DEFAULT '',
  custodian           TEXT NOT NULL DEFAULT '',
  notes               TEXT NOT NULL DEFAULT '',
  branch_id           UUID,
  department_id       UUID,
  status              TEXT NOT NULL DEFAULT 'aktif' CHECK (status IN ('aktif', 'dilepas')),
  disposed_on         DATE,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by          UUID REFERENCES platform.users (id),
  version             INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  FOREIGN KEY (company_id, category_id) REFERENCES asset.categories (company_id, id),
  FOREIGN KEY (company_id, asset_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, accum_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, expense_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, branch_id) REFERENCES core.branches (company_id, id),
  FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id),
  CHECK (residual <= cost AND opening_accum <= cost - residual),
  CHECK ((opening_month IS NULL) = (opening_accum = 0)),
  CHECK ((status = 'dilepas') = (disposed_on IS NOT NULL)),
  CHECK (method = 'tidak_disusutkan' OR (accum_account_id IS NOT NULL AND expense_account_id IS NOT NULL AND life_months > 0))
);
CREATE UNIQUE INDEX assets_code_idx ON asset.assets (company_id, lower(code));
CREATE INDEX assets_active_idx ON asset.assets (company_id) WHERE status = 'aktif';

-- Satu jurnal penyusutan per bulan (seri PNY), berisi penyusutan semua aset yang jatuh
-- tempo s.d. bulan itu, termasuk bulan-bulan yang belum pernah diposting (penyusutan susulan).
CREATE TABLE asset.depreciation_runs (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  month             DATE NOT NULL CHECK (extract(day FROM month) = 1),
  number            TEXT NOT NULL,
  journal_entry_id  UUID NOT NULL,
  total             NUMERIC(19,4) NOT NULL CHECK (total > 0),
  assets            INT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  posted_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  posted_by         UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  UNIQUE (company_id, id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id)
);
CREATE UNIQUE INDEX depreciation_runs_month_idx ON asset.depreciation_runs (company_id, month) WHERE status = 'posted';

-- Pelepasan aset (FA-03): dijual (uang langsung diterima), dijual lewat faktur penjualan,
-- atau dihapus. Jurnalnya (seri PAT) memuat penyusutan susulan s.d. bulan pelepasan.
CREATE TABLE asset.disposals (
  id                   UUID PRIMARY KEY,
  company_id           UUID NOT NULL REFERENCES core.companies (id),
  asset_id             UUID NOT NULL,
  number               TEXT NOT NULL,
  kind                 TEXT NOT NULL CHECK (kind IN ('jual', 'faktur', 'hapus')),
  disposal_date        DATE NOT NULL,
  proceeds             NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (proceeds >= 0),
  proceeds_account_id  UUID,
  cost                 NUMERIC(19,4) NOT NULL,
  accumulated          NUMERIC(19,4) NOT NULL,
  book_value           NUMERIC(19,4) NOT NULL,
  gain                 NUMERIC(19,4) NOT NULL, -- negatif = rugi
  memo                 TEXT NOT NULL DEFAULT '',
  journal_entry_id     UUID NOT NULL,
  status               TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by           UUID REFERENCES platform.users (id),
  voided_at            TIMESTAMPTZ,
  voided_by            UUID REFERENCES platform.users (id),
  void_reason          TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, asset_id) REFERENCES asset.assets (company_id, id),
  FOREIGN KEY (company_id, proceeds_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK ((kind = 'jual') = (proceeds_account_id IS NOT NULL))
);
CREATE UNIQUE INDEX disposals_active_idx ON asset.disposals (company_id, asset_id) WHERE status = 'posted';

-- Penyusutan yang sudah dijurnal, per aset per bulan: dari jurnal bulanan atau dari jurnal
-- pelepasan. Baris dihapus bersama void dokumennya, sehingga bulan itu disusutkan lagi nanti.
CREATE TABLE asset.depreciation_lines (
  id           UUID PRIMARY KEY,
  company_id   UUID NOT NULL,
  asset_id     UUID NOT NULL,
  month        DATE NOT NULL CHECK (extract(day FROM month) = 1),
  amount       NUMERIC(19,4) NOT NULL CHECK (amount > 0),
  run_id       UUID,
  disposal_id  UUID,
  UNIQUE (asset_id, month),
  FOREIGN KEY (company_id, asset_id) REFERENCES asset.assets (company_id, id),
  FOREIGN KEY (company_id, run_id) REFERENCES asset.depreciation_runs (company_id, id),
  FOREIGN KEY (company_id, disposal_id) REFERENCES asset.disposals (company_id, id),
  CHECK ((run_id IS NULL) <> (disposal_id IS NULL))
);
CREATE INDEX depreciation_lines_run_idx ON asset.depreciation_lines (run_id) WHERE run_id IS NOT NULL;

ALTER TABLE platform.attachments DROP CONSTRAINT attachments_object_type_check;
ALTER TABLE platform.attachments ADD CONSTRAINT attachments_object_type_check CHECK (object_type IN (
  'journal', 'sales_invoice', 'sales_receipt', 'sales_credit_note', 'purchase_bill', 'purchase_payment',
  'purchase_debit_note', 'cash_transaction', 'inventory_adjustment', 'project', 'contact', 'fixed_asset'));

-- +goose Down
DELETE FROM platform.attachments WHERE object_type = 'fixed_asset';
ALTER TABLE platform.attachments DROP CONSTRAINT attachments_object_type_check;
ALTER TABLE platform.attachments ADD CONSTRAINT attachments_object_type_check CHECK (object_type IN (
  'journal', 'sales_invoice', 'sales_receipt', 'sales_credit_note', 'purchase_bill', 'purchase_payment',
  'purchase_debit_note', 'cash_transaction', 'inventory_adjustment', 'project', 'contact'));
DROP SCHEMA asset CASCADE;
