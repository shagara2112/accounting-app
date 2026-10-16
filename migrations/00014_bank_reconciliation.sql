-- +goose Up
-- Rekonsiliasi bank manual (CB-04, CB-05): saldo rekening koran per tanggal, dan baris buku
-- besar rekening itu yang dicentang cocok dengan rekening koran.
CREATE TABLE cash.reconciliations (
  id                 UUID PRIMARY KEY,
  company_id         UUID NOT NULL,
  account_id         UUID NOT NULL,
  statement_date     DATE NOT NULL,
  statement_balance  NUMERIC(19,4) NOT NULL,
  opening_balance    NUMERIC(19,4) NOT NULL, -- saldo rekening koran di rekonsiliasi sebelumnya
  status             TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
  created_by         UUID REFERENCES platform.users (id),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  closed_by          UUID REFERENCES platform.users (id),
  closed_at          TIMESTAMPTZ,
  version            INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, account_id, statement_date),
  FOREIGN KEY (company_id, account_id) REFERENCES cash.accounts (company_id, account_id),
  CHECK ((status = 'closed') = (closed_at IS NOT NULL))
);
-- Paling banyak satu rekonsiliasi terbuka per rekening.
CREATE UNIQUE INDEX reconciliations_open_idx ON cash.reconciliations (company_id, account_id) WHERE status = 'open';

CREATE TABLE cash.recon_items (
  reconciliation_id  UUID NOT NULL,
  company_id         UUID NOT NULL,
  journal_line_id    UUID NOT NULL REFERENCES ledger.journal_lines (id),
  PRIMARY KEY (reconciliation_id, journal_line_id),
  -- Satu baris buku besar hanya bisa dicocokkan di satu rekonsiliasi.
  UNIQUE (company_id, journal_line_id),
  FOREIGN KEY (company_id, reconciliation_id) REFERENCES cash.reconciliations (company_id, id) ON DELETE CASCADE
);

-- Rekonsiliasi yang sudah ditutup tidak bisa diubah, kecuali dibuka kembali.
-- +goose StatementBegin
CREATE FUNCTION cash.guard_recon_item() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s TEXT;
BEGIN
  SELECT status INTO s FROM cash.reconciliations WHERE id = COALESCE(NEW.reconciliation_id, OLD.reconciliation_id);
  IF s = 'closed' THEN
    RAISE EXCEPTION 'rekonsiliasi yang sudah ditutup tidak bisa diubah' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END
$$;
-- +goose StatementEnd

CREATE TRIGGER recon_items_guard
  BEFORE INSERT OR UPDATE OR DELETE ON cash.recon_items
  FOR EACH ROW EXECUTE FUNCTION cash.guard_recon_item();

-- +goose Down
DROP TABLE cash.recon_items;
DROP FUNCTION cash.guard_recon_item();
DROP TABLE cash.reconciliations;
