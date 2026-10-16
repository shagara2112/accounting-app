-- +goose Up
-- Tahap 9F: dimensi cabang dan departemen di setiap baris jurnal (GL-09) dengan pembatasan
-- akses per cabang (PLT-12), penomoran dokumen yang bisa diatur per seri dan cabang (GL-07),
-- dan jurnal berulang (GL-08).

-- Dimensi analitik ------------------------------------------------------------------
CREATE TABLE core.branches (
  id          UUID PRIMARY KEY,
  company_id  UUID NOT NULL REFERENCES core.companies (id),
  code        TEXT NOT NULL CHECK (code ~ '^[A-Z0-9-]{1,10}$'),
  name        TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 100),
  active      BOOLEAN NOT NULL DEFAULT true,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  version     INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, code)
);

CREATE TABLE core.departments (
  id          UUID PRIMARY KEY,
  company_id  UUID NOT NULL REFERENCES core.companies (id),
  code        TEXT NOT NULL CHECK (code ~ '^[A-Z0-9-]{1,10}$'),
  name        TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 100),
  active      BOOLEAN NOT NULL DEFAULT true,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  version     INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, code)
);

ALTER TABLE ledger.journal_lines
  ADD COLUMN branch_id UUID,
  ADD COLUMN department_id UUID,
  ADD FOREIGN KEY (company_id, branch_id) REFERENCES core.branches (company_id, id),
  ADD FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id);
CREATE INDEX journal_lines_branch_idx ON ledger.journal_lines (company_id, branch_id, entry_date) WHERE branch_id IS NOT NULL;

-- Dokumen membawa cabang dan departemen di kepalanya; jurnalnya mewarisi ke setiap baris.
ALTER TABLE sales.invoices
  ADD COLUMN branch_id UUID, ADD COLUMN department_id UUID,
  ADD FOREIGN KEY (company_id, branch_id) REFERENCES core.branches (company_id, id),
  ADD FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id);
ALTER TABLE purchase.bills
  ADD COLUMN branch_id UUID, ADD COLUMN department_id UUID,
  ADD FOREIGN KEY (company_id, branch_id) REFERENCES core.branches (company_id, id),
  ADD FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id);
ALTER TABLE cash.transactions
  ADD COLUMN branch_id UUID, ADD COLUMN department_id UUID,
  ADD FOREIGN KEY (company_id, branch_id) REFERENCES core.branches (company_id, id),
  ADD FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id);
ALTER TABLE sales.daily_sales
  ADD COLUMN branch_id UUID,
  ADD FOREIGN KEY (company_id, branch_id) REFERENCES core.branches (company_id, id);
ALTER TABLE inventory.movements
  ADD COLUMN branch_id UUID, ADD COLUMN department_id UUID;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION inventory.guard_movement() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE'
     AND (NEW.id, NEW.company_id, NEW.product_id, NEW.seq, NEW.move_date, NEW.qty, NEW.valuation,
          NEW.fixed_value, NEW.mirror_of, NEW.cost_of, NEW.owned, NEW.counter_account_id, NEW.project_id,
          NEW.branch_id, NEW.department_id, NEW.source_type, NEW.source_id, NEW.source_number, NEW.memo)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.product_id, OLD.seq, OLD.move_date, OLD.qty, OLD.valuation,
          OLD.fixed_value, OLD.mirror_of, OLD.cost_of, OLD.owned, OLD.counter_account_id, OLD.project_id,
          OLD.branch_id, OLD.department_id, OLD.source_type, OLD.source_id, OLD.source_number, OLD.memo) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'pergerakan stok tidak bisa diubah atau dihapus; gunakan void dokumennya'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

-- PLT-12: pengguna bisa dibatasi ke cabang tertentu di satu PT (kosong = semua cabang).
ALTER TABLE core.user_company_roles ADD COLUMN branch_ids UUID[] NOT NULL DEFAULT '{}';

-- Penomoran dokumen (GL-07) ---------------------------------------------------------
-- Tanpa baris = format bawaan SERI/YYYY/MM/0001 yang diulang tiap bulan.
CREATE TABLE ledger.number_formats (
  company_id  UUID NOT NULL REFERENCES core.companies (id),
  series      TEXT NOT NULL,
  prefix      TEXT NOT NULL CHECK (prefix ~ '^[A-Za-z0-9-]{1,12}$'),
  reset       TEXT NOT NULL CHECK (reset IN ('month', 'year', 'never')),
  digits      SMALLINT NOT NULL CHECK (digits BETWEEN 3 AND 8),
  per_branch  BOOLEAN NOT NULL DEFAULT false,
  updated_by  UUID REFERENCES platform.users (id),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (company_id, series)
);

