-- +goose Up
-- Revisi klien: jurnal manual boleh diubah selama periodenya belum dikunci. Perubahan tidak
-- menghapus jejak: versi yang digantikan disimpan utuh di journal_revisions (append-only)
-- dan di audit trail. Di luar operasi revisi, jurnal dan barisnya tetap tidak bisa diubah:
-- pengecualian hanya berlaku untuk jurnal yang ditandai lajur.revise_entry di transaksi itu.
ALTER TABLE ledger.journal_entries
  ADD COLUMN revision   INT NOT NULL DEFAULT 0 CHECK (revision >= 0),
  ADD COLUMN revised_at TIMESTAMPTZ,
  ADD COLUMN revised_by UUID REFERENCES platform.users (id);

CREATE TABLE ledger.journal_revisions (
  id          UUID PRIMARY KEY,
  company_id  UUID NOT NULL,
  entry_id    UUID NOT NULL,
  revision    INT NOT NULL, -- versi yang digantikan; 0 = versi saat pertama diposting
  entry_date  DATE NOT NULL,
  memo        TEXT NOT NULL,
  total       NUMERIC(19,4) NOT NULL,
  lines       JSONB NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  revised_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  revised_by  UUID REFERENCES platform.users (id),
  UNIQUE (entry_id, revision),
  FOREIGN KEY (company_id, entry_id) REFERENCES ledger.journal_entries (company_id, id)
);

CREATE TRIGGER journal_revisions_append_only
  BEFORE UPDATE OR DELETE ON ledger.journal_revisions
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

CREATE TRIGGER journal_revisions_no_truncate
  BEFORE TRUNCATE ON ledger.journal_revisions
  FOR EACH STATEMENT EXECUTE FUNCTION platform.reject_change();

-- Jurnal yang sudah diposting hanya boleh: posted → void, atau direvisi (tetap posted, nomor,
-- jenis, dan sumber tidak berubah, versi naik satu) saat ditandai lajur.revise_entry.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.guard_entry() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND OLD.status = 'posted' AND NEW.status = 'void'
     AND (NEW.id, NEW.company_id, NEW.number, NEW.kind, NEW.entry_date, NEW.memo, NEW.source_type,
          NEW.source_id, NEW.reversal_of, NEW.auto_reverse, NEW.total, NEW.posted_at, NEW.posted_by,
          NEW.revision, NEW.revised_at, NEW.revised_by)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.number, OLD.kind, OLD.entry_date, OLD.memo, OLD.source_type,
          OLD.source_id, OLD.reversal_of, OLD.auto_reverse, OLD.total, OLD.posted_at, OLD.posted_by,
          OLD.revision, OLD.revised_at, OLD.revised_by) THEN
    RETURN NEW;
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.status = 'posted' AND NEW.status = 'posted'
     AND current_setting('lajur.revise_entry', true) = OLD.id::text
     AND NEW.revision = OLD.revision + 1
     AND (NEW.id, NEW.company_id, NEW.number, NEW.kind, NEW.source_type, NEW.source_id, NEW.reversal_of,
          NEW.auto_reverse, NEW.posted_at, NEW.posted_by, NEW.voided_at, NEW.voided_by, NEW.void_reason)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.number, OLD.kind, OLD.source_type, OLD.source_id, OLD.reversal_of,
          OLD.auto_reverse, OLD.posted_at, OLD.posted_by, OLD.voided_at, OLD.voided_by, OLD.void_reason) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'jurnal yang sudah diposting tidak bisa diubah atau dihapus; gunakan void'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

-- Baris jurnal hanya boleh dihapus saat jurnalnya sedang direvisi; baris penggantinya ditulis
-- lewat INSERT biasa sehingga semua pemeriksaan (akun detail, seimbang) tetap berlaku.
-- +goose StatementBegin
CREATE FUNCTION ledger.guard_line() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND current_setting('lajur.revise_entry', true) = OLD.entry_id::text THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'baris jurnal yang sudah diposting tidak bisa diubah atau dihapus'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

DROP TRIGGER journal_lines_append_only ON ledger.journal_lines;
CREATE TRIGGER journal_lines_guard
  BEFORE UPDATE OR DELETE ON ledger.journal_lines
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_line();

-- Saldo agregat (per akun dan per proyek) ikut dikurangi saat baris lama dihapus oleh revisi.
-- Sel yang kembali nol dihapus, sama seperti sel yang belum pernah punya baris.
-- +goose StatementBegin
CREATE FUNCTION ledger.remove_line_from_balance() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  k TEXT;
  m DATE := date_trunc('month', OLD.entry_date)::date;
BEGIN
  SELECT kind INTO k FROM ledger.journal_entries WHERE id = OLD.entry_id;
  UPDATE ledger.balances
     SET debit = debit - OLD.debit, credit = credit - OLD.credit
   WHERE company_id = OLD.company_id AND account_id = OLD.account_id AND month = m AND kind = k;
  DELETE FROM ledger.balances
   WHERE company_id = OLD.company_id AND account_id = OLD.account_id AND month = m AND kind = k
     AND debit = 0 AND credit = 0;
  IF OLD.project_id IS NOT NULL THEN
    UPDATE ledger.project_balances
       SET debit = debit - OLD.debit, credit = credit - OLD.credit
     WHERE company_id = OLD.company_id AND project_id = OLD.project_id AND account_id = OLD.account_id AND month = m;
    DELETE FROM ledger.project_balances
     WHERE company_id = OLD.company_id AND project_id = OLD.project_id AND account_id = OLD.account_id AND month = m
       AND debit = 0 AND credit = 0;
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER journal_lines_from_balances
  AFTER DELETE ON ledger.journal_lines
  FOR EACH ROW EXECUTE FUNCTION ledger.remove_line_from_balance();

-- +goose Down
DROP TRIGGER journal_lines_from_balances ON ledger.journal_lines;
DROP FUNCTION ledger.remove_line_from_balance();
DROP TRIGGER journal_lines_guard ON ledger.journal_lines;
DROP FUNCTION ledger.guard_line();
CREATE TRIGGER journal_lines_append_only
  BEFORE UPDATE OR DELETE ON ledger.journal_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.guard_entry() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND OLD.status = 'posted' AND NEW.status = 'void'
     AND (NEW.id, NEW.company_id, NEW.number, NEW.kind, NEW.entry_date, NEW.memo, NEW.source_type,
          NEW.source_id, NEW.reversal_of, NEW.auto_reverse, NEW.total, NEW.posted_at, NEW.posted_by)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.number, OLD.kind, OLD.entry_date, OLD.memo, OLD.source_type,
          OLD.source_id, OLD.reversal_of, OLD.auto_reverse, OLD.total, OLD.posted_at, OLD.posted_by) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'jurnal yang sudah diposting tidak bisa diubah atau dihapus; gunakan void'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

DROP TABLE ledger.journal_revisions;
ALTER TABLE ledger.journal_entries DROP COLUMN revised_by, DROP COLUMN revised_at, DROP COLUMN revision;
