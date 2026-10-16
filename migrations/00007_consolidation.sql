-- +goose Up
-- Tahap 7: transaksi antar-PT dengan dokumen cerminan (ME-03) dan konsolidasi grup (ME-04).
-- Konsolidasi MVP: PT berkepemilikan 100%, rupiah, satu grup per instalasi.

-- Kontak yang mewakili PT lain dalam grup. Akun default dipakai saat dokumen cerminan dibuat
-- di PT ini: beban/aset untuk tagihan cerminan, pendapatan untuk faktur cerminan.
ALTER TABLE core.contacts
  ADD COLUMN linked_company_id UUID REFERENCES core.companies (id),
  ADD COLUMN ic_expense_account_id UUID,
  ADD COLUMN ic_revenue_account_id UUID,
  ADD FOREIGN KEY (company_id, ic_expense_account_id) REFERENCES ledger.accounts (company_id, id),
  ADD FOREIGN KEY (company_id, ic_revenue_account_id) REFERENCES ledger.accounts (company_id, id),
  ADD CHECK (linked_company_id IS DISTINCT FROM company_id);
CREATE UNIQUE INDEX contacts_linked_idx ON core.contacts (company_id, linked_company_id)
  WHERE linked_company_id IS NOT NULL;

CREATE SCHEMA IF NOT EXISTS consol;

-- Pasangan dokumen antar-PT: faktur di PT penjual ↔ tagihan di PT pembeli. Satu dokumen hanya
-- bisa punya satu pasangan.
CREATE TABLE consol.ic_links (
  id                 UUID PRIMARY KEY,
  source_company_id  UUID NOT NULL REFERENCES core.companies (id),
  source_type        TEXT NOT NULL CHECK (source_type IN ('sales.invoice', 'purchase.bill')),
  source_id          UUID NOT NULL,
  target_company_id  UUID NOT NULL REFERENCES core.companies (id),
  target_type        TEXT NOT NULL CHECK (target_type IN ('sales.invoice', 'purchase.bill')),
  target_id          UUID NOT NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by         UUID REFERENCES platform.users (id),
  UNIQUE (source_type, source_id),
  UNIQUE (target_type, target_id),
  CHECK (source_company_id <> target_company_id),
  CHECK (source_type <> target_type)
);

CREATE TRIGGER ic_links_append_only
  BEFORE UPDATE OR DELETE ON consol.ic_links
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- Bagan akun grup. Disusun dari gabungan kode akun semua PT (kode yang sama di semua template),
-- lalu bisa diganti nama atau ditambah akun baru untuk memisahkan akun yang berbeda isi.
CREATE TABLE consol.group_accounts (
  code         TEXT PRIMARY KEY CHECK (code ~ '^[1-9](-[0-9]{1,8}(\.[0-9]{1,4}){0,2})?$'),
  name         TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
  parent_code  TEXT REFERENCES consol.group_accounts (code) DEFERRABLE INITIALLY DEFERRED,
  postable     BOOLEAN NOT NULL,
  class        SMALLINT NOT NULL CHECK (class BETWEEN 1 AND 9),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by   UUID REFERENCES platform.users (id),
  CHECK (class = substr(code, 1, 1)::smallint),
  CHECK ((parent_code IS NULL) = (code ~ '^[1-9]$'))
);

-- Pemetaan akun PT ke akun grup. Akun tanpa baris di sini masuk ke akun grup berkode sama.
CREATE TABLE consol.account_map (
  company_id  UUID NOT NULL,
  account_id  UUID NOT NULL,
  group_code  TEXT NOT NULL REFERENCES consol.group_accounts (code),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by  UUID REFERENCES platform.users (id),
  PRIMARY KEY (company_id, account_id),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

-- +goose Down
DROP SCHEMA consol CASCADE;
DROP INDEX core.contacts_linked_idx;
ALTER TABLE core.contacts
  DROP COLUMN ic_revenue_account_id, DROP COLUMN ic_expense_account_id, DROP COLUMN linked_company_id;
