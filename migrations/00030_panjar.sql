-- +goose Up
-- Panjar unit (revisi klien): kepala unit mengajukan kebutuhan unitnya (departemen) sebelum
-- membeli; finance menyetujui (boleh dengan nominal lebih kecil) atau menolak. Pembayarannya
-- tetap dicatat finance seperti biasa di Kas dan bank atau Pembelian; pengajuan yang disetujui
-- bisa ditandai sudah dibayar dengan rujukan bukti pembayarannya.
CREATE SCHEMA IF NOT EXISTS panjar;

CREATE TABLE panjar.requests (
  id               UUID PRIMARY KEY,
  company_id       UUID NOT NULL REFERENCES core.companies (id),
  number           TEXT NOT NULL,
  department_id    UUID NOT NULL,
  request_date     DATE NOT NULL,
  needed_date      DATE,
  purpose          TEXT NOT NULL CHECK (length(btrim(purpose)) BETWEEN 1 AND 300),
  total            NUMERIC(19,4) NOT NULL CHECK (total > 0),
  status           TEXT NOT NULL DEFAULT 'diajukan'
                   CHECK (status IN ('diajukan', 'disetujui', 'ditolak', 'dibatalkan', 'dibayar')),
  approved_amount  NUMERIC(19,4) CHECK (approved_amount > 0),
  decided_at       TIMESTAMPTZ,
  decided_by       UUID REFERENCES platform.users (id),
  decision_note    TEXT NOT NULL DEFAULT '',
  paid_at          DATE,
  paid_ref         TEXT NOT NULL DEFAULT '',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by       UUID NOT NULL REFERENCES platform.users (id),
  version          INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id),
  CHECK (approved_amount IS NULL OR approved_amount <= total),
  CHECK ((status IN ('disetujui', 'dibayar')) = (approved_amount IS NOT NULL)),
  CHECK ((status = 'dibayar') = (paid_at IS NOT NULL))
);
CREATE INDEX panjar_requests_list_idx ON panjar.requests (company_id, request_date DESC, id DESC);
CREATE INDEX panjar_requests_dept_idx ON panjar.requests (company_id, department_id, status);

CREATE TABLE panjar.lines (
  id           UUID PRIMARY KEY,
  company_id   UUID NOT NULL,
  request_id   UUID NOT NULL,
  line_no      SMALLINT NOT NULL CHECK (line_no > 0),
  description  TEXT NOT NULL CHECK (length(btrim(description)) BETWEEN 1 AND 200),
  account_id   UUID,   -- akun beban perkiraan, opsional
  amount       NUMERIC(19,4) NOT NULL CHECK (amount > 0),
  UNIQUE (request_id, line_no),
  FOREIGN KEY (company_id, request_id) REFERENCES panjar.requests (company_id, id) ON DELETE CASCADE,
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

-- +goose Down
DROP TABLE panjar.lines;
DROP TABLE panjar.requests;
DROP SCHEMA panjar;
