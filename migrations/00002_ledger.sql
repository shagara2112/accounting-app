-- +goose Up
-- Buku besar (TDD, bagian Mesin buku besar). Aturan terpenting dijaga basis data, bukan
-- hanya aplikasi: jurnal seimbang saat commit, baris jurnal tidak bisa diubah, dan saldo
-- selalu sama dengan jumlah baris jurnal.
CREATE SCHEMA IF NOT EXISTS ledger;

-- Pengaturan buku per PT. PT tanpa baris di sini memakai tahun buku Januari–Desember.
CREATE TABLE ledger.settings (
  company_id               UUID PRIMARY KEY REFERENCES core.companies (id),
  fiscal_year_start_month  SMALLINT NOT NULL DEFAULT 1 CHECK (fiscal_year_start_month BETWEEN 1 AND 12),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by               UUID REFERENCES platform.users (id)
);

-- Bagan akun. Angka kepala kode (1–9) adalah kelas akun; dari kelas diturunkan saldo
-- normal, letak di laporan, dan apakah akun ditutup ke laba ditahan.
CREATE TABLE ledger.accounts (
  id          UUID PRIMARY KEY,
  company_id  UUID NOT NULL REFERENCES core.companies (id),
  code        TEXT NOT NULL CHECK (code ~ '^[1-9](-[0-9]{1,8}(\.[0-9]{1,4}){0,2})?$'),
  name        TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  class       SMALLINT NOT NULL CHECK (class BETWEEN 1 AND 9),
  parent_id   UUID,
  level       SMALLINT NOT NULL CHECK (level BETWEEN 1 AND 5),
  postable    BOOLEAN NOT NULL,
  active      BOOLEAN NOT NULL DEFAULT true,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by  UUID REFERENCES platform.users (id),
  version     INT NOT NULL DEFAULT 1,
  UNIQUE (company_id, id),
  UNIQUE (company_id, code),
  CHECK (class = substr(code, 1, 1)::smallint),
  CHECK ((level = 1) = (parent_id IS NULL)),
  FOREIGN KEY (company_id, parent_id) REFERENCES ledger.accounts (company_id, id)
);
CREATE INDEX accounts_parent_idx ON ledger.accounts (parent_id);

-- Kode, kelas, induk, dan jenis akun tidak bisa diubah setelah dibuat; akun juga tidak
-- pernah dihapus, hanya dinonaktifkan.
-- +goose StatementBegin
CREATE FUNCTION ledger.guard_account() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'akun tidak bisa dihapus; nonaktifkan saja' USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.id, NEW.company_id, NEW.code, NEW.class, NEW.parent_id, NEW.level, NEW.postable)
     IS DISTINCT FROM (OLD.id, OLD.company_id, OLD.code, OLD.class, OLD.parent_id, OLD.level, OLD.postable) THEN
    RAISE EXCEPTION 'kode, kelas, induk, dan jenis akun tidak bisa diubah' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER accounts_guard
  BEFORE UPDATE OR DELETE ON ledger.accounts
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_account();

-- Tutup buku dua tahap: open → adjusting (hanya jurnal penyesuaian) → locked.
-- Bulan tanpa baris dianggap open.
CREATE TABLE ledger.periods (
  company_id  UUID NOT NULL REFERENCES core.companies (id),
  month       DATE NOT NULL CHECK (extract(day FROM month) = 1),
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'adjusting', 'locked')),
  changed_at  TIMESTAMPTZ,
  changed_by  UUID REFERENCES platform.users (id),
  reason      TEXT,
  PRIMARY KEY (company_id, month)
);

-- Nomor tanpa celah per PT, seri, dan bulan. Nomor diambil di transaksi posting, sehingga
-- nomor dari posting yang gagal ikut di-rollback.
CREATE TABLE ledger.sequences (
  company_id  UUID NOT NULL REFERENCES core.companies (id),
  series      TEXT NOT NULL,
  month       DATE NOT NULL,
  last_no     INT NOT NULL,
  PRIMARY KEY (company_id, series, month)
);

CREATE TABLE ledger.journal_entries (
  id            UUID PRIMARY KEY,
  company_id    UUID NOT NULL REFERENCES core.companies (id),
  number        TEXT NOT NULL,
  kind          TEXT NOT NULL CHECK (kind IN ('umum', 'penyesuaian', 'pembalik', 'penutup')),
  entry_date    DATE NOT NULL,
  memo          TEXT NOT NULL DEFAULT '',
  source_type   TEXT NOT NULL, -- manual, void, auto_reversal; modul lain mis. sales.invoice
  source_id     UUID,
  reversal_of   UUID,
  auto_reverse  BOOLEAN NOT NULL DEFAULT false,
  total         NUMERIC(19,4) NOT NULL CHECK (total > 0),
  status        TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  posted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  posted_by     UUID REFERENCES platform.users (id),
  voided_at     TIMESTAMPTZ,
  voided_by     UUID REFERENCES platform.users (id),
  void_reason   TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, id, entry_date),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, reversal_of) REFERENCES ledger.journal_entries (company_id, id),
  CHECK ((status = 'void') = (voided_at IS NOT NULL)),
  CHECK ((reversal_of IS NOT NULL) = (source_type IN ('void', 'auto_reversal')))
);
CREATE INDEX journal_entries_date_idx ON ledger.journal_entries (company_id, entry_date DESC, id DESC);
CREATE INDEX journal_entries_reversal_idx ON ledger.journal_entries (reversal_of) WHERE reversal_of IS NOT NULL;

