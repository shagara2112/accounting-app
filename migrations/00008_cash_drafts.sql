-- +goose Up
-- Draf transaksi kas: staf mencatat mutasi kas/bank tanpa akun lawan; akuntan melengkapi akun
-- lalu memposting. Nomor BKM/BKK dan jurnal baru dibuat saat diposting, sehingga draf belum
-- mengubah saldo kas/bank di buku besar.
ALTER TABLE cash.transactions DROP CONSTRAINT transactions_status_check;
ALTER TABLE cash.transactions
  ALTER COLUMN number DROP NOT NULL,
  ALTER COLUMN journal_entry_id DROP NOT NULL,
  ALTER COLUMN status SET DEFAULT 'draft',
  ADD COLUMN posted_at TIMESTAMPTZ,
  ADD COLUMN posted_by UUID REFERENCES platform.users (id),
  ADD COLUMN version INT NOT NULL DEFAULT 1,
  ADD CONSTRAINT transactions_status_check CHECK (status IN ('draft', 'posted', 'void')),
  ADD CHECK ((status = 'draft') = (number IS NULL)),
  ADD CHECK ((status = 'draft') = (journal_entry_id IS NULL));

-- Transaksi lama langsung diposting saat dicatat.
ALTER TABLE cash.transactions DISABLE TRIGGER cash_transactions_guard;
UPDATE cash.transactions SET posted_at = created_at, posted_by = created_by;
ALTER TABLE cash.transactions ENABLE TRIGGER cash_transactions_guard;

-- Baris draf boleh belum punya akun, dan boleh diubah atau dihapus selama transaksinya draf.
ALTER TABLE cash.transaction_lines ALTER COLUMN account_id DROP NOT NULL;
DROP TRIGGER cash_transaction_lines_append_only ON cash.transaction_lines;

-- +goose StatementBegin
CREATE FUNCTION cash.guard_transaction_line() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  s TEXT;
BEGIN
  SELECT status INTO s FROM cash.transactions WHERE id = COALESCE(NEW.transaction_id, OLD.transaction_id);
  IF s IS NOT NULL AND s <> 'draft' THEN
    RAISE EXCEPTION 'baris transaksi kas yang sudah diposting tidak bisa diubah' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN COALESCE(NEW, OLD);
END
$$;
-- +goose StatementEnd

CREATE TRIGGER cash_transaction_lines_guard
  BEFORE INSERT OR UPDATE OR DELETE ON cash.transaction_lines
  FOR EACH ROW EXECUTE FUNCTION cash.guard_transaction_line();

-- Transaksi hanya boleh diposting bila semua barisnya sudah punya akun.
-- +goose StatementBegin
CREATE FUNCTION cash.check_posting() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'draft' AND NEW.status = 'posted'
     AND EXISTS (SELECT 1 FROM cash.transaction_lines WHERE transaction_id = NEW.id AND account_id IS NULL) THEN
    RAISE EXCEPTION 'transaksi kas % masih punya baris tanpa akun', NEW.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER cash_transactions_posting
  BEFORE UPDATE ON cash.transactions
  FOR EACH ROW EXECUTE FUNCTION cash.check_posting();

CREATE INDEX cash_transactions_drafts_idx ON cash.transactions (company_id) WHERE status = 'draft';

-- +goose Down
DROP INDEX cash.cash_transactions_drafts_idx;
DROP TRIGGER cash_transactions_posting ON cash.transactions;
DROP FUNCTION cash.check_posting();
DROP TRIGGER cash_transaction_lines_guard ON cash.transaction_lines;
DROP FUNCTION cash.guard_transaction_line();
DELETE FROM cash.transaction_lines WHERE transaction_id IN (SELECT id FROM cash.transactions WHERE status = 'draft');
DELETE FROM cash.transactions WHERE status = 'draft';
CREATE TRIGGER cash_transaction_lines_append_only
  BEFORE UPDATE OR DELETE ON cash.transaction_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();
ALTER TABLE cash.transaction_lines ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE cash.transactions DROP CONSTRAINT transactions_status_check;
ALTER TABLE cash.transactions
  DROP COLUMN version, DROP COLUMN posted_by, DROP COLUMN posted_at,
  ALTER COLUMN status SET DEFAULT 'posted',
  ALTER COLUMN journal_entry_id SET NOT NULL,
  ALTER COLUMN number SET NOT NULL,
  ADD CONSTRAINT transactions_status_check CHECK (status IN ('posted', 'void'));
