-- +goose Up
-- TAX-07: faktur pajak masukan (nomor dan tanggal faktur pemasok, dikreditkan atau tidak, masa
-- pengkreditan, dan pencocokan dengan daftar pajak masukan Coretax).
-- TAX-08: bukti potong PPh 23, 4(2), dan 26 dari tagihan pemasok, diekspor ke XML BPPU dan BPNR
-- Coretax. Data tagihannya tetap milik modul pembelian; di sini hanya status pajaknya.

-- PPh 26 untuk pemasok luar negeri.
ALTER TABLE tax.tax_codes DROP CONSTRAINT tax_codes_article_check;
ALTER TABLE tax.tax_codes ADD CONSTRAINT tax_codes_article_check CHECK (article IN ('23', '4(2)', '26'));
INSERT INTO tax.tax_codes (code, kind, name, rate_bp, dpp_num, dpp_den, article, valid_from, valid_to) VALUES
  ('PPH26', 'pph', 'PPh 26 luar negeri 20%', 2000, 1, 1, '26', '2009-01-01', NULL);

-- Lawan transaksi luar negeri: kode negara ISO 3166 alfa-3 dan nomor identitas pajak di
-- negaranya (TIN), dipakai bukti potong PPh 26.
ALTER TABLE core.contacts
  ADD COLUMN country_code TEXT NOT NULL DEFAULT 'IDN' CHECK (country_code ~ '^[A-Z]{3}$'),
  ADD COLUMN foreign_tin TEXT NOT NULL DEFAULT '' CHECK (length(foreign_tin) <= 50);

-- Kode objek pajak bawaan per kode PPh, mis. {"PPH42-SEWA": "28-403-02"}.
ALTER TABLE tax.settings ADD COLUMN withholding_objects JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Batch ekspor juga dipakai bukti potong.
ALTER TABLE tax.export_batches ADD COLUMN kind TEXT NOT NULL DEFAULT 'faktur' CHECK (kind IN ('faktur', 'bppu', 'bpnr'));

-- Status pajak masukan per tagihan ber-PPN. Tagihan tanpa baris dianggap dikreditkan pada
-- masa tagihannya, sehingga rekap PPN tetap sama sampai staf pajak mengubahnya.
CREATE TABLE tax.input_invoices (
  bill_id             UUID PRIMARY KEY,
  company_id          UUID NOT NULL,
  tax_invoice_number  TEXT CHECK (tax_invoice_number ~ '^[0-9]{13,20}$'),
  tax_invoice_date    DATE,
  creditable          BOOLEAN NOT NULL DEFAULT true,
  credit_period       DATE CHECK (extract(day FROM credit_period) = 1),
  reclass_entry_id    UUID, -- jurnal PPN masukan tidak dikreditkan ke akun biayanya
  coretax_vat         NUMERIC(19,4), -- PPN menurut daftar pajak masukan Coretax terakhir
  coretax_checked_at  TIMESTAMPTZ,
  note                TEXT NOT NULL DEFAULT '',
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by          UUID REFERENCES platform.users (id),
  FOREIGN KEY (company_id, bill_id) REFERENCES purchase.bills (company_id, id),
  FOREIGN KEY (company_id, reclass_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK ((tax_invoice_number IS NULL) = (tax_invoice_date IS NULL)),
  CHECK (creditable OR credit_period IS NULL),
  CHECK (NOT creditable OR reclass_entry_id IS NULL)
);
CREATE UNIQUE INDEX input_invoices_number_key ON tax.input_invoices (company_id, tax_invoice_number)
  WHERE tax_invoice_number IS NOT NULL;

-- Bukti potong per tagihan yang memotong PPh. Tagihan tanpa baris dianggap siap dengan isian
-- bawaan (kode objek pajak dari pengaturan, dokumen = surat tagihan pemasok).
CREATE TABLE tax.withholding_slips (
  bill_id           UUID PRIMARY KEY,
  company_id        UUID NOT NULL,
  tax_object_code   TEXT CHECK (tax_object_code ~ '^[0-9]{2}-[0-9]{3}-[0-9]{2}$'),
  facility          TEXT NOT NULL DEFAULT 'N/A',
  facility_number   TEXT NOT NULL DEFAULT '',
  deemed            NUMERIC(7,4) CHECK (deemed > 0 AND deemed <= 100), -- norma PPh 26 (%)
  document          TEXT NOT NULL DEFAULT 'CommercialInvoice',
  document_number   TEXT NOT NULL DEFAULT '',
  document_date     DATE,
  withholding_date  DATE,
  status            TEXT NOT NULL DEFAULT 'ready' CHECK (status IN ('ready', 'exported', 'approved', 'cancelled')),
  slip_number       TEXT CHECK (length(slip_number) BETWEEN 1 AND 40),
  approved_date     DATE,
  exported_base     NUMERIC(19,4),
  exported_amount   NUMERIC(19,4),
  last_batch_id     UUID,
  note              TEXT NOT NULL DEFAULT '',
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by        UUID REFERENCES platform.users (id),
  FOREIGN KEY (company_id, bill_id) REFERENCES purchase.bills (company_id, id),
  FOREIGN KEY (company_id, last_batch_id) REFERENCES tax.export_batches (company_id, id),
  CHECK ((status IN ('approved', 'cancelled')) = (slip_number IS NOT NULL))
);
CREATE UNIQUE INDEX withholding_slips_number_key ON tax.withholding_slips (company_id, slip_number)
  WHERE slip_number IS NOT NULL;

-- +goose Down
DROP TABLE tax.withholding_slips;
DROP TABLE tax.input_invoices;
ALTER TABLE tax.export_batches DROP COLUMN kind;
ALTER TABLE tax.settings DROP COLUMN withholding_objects;
ALTER TABLE core.contacts DROP COLUMN foreign_tin, DROP COLUMN country_code;
DELETE FROM tax.tax_codes WHERE code = 'PPH26';
ALTER TABLE tax.tax_codes DROP CONSTRAINT tax_codes_article_check;
ALTER TABLE tax.tax_codes ADD CONSTRAINT tax_codes_article_check CHECK (article IN ('23', '4(2)'));
