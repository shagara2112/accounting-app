-- +goose Up
-- Tahap 5: proyek (IND-01–IND-03, IND-05) dengan termin, uang muka, dan retensi (AR-08);
-- produk dan jasa (INV-01); persediaan rata-rata tertimbang dengan HPP otomatis (INV-03);
-- penyesuaian, pemakaian, dan stock opname (INV-04).

-- Proyek ----------------------------------------------------------------------------

CREATE SCHEMA IF NOT EXISTS project;

CREATE TABLE project.projects (
  id                  UUID PRIMARY KEY,
  company_id          UUID NOT NULL REFERENCES core.companies (id),
  code                TEXT NOT NULL CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9._/-]{0,29}$'),
  name                TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  customer_id         UUID,                 -- pemberi kerja; wajib untuk faktur termin
  contract_value      NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (contract_value >= 0), -- sebelum PPN
  dp_pct              NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (dp_pct BETWEEN 0 AND 100),
  retention_pct       NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (retention_pct BETWEEN 0 AND 100),
  pph_code            TEXT,                 -- PPh final 4(2) yang dipotong pemberi kerja
  revenue_account_id  UUID,                 -- akun pendapatan untuk faktur termin
  start_date          DATE,
  end_date            DATE,
  status              TEXT NOT NULL DEFAULT 'berjalan' CHECK (status IN ('berjalan', 'selesai', 'batal')),
  notes               TEXT NOT NULL DEFAULT '',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by          UUID REFERENCES platform.users (id),
  version             INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  FOREIGN KEY (company_id, customer_id) REFERENCES core.contacts (company_id, id),
  FOREIGN KEY (company_id, revenue_account_id) REFERENCES ledger.accounts (company_id, id),
  CHECK (end_date IS NULL OR start_date IS NULL OR end_date >= start_date)
);
CREATE UNIQUE INDEX projects_code_idx ON project.projects (company_id, lower(code));

-- RAB per pos biaya (IND-01). Satu pos = satu akun biaya, sehingga biaya aktual tinggal
-- dibaca dari saldo akun itu untuk proyek ini.
CREATE TABLE project.budget_lines (
  id           UUID PRIMARY KEY,
  company_id   UUID NOT NULL,
  project_id   UUID NOT NULL,
  line_no      SMALLINT NOT NULL CHECK (line_no > 0),
  account_id   UUID NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  amount       NUMERIC(19,4) NOT NULL CHECK (amount >= 0),
  UNIQUE (project_id, account_id),
  FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id) ON DELETE CASCADE,
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

-- Dimensi proyek di buku besar --------------------------------------------------------

ALTER TABLE ledger.journal_lines ADD COLUMN project_id UUID;
ALTER TABLE ledger.journal_lines
  ADD FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id);
CREATE INDEX journal_lines_project_idx ON ledger.journal_lines (company_id, project_id, entry_date)
  WHERE project_id IS NOT NULL;