-- Jurnal yang sudah diposting hanya boleh berubah satu kali: posted → void.
-- +goose StatementBegin
CREATE FUNCTION ledger.guard_entry() RETURNS trigger
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

CREATE TRIGGER journal_entries_guard
  BEFORE UPDATE OR DELETE ON ledger.journal_entries
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_entry();

CREATE TRIGGER journal_entries_no_truncate
  BEFORE TRUNCATE ON ledger.journal_entries
  FOR EACH STATEMENT EXECUTE FUNCTION platform.reject_change();

CREATE TABLE ledger.journal_lines (
  id          UUID PRIMARY KEY,
  company_id  UUID NOT NULL,
  entry_id    UUID NOT NULL,
  entry_date  DATE NOT NULL, -- salinan tanggal jurnal (dijaga foreign key) untuk indeks buku besar
  line_no     SMALLINT NOT NULL CHECK (line_no > 0),
  account_id  UUID NOT NULL,
  debit       NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (debit >= 0),
  credit      NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (credit >= 0),
  memo        TEXT NOT NULL DEFAULT '',
  UNIQUE (entry_id, line_no),
  CHECK ((debit = 0) <> (credit = 0)),
  FOREIGN KEY (company_id, entry_id, entry_date) REFERENCES ledger.journal_entries (company_id, id, entry_date),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);
CREATE INDEX journal_lines_account_idx ON ledger.journal_lines (company_id, account_id, entry_date);

CREATE TRIGGER journal_lines_append_only
  BEFORE UPDATE OR DELETE ON ledger.journal_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

CREATE TRIGGER journal_lines_no_truncate
  BEFORE TRUNCATE ON ledger.journal_lines
  FOR EACH STATEMENT EXECUTE FUNCTION platform.reject_change();

-- Baris hanya boleh memakai akun detail (bukan akun header/induk).
-- +goose StatementBegin
CREATE FUNCTION ledger.check_line_account() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM ledger.accounts
                 WHERE id = NEW.account_id AND company_id = NEW.company_id AND postable) THEN
    RAISE EXCEPTION 'akun % adalah akun header dan tidak bisa diposting', NEW.account_id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER journal_lines_account
  BEFORE INSERT ON ledger.journal_lines
  FOR EACH ROW EXECUTE FUNCTION ledger.check_line_account();

-- Σ debit = Σ kredit = total, minimal dua baris; diperiksa saat commit (DEFERRABLE),
-- karena jurnal dan baris-barisnya ditulis dengan beberapa perintah.
-- +goose StatementBegin
CREATE FUNCTION ledger.check_entry_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  eid UUID;
  d   NUMERIC;
  c   NUMERIC;
  n   INT;
  t   NUMERIC;
BEGIN
  IF TG_TABLE_NAME = 'journal_entries' THEN
    eid := NEW.id;
  ELSE
    eid := NEW.entry_id;
  END IF;
  SELECT coalesce(sum(debit), 0), coalesce(sum(credit), 0), count(*) INTO d, c, n
    FROM ledger.journal_lines WHERE entry_id = eid;
  SELECT total INTO t FROM ledger.journal_entries WHERE id = eid;
  IF n < 2 OR d <> c OR d <> t THEN
    RAISE EXCEPTION 'jurnal % tidak seimbang: debit %, kredit %, total %, % baris', eid, d, c, t, n
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER journal_entries_balanced
  AFTER INSERT ON ledger.journal_entries DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION ledger.check_entry_balanced();

CREATE CONSTRAINT TRIGGER journal_lines_balanced
  AFTER INSERT ON ledger.journal_lines DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION ledger.check_entry_balanced();

-- Saldo agregat per PT × akun × bulan × jenis jurnal. Dipisah per jenis supaya neraca
-- lajur langsung punya kolom penyesuaian. Diisi trigger, sehingga tidak mungkin terlupa.
CREATE TABLE ledger.balances (
  company_id  UUID NOT NULL,
  account_id  UUID NOT NULL,
  month       DATE NOT NULL,
  kind        TEXT NOT NULL,
  debit       NUMERIC(19,4) NOT NULL DEFAULT 0,
  credit      NUMERIC(19,4) NOT NULL DEFAULT 0,
  PRIMARY KEY (company_id, account_id, month, kind),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);
CREATE INDEX balances_month_idx ON ledger.balances (company_id, month);

-- +goose StatementBegin
CREATE FUNCTION ledger.apply_line_to_balance() RETURNS trigger
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

CREATE TRIGGER journal_lines_to_balances
  AFTER INSERT ON ledger.journal_lines
  FOR EACH ROW EXECUTE FUNCTION ledger.apply_line_to_balance();

-- +goose Down
DROP SCHEMA ledger CASCADE;