-- Jurnal berulang (GL-08, GL-11) ----------------------------------------------------
CREATE TABLE ledger.recurring_journals (
  id               UUID PRIMARY KEY,
  company_id       UUID NOT NULL REFERENCES core.companies (id),
  name             TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  kind             TEXT NOT NULL CHECK (kind IN ('umum', 'penyesuaian')),
  memo             TEXT NOT NULL DEFAULT '',
  auto_reverse     BOOLEAN NOT NULL DEFAULT false,
  day_of_month     SMALLINT NOT NULL CHECK (day_of_month BETWEEN 0 AND 28), -- 0 = hari terakhir bulan
  next_date        DATE NOT NULL,
  end_date         DATE,
  active           BOOLEAN NOT NULL DEFAULT true,
  last_error       TEXT,
  last_run_at      TIMESTAMPTZ,
  source_entry_id  UUID,
  created_by       UUID REFERENCES platform.users (id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  version          INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  CHECK (kind = 'penyesuaian' OR NOT auto_reverse)
);
CREATE INDEX recurring_due_idx ON ledger.recurring_journals (next_date) WHERE active;

CREATE TABLE ledger.recurring_lines (
  id             UUID PRIMARY KEY,
  company_id     UUID NOT NULL,
  recurring_id   UUID NOT NULL,
  line_no        SMALLINT NOT NULL CHECK (line_no > 0),
  account_id     UUID NOT NULL,
  debit          NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (debit >= 0),
  credit         NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (credit >= 0),
  memo           TEXT NOT NULL DEFAULT '',
  project_id     UUID,
  branch_id      UUID,
  department_id  UUID,
  UNIQUE (recurring_id, line_no),
  FOREIGN KEY (company_id, recurring_id) REFERENCES ledger.recurring_journals (company_id, id) ON DELETE CASCADE,
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id),
  FOREIGN KEY (company_id, branch_id) REFERENCES core.branches (company_id, id),
  FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id),
  CHECK ((debit = 0) <> (credit = 0))
);

-- Satu jurnal per template per tanggal jadwal, supaya penjadwal yang berjalan ulang tidak
-- memposting dua kali.
CREATE TABLE ledger.recurring_runs (
  recurring_id  UUID NOT NULL REFERENCES ledger.recurring_journals (id) ON DELETE CASCADE,
  run_date      DATE NOT NULL,
  entry_id      UUID NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (recurring_id, run_date)
);

-- +goose Down
DROP TABLE ledger.recurring_runs;
DROP TABLE ledger.recurring_lines;
DROP TABLE ledger.recurring_journals;
DROP TABLE ledger.number_formats;
ALTER TABLE core.user_company_roles DROP COLUMN branch_ids;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION inventory.guard_movement() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE'
     AND (NEW.id, NEW.company_id, NEW.product_id, NEW.seq, NEW.move_date, NEW.qty, NEW.valuation,
          NEW.fixed_value, NEW.mirror_of, NEW.cost_of, NEW.owned, NEW.counter_account_id, NEW.project_id,
          NEW.source_type, NEW.source_id, NEW.source_number, NEW.memo)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.product_id, OLD.seq, OLD.move_date, OLD.qty, OLD.valuation,
          OLD.fixed_value, OLD.mirror_of, OLD.cost_of, OLD.owned, OLD.counter_account_id, OLD.project_id,
          OLD.source_type, OLD.source_id, OLD.source_number, OLD.memo) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'pergerakan stok tidak bisa diubah atau dihapus; gunakan void dokumennya'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd
ALTER TABLE inventory.movements DROP COLUMN department_id, DROP COLUMN branch_id;
ALTER TABLE sales.daily_sales DROP COLUMN branch_id;
ALTER TABLE cash.transactions DROP COLUMN department_id, DROP COLUMN branch_id;
ALTER TABLE purchase.bills DROP COLUMN department_id, DROP COLUMN branch_id;
ALTER TABLE sales.invoices DROP COLUMN department_id, DROP COLUMN branch_id;
DROP INDEX ledger.journal_lines_branch_idx;
ALTER TABLE ledger.journal_lines DROP COLUMN department_id, DROP COLUMN branch_id;
DROP TABLE core.departments;
DROP TABLE core.branches;