-- Saldo per proyek × akun × bulan, diisi trigger yang sama dengan ledger.balances, sehingga
-- laba per proyek dan biaya vs RAB tidak perlu menjumlah ulang baris jurnal.
CREATE TABLE ledger.project_balances (
  company_id  UUID NOT NULL,
  project_id  UUID NOT NULL,
  account_id  UUID NOT NULL,
  month       DATE NOT NULL,
  debit       NUMERIC(19,4) NOT NULL DEFAULT 0,
  credit      NUMERIC(19,4) NOT NULL DEFAULT 0,
  PRIMARY KEY (company_id, project_id, account_id, month),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.apply_line_to_balance() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  k TEXT;
  m DATE := date_trunc('month', NEW.entry_date)::date;
BEGIN
  SELECT kind INTO k FROM ledger.journal_entries WHERE id = NEW.entry_id;
  INSERT INTO ledger.balances (company_id, account_id, month, kind, debit, credit)
  VALUES (NEW.company_id, NEW.account_id, m, k, NEW.debit, NEW.credit)
  ON CONFLICT (company_id, account_id, month, kind) DO UPDATE
    SET debit = ledger.balances.debit + EXCLUDED.debit,
        credit = ledger.balances.credit + EXCLUDED.credit;
  IF NEW.project_id IS NOT NULL THEN
    INSERT INTO ledger.project_balances (company_id, project_id, account_id, month, debit, credit)
    VALUES (NEW.company_id, NEW.project_id, NEW.account_id, m, NEW.debit, NEW.credit)
    ON CONFLICT (company_id, project_id, account_id, month) DO UPDATE
      SET debit = ledger.project_balances.debit + EXCLUDED.debit,
          credit = ledger.project_balances.credit + EXCLUDED.credit;
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- Produk dan persediaan ---------------------------------------------------------------

CREATE SCHEMA IF NOT EXISTS inventory;

-- Master produk dan jasa (INV-01). Barang dilacak stoknya dan butuh akun persediaan serta
-- akun HPP; jasa hanya membawa akun pendapatan dan beban default.
CREATE TABLE inventory.products (
  id                    UUID PRIMARY KEY,
  company_id            UUID NOT NULL REFERENCES core.companies (id),
  code                  TEXT NOT NULL CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9._/-]{0,39}$'),
  name                  TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  kind                  TEXT NOT NULL CHECK (kind IN ('barang', 'jasa')),
  unit                  TEXT NOT NULL CHECK (length(btrim(unit)) BETWEEN 1 AND 20),
  sale_price            NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (sale_price >= 0),
  purchase_price        NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (purchase_price >= 0),
  revenue_account_id    UUID,
  expense_account_id    UUID,  -- barang: akun HPP; jasa: beban default di tagihan
  inventory_account_id  UUID,  -- hanya barang
  active                BOOLEAN NOT NULL DEFAULT true,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by            UUID REFERENCES platform.users (id),
  version               INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  FOREIGN KEY (company_id, revenue_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, expense_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, inventory_account_id) REFERENCES ledger.accounts (company_id, id),
  CHECK ((kind = 'barang') = (inventory_account_id IS NOT NULL)),
  CHECK (kind = 'jasa' OR expense_account_id IS NOT NULL)
);
CREATE UNIQUE INDEX products_code_idx ON inventory.products (company_id, lower(code));

-- Jenis produk tidak bisa diubah setelah ada pergerakan stok; akun persediaan juga tidak,
-- karena saldo akun lama tidak ikut pindah.
-- +goose StatementBegin
CREATE FUNCTION inventory.guard_product() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'produk tidak bisa dihapus; nonaktifkan saja' USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.kind, NEW.inventory_account_id) IS DISTINCT FROM (OLD.kind, OLD.inventory_account_id)
     AND EXISTS (SELECT 1 FROM inventory.movements WHERE product_id = OLD.id) THEN
    RAISE EXCEPTION 'jenis dan akun persediaan produk yang sudah punya pergerakan stok tidak bisa diubah'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Saldo stok terakhir per produk. Barisnya juga dipakai sebagai kunci: setiap perubahan
-- pergerakan produk mengunci baris ini lebih dulu (FOR UPDATE), urut id produk.
CREATE TABLE inventory.stock (
  company_id  UUID NOT NULL,
  product_id  UUID NOT NULL,
  qty         NUMERIC(19,4) NOT NULL DEFAULT 0,
  value       NUMERIC(19,4) NOT NULL DEFAULT 0,
  PRIMARY KEY (company_id, product_id),
  FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id)
);

