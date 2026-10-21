-- +goose Up
-- Pelanggan pemungut PPN (WAPU): instansi pemerintah (kode transaksi 02) atau pemungut selain
-- instansi pemerintah, mis. BUMN (03). PPN fakturnya disetor sendiri oleh pelanggan, sehingga
-- pelunasan mengurangi piutang sebesar PPN itu tanpa uang masuk (PPN dipungut).
ALTER TABLE core.contacts
  ADD COLUMN vat_collector TEXT NOT NULL DEFAULT '' CHECK (vat_collector IN ('', '02', '03'));

-- Faktur mencatat status pemungut pelanggan dan akun PPN keluarannya saat diposting, supaya
-- pelunasan dan retur memakai akun yang sama walau pengaturan akun sistem berubah kemudian.
-- Faktur lama (kosong) memakai akun PPN keluaran.
ALTER TABLE sales.invoices
  ADD COLUMN vat_collector TEXT NOT NULL DEFAULT '' CHECK (vat_collector IN ('', '02', '03')),
  ADD COLUMN vat_account_id UUID,
  ADD FOREIGN KEY (company_id, vat_account_id) REFERENCES ledger.accounts (company_id, id);

-- PPN dipungut pelanggan per faktur yang dilunasi; uang yang diterima = alokasi − PPh − PPN dipungut.
ALTER TABLE sales.receipt_allocations
  ADD COLUMN vat_collected NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (vat_collected >= 0),
  ADD CONSTRAINT receipt_allocations_deductions_check CHECK (withholding + vat_collected <= amount);

-- Pelunasan yang hanya mencatat PPN dipungut (tanpa uang masuk) juga sah.
ALTER TABLE sales.receipts
  ADD COLUMN vat_collected NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (vat_collected >= 0),
  DROP CONSTRAINT receipts_amount_check,
  ADD CONSTRAINT receipts_amount_check CHECK (amount >= 0 AND amount + from_advance + vat_collected > 0);

-- +goose Down
ALTER TABLE sales.receipts
  DROP CONSTRAINT receipts_amount_check,
  DROP COLUMN vat_collected,
  ADD CONSTRAINT receipts_amount_check CHECK (amount >= 0 AND amount + from_advance > 0);
ALTER TABLE sales.receipt_allocations
  DROP CONSTRAINT receipt_allocations_deductions_check,
  DROP COLUMN vat_collected;
ALTER TABLE sales.invoices DROP COLUMN vat_account_id, DROP COLUMN vat_collector;
ALTER TABLE core.contacts DROP COLUMN vat_collector;
