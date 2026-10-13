-- +goose Up
-- Tahap 4: faktur pajak keluaran dan ekspor XML Coretax (TAX-03, TAX-04), profil pajak PT
-- (TAX-05). Status faktur pajak disimpan modul pajak sendiri; data fakturnya tetap milik
-- modul penjualan dan dibaca lewat service-nya.

-- Alamat PT dicetak di faktur pajak; NPWP dan NITKU sudah ada sejak tahap 1.
ALTER TABLE core.companies ADD COLUMN address TEXT NOT NULL DEFAULT '';

-- Isian default XML Coretax per PT. Kode barang/jasa 6 digit boleh 000000 (DJP: opsional,
-- tetapi bila diisi harus sesuai referensi); kode satuan wajib dari daftar referensi DJP.
CREATE TABLE tax.settings (
  company_id  UUID PRIMARY KEY REFERENCES core.companies (id),
  goods_opt   TEXT NOT NULL DEFAULT 'B' CHECK (goods_opt IN ('A', 'B')), -- A barang, B jasa
  goods_code  TEXT NOT NULL DEFAULT '000000' CHECK (goods_code ~ '^[0-9]{6}$'),
  unit_code   TEXT NOT NULL DEFAULT 'UM.0021' CHECK (unit_code ~ '^UM\.[0-9]{4}$'),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by  UUID REFERENCES platform.users (id)
);

-- Riwayat ekspor; XML disimpan supaya bisa diunduh ulang dan diperiksa auditor.
CREATE TABLE tax.export_batches (
  id             UUID PRIMARY KEY,
  company_id     UUID NOT NULL REFERENCES core.companies (id),
  period         DATE NOT NULL CHECK (extract(day FROM period) = 1),
  format         TEXT NOT NULL, -- versi eksportir, mis. coretax-faktur-keluaran/v1
  invoice_count  INT NOT NULL CHECK (invoice_count > 0),
  file_name      TEXT NOT NULL,
  content        BYTEA NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by     UUID REFERENCES platform.users (id),
  UNIQUE (company_id, id)
);

-- Status faktur pajak keluaran per faktur penjualan. Baris dibuat saat faktur pertama kali
-- diubah statusnya; faktur tanpa baris dianggap "siap" dengan kode transaksi default.
CREATE TABLE tax.output_invoices (
  invoice_id          UUID PRIMARY KEY,
  company_id          UUID NOT NULL,
  trx_code            TEXT NOT NULL CHECK (trx_code ~ '^(0[1-9]|10)$'),
  status              TEXT NOT NULL DEFAULT 'ready'
                      CHECK (status IN ('ready', 'exported', 'approved', 'cancelled', 'replaced')),
  tax_invoice_number  TEXT CHECK (tax_invoice_number ~ '^[0-9]{13,20}$'),
  approved_date       DATE,
  last_batch_id       UUID,
  replaced_by         UUID,
  note                TEXT NOT NULL DEFAULT '',
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by          UUID REFERENCES platform.users (id),
  FOREIGN KEY (company_id, invoice_id) REFERENCES sales.invoices (company_id, id),
  FOREIGN KEY (company_id, last_batch_id) REFERENCES tax.export_batches (company_id, id),
  FOREIGN KEY (company_id, replaced_by) REFERENCES sales.invoices (company_id, id),
  CHECK ((status IN ('ready', 'exported')) = (tax_invoice_number IS NULL)),
  CHECK ((status = 'replaced') = (replaced_by IS NOT NULL))
);
CREATE UNIQUE INDEX output_invoices_number_key ON tax.output_invoices (company_id, tax_invoice_number)
  WHERE tax_invoice_number IS NOT NULL;

-- +goose Down
DROP TABLE tax.output_invoices;
DROP TABLE tax.export_batches;
DROP TABLE tax.settings;
ALTER TABLE core.companies DROP COLUMN address;