-- Kartu stok. Urutan = (move_date, seq). Nilai bertanda: positif masuk, negatif keluar.
--   fixed   : nilai tetap dari dokumen (pembelian, penyesuaian masuk, void tagihan)
--   average : keluar dengan biaya rata-rata saat itu (penjualan, pemakaian, penyesuaian keluar)
--   mirror  : kebalikan persis pergerakan lain (void dokumen yang nilainya dihitung di sini)
-- Pergerakan "owned" dijurnal modul persediaan sendiri: setiap kali hitung ulang menghasilkan
-- nilai berbeda dari posted_value, selisihnya dijurnal (HPP atau selisih persediaan).
CREATE TABLE inventory.movements (
  id                  UUID PRIMARY KEY,
  company_id          UUID NOT NULL,
  product_id          UUID NOT NULL,
  seq                 BIGINT GENERATED ALWAYS AS IDENTITY,
  move_date           DATE NOT NULL,
  qty                 NUMERIC(19,4) NOT NULL CHECK (qty <> 0),
  valuation           TEXT NOT NULL CHECK (valuation IN ('fixed', 'average', 'mirror')),
  fixed_value         NUMERIC(19,4),
  mirror_of           UUID,
  value               NUMERIC(19,4) NOT NULL DEFAULT 0,
  owned               BOOLEAN NOT NULL,
  posted_value        NUMERIC(19,4) NOT NULL DEFAULT 0,
  counter_account_id  UUID,
  project_id          UUID,
  qty_after           NUMERIC(19,4) NOT NULL DEFAULT 0,
  value_after         NUMERIC(19,4) NOT NULL DEFAULT 0,
  source_type         TEXT NOT NULL,
  source_id           UUID NOT NULL,
  source_number       TEXT NOT NULL,
  memo                TEXT NOT NULL DEFAULT '',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (company_id, id),
  FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id),
  FOREIGN KEY (company_id, mirror_of) REFERENCES inventory.movements (company_id, id),
  FOREIGN KEY (company_id, counter_account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id),
  CHECK ((valuation = 'fixed') = (fixed_value IS NOT NULL)),
  CHECK ((valuation = 'mirror') = (mirror_of IS NOT NULL)),
  CHECK (valuation <> 'average' OR qty < 0),
  CHECK (owned = (counter_account_id IS NOT NULL)),
  CHECK (owned OR posted_value = 0)
);
CREATE INDEX movements_product_idx ON inventory.movements (company_id, product_id, move_date, seq);
CREATE INDEX movements_source_idx ON inventory.movements (company_id, source_type, source_id);

-- Hanya hasil hitung (value, posted_value, qty_after, value_after) yang boleh berubah;
-- pergerakan tidak pernah dihapus. Koreksi = pergerakan pembalik.
-- +goose StatementBegin
CREATE FUNCTION inventory.guard_movement() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE'
     AND (NEW.id, NEW.company_id, NEW.product_id, NEW.seq, NEW.move_date, NEW.qty, NEW.valuation,
          NEW.fixed_value, NEW.mirror_of, NEW.owned, NEW.counter_account_id, NEW.project_id,
          NEW.source_type, NEW.source_id, NEW.source_number, NEW.memo)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.product_id, OLD.seq, OLD.move_date, OLD.qty, OLD.valuation,
          OLD.fixed_value, OLD.mirror_of, OLD.owned, OLD.counter_account_id, OLD.project_id,
          OLD.source_type, OLD.source_id, OLD.source_number, OLD.memo) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'pergerakan stok tidak bisa diubah atau dihapus; gunakan void dokumennya'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER movements_guard
  BEFORE UPDATE OR DELETE ON inventory.movements
  FOR EACH ROW EXECUTE FUNCTION inventory.guard_movement();

CREATE TRIGGER products_guard
  BEFORE UPDATE OR DELETE ON inventory.products
  FOR EACH ROW EXECUTE FUNCTION inventory.guard_product();

-- Dokumen persediaan: penyesuaian stok, pemakaian barang (mis. material ke proyek), dan
-- stock opname. Diposting saat disimpan; koreksi lewat void.
CREATE TABLE inventory.adjustments (
  id           UUID PRIMARY KEY,
  company_id   UUID NOT NULL REFERENCES core.companies (id),
  number       TEXT NOT NULL,
  kind         TEXT NOT NULL CHECK (kind IN ('penyesuaian', 'pemakaian', 'opname')),
  adj_date     DATE NOT NULL,
  account_id   UUID NOT NULL,   -- lawan akun persediaan: selisih persediaan atau biaya pemakaian
  project_id   UUID,
  memo         TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by   UUID REFERENCES platform.users (id),
  voided_at    TIMESTAMPTZ,
  voided_by    UUID REFERENCES platform.users (id),
  void_date    DATE,
  void_reason  TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id),
  CHECK ((status = 'void') = (voided_at IS NOT NULL))
);
CREATE INDEX adjustments_date_idx ON inventory.adjustments (company_id, adj_date DESC, id DESC);

