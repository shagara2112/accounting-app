-- +goose Up
-- Anggaran tahunan (BUD-01): per tahun buku ada satu atau beberapa versi (mis. Awal, Revisi 1);
-- satu versi aktif dipakai laporan anggaran vs realisasi (BUD-02). Baris anggaran per akun laba
-- rugi, boleh dirinci per departemen atau per proyek, dengan nominal per bulan tahun buku
-- (m01 = bulan pertama tahun buku). Nominal dicatat menurut saldo normal akunnya: pendapatan
-- dan beban sama-sama positif.
CREATE SCHEMA IF NOT EXISTS budget;

CREATE TABLE budget.budgets (
  id           UUID PRIMARY KEY,
  company_id   UUID NOT NULL REFERENCES core.companies (id),
  fiscal_year  INT NOT NULL CHECK (fiscal_year BETWEEN 2000 AND 2100), -- tahun bulan pertama tahun buku
  name         TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
  active       BOOLEAN NOT NULL DEFAULT false,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by   UUID REFERENCES platform.users (id),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  version      INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, fiscal_year, name)
);
CREATE UNIQUE INDEX budgets_active_key ON budget.budgets (company_id, fiscal_year) WHERE active;

CREATE TABLE budget.lines (
  id             UUID PRIMARY KEY,
  company_id     UUID NOT NULL,
  budget_id      UUID NOT NULL,
  account_id     UUID NOT NULL,
  department_id  UUID,
  project_id     UUID,
  m01 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m01 >= 0),
  m02 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m02 >= 0),
  m03 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m03 >= 0),
  m04 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m04 >= 0),
  m05 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m05 >= 0),
  m06 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m06 >= 0),
  m07 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m07 >= 0),
  m08 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m08 >= 0),
  m09 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m09 >= 0),
  m10 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m10 >= 0),
  m11 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m11 >= 0),
  m12 NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (m12 >= 0),
  FOREIGN KEY (company_id, budget_id) REFERENCES budget.budgets (company_id, id) ON DELETE CASCADE,
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id),
  FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id),
  CHECK (department_id IS NULL OR project_id IS NULL),
  UNIQUE NULLS NOT DISTINCT (budget_id, account_id, department_id, project_id)
);
CREATE INDEX budget_lines_budget_idx ON budget.lines (budget_id);

-- +goose Down
DROP TABLE budget.lines;
DROP TABLE budget.budgets;
DROP SCHEMA budget;