CREATE TABLE inventory.adjustment_lines (
  id             UUID PRIMARY KEY,
  company_id     UUID NOT NULL,
  adjustment_id  UUID NOT NULL,
  line_no        SMALLINT NOT NULL CHECK (line_no > 0),
  product_id     UUID NOT NULL,
  system_qty     NUMERIC(19,4),  -- opname: stok menurut sistem saat diposting
  counted_qty    NUMERIC(19,4),  -- opname: hasil hitung fisik
  qty            NUMERIC(19,4) NOT NULL, -- selisih bertanda; 0 = cocok (hanya opname)
  unit_cost      NUMERIC(19,4),  -- hanya untuk qty > 0
  memo           TEXT NOT NULL DEFAULT '',
  UNIQUE (adjustment_id, line_no),
  FOREIGN KEY (company_id, adjustment_id) REFERENCES inventory.adjustments (company_id, id),
  FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id),
  CHECK ((qty > 0) = (unit_cost IS NOT NULL)),
  CHECK (unit_cost IS NULL OR unit_cost >= 0)
);

CREATE TRIGGER adjustments_guard
  BEFORE UPDATE OR DELETE ON inventory.adjustments
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER adjustment_lines_append_only
  BEFORE UPDATE OR DELETE ON inventory.adjustment_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- Penjualan, pembelian, dan kas ikut membawa proyek dan produk ---------------------------

-- Jenis faktur: biasa, uang muka proyek, termin progres (AR-08), atau pelepasan retensi.
-- Faktur termin menahan retensi sebagai piutang retensi, sehingga yang ditagih sekarang
-- (receivable) = total − retensi.
ALTER TABLE sales.invoices
  ADD COLUMN kind TEXT NOT NULL DEFAULT 'biasa' CHECK (kind IN ('biasa', 'uang_muka', 'termin', 'retensi')),
  ADD COLUMN project_id UUID,
  ADD COLUMN progress_pct NUMERIC(19,4) CHECK (progress_pct > 0 AND progress_pct <= 100),
  ADD COLUMN retention NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (retention >= 0),
  ADD COLUMN receivable NUMERIC(19,4) GENERATED ALWAYS AS (total - retention) STORED,
  ADD FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id),
  ADD CHECK (kind = 'biasa' OR project_id IS NOT NULL),
  ADD CHECK ((kind = 'termin') = (progress_pct IS NOT NULL)),
  ADD CHECK (retention = 0 OR kind = 'termin'),
  ADD CHECK (retention <= total);
CREATE INDEX invoices_project_idx ON sales.invoices (company_id, project_id) WHERE project_id IS NOT NULL;

-- discount_fixed: diskon baris berupa nominal (potongan uang muka di faktur termin), bukan %.
ALTER TABLE sales.invoice_lines
  ADD COLUMN product_id UUID,
  ADD COLUMN discount_fixed BOOLEAN NOT NULL DEFAULT false,
  ADD FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id);

ALTER TABLE purchase.bill_lines
  ADD COLUMN product_id UUID,
  ADD COLUMN project_id UUID,
  ADD FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id),
  ADD FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id);

ALTER TABLE cash.transaction_lines
  ADD COLUMN project_id UUID,
  ADD FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id);

-- +goose Down
ALTER TABLE cash.transaction_lines DROP COLUMN project_id;
ALTER TABLE purchase.bill_lines DROP COLUMN project_id, DROP COLUMN product_id;
ALTER TABLE sales.invoice_lines DROP COLUMN discount_fixed, DROP COLUMN product_id;
DROP INDEX sales.invoices_project_idx;
ALTER TABLE sales.invoices
  DROP COLUMN receivable, DROP COLUMN retention, DROP COLUMN progress_pct, DROP COLUMN project_id, DROP COLUMN kind;
DROP SCHEMA inventory CASCADE;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.apply_line_to_balance() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  k TEXT;
BEGIN
  SELECT kind INTO k FROM ledger.journal_entries WHERE id = NEW.entry_id;
  INSERT INTO ledger.balances (company_id, account_id, month, kind, debit, credit)
  VALUES (NEW.company_id, NEW.account_id, date_trunc('month', NEW.entry_date)::date, k, NEW.debit, NEW.credit)
  ON CONFLICT (company_id, account_id, month, kind) DO UPDATE
    SET debit = ledger.balances.debit + EXCLUDED.debit,
        credit = ledger.balances.credit + EXCLUDED.credit;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
DROP TABLE ledger.project_balances;
ALTER TABLE ledger.journal_lines DROP COLUMN project_id;
DROP SCHEMA project CASCADE;
