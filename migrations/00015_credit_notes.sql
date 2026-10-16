-- +goose Up
-- Nota kredit dan retur penjualan (AR-05), nota debit dan retur pembelian (AP-05). Nota selalu
-- merujuk faktur atau tagihan asalnya dan langsung diposting saat disimpan; koreksi lewat void.
-- Nilainya mengurangi sisa dokumen asal; kelebihannya (dokumen asal sudah lunas) menjadi uang
-- muka pelanggan atau uang muka pembelian.

CREATE TABLE sales.credit_notes (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  number            TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  customer_id       UUID NOT NULL,
  invoice_id        UUID NOT NULL,
  note_date         DATE NOT NULL,
  memo              TEXT NOT NULL DEFAULT '',
  subtotal          NUMERIC(19,4) NOT NULL,
  discount          NUMERIC(19,4) NOT NULL,
  dpp               NUMERIC(19,4) NOT NULL,
  other_dpp         NUMERIC(19,4) NOT NULL,
  vat               NUMERIC(19,4) NOT NULL,
  total             NUMERIC(19,4) NOT NULL CHECK (total > 0),
  applied           NUMERIC(19,4) NOT NULL CHECK (applied >= 0), -- mengurangi sisa piutang faktur
  journal_entry_id  UUID NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by        UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, customer_id) REFERENCES core.contacts (company_id, id),
  FOREIGN KEY (company_id, invoice_id) REFERENCES sales.invoices (company_id, id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK (applied <= total)
);
CREATE INDEX credit_notes_date_idx ON sales.credit_notes (company_id, note_date DESC, id DESC);
CREATE INDEX credit_notes_invoice_idx ON sales.credit_notes (invoice_id);

CREATE TABLE sales.credit_note_lines (
  id               UUID PRIMARY KEY,
  company_id       UUID NOT NULL,
  credit_note_id   UUID NOT NULL,
  line_no          SMALLINT NOT NULL CHECK (line_no > 0),
  invoice_line_id  UUID NOT NULL REFERENCES sales.invoice_lines (id),
  description      TEXT NOT NULL DEFAULT '',
  account_id       UUID NOT NULL,
  product_id       UUID,
  qty              NUMERIC(19,4) NOT NULL CHECK (qty > 0),
  unit_price       NUMERIC(19,4) NOT NULL CHECK (unit_price >= 0),
  discount_pct     NUMERIC(19,4) NOT NULL DEFAULT 0,
  discount         NUMERIC(19,4) NOT NULL DEFAULT 0,
  net              NUMERIC(19,4) NOT NULL,
  tax_code         TEXT NOT NULL,
  other_dpp        NUMERIC(19,4) NOT NULL DEFAULT 0,
  vat              NUMERIC(19,4) NOT NULL DEFAULT 0,
  restock          BOOLEAN NOT NULL DEFAULT false, -- barang kembali ke stok
  UNIQUE (credit_note_id, line_no),
  FOREIGN KEY (company_id, credit_note_id) REFERENCES sales.credit_notes (company_id, id),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id),
  CHECK (NOT restock OR product_id IS NOT NULL)
);
CREATE INDEX credit_note_lines_invoice_line_idx ON sales.credit_note_lines (invoice_line_id);

CREATE TRIGGER credit_notes_guard
  BEFORE UPDATE OR DELETE ON sales.credit_notes
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER credit_note_lines_append_only
  BEFORE UPDATE OR DELETE ON sales.credit_note_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

CREATE TABLE purchase.debit_notes (
  id                UUID PRIMARY KEY,
  company_id        UUID NOT NULL REFERENCES core.companies (id),
  number            TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'void')),
  supplier_id       UUID NOT NULL,
  bill_id           UUID NOT NULL,
  note_date         DATE NOT NULL,
  memo              TEXT NOT NULL DEFAULT '',
  subtotal          NUMERIC(19,4) NOT NULL,
  discount          NUMERIC(19,4) NOT NULL,
  dpp               NUMERIC(19,4) NOT NULL,
  other_dpp         NUMERIC(19,4) NOT NULL,
  vat               NUMERIC(19,4) NOT NULL,
  withholding       NUMERIC(19,4) NOT NULL DEFAULT 0 CHECK (withholding >= 0), -- PPh potong yang batal
  total             NUMERIC(19,4) NOT NULL CHECK (total > 0),
  payable           NUMERIC(19,4) NOT NULL, -- total − PPh = pengurang utang ke pemasok
  applied           NUMERIC(19,4) NOT NULL CHECK (applied >= 0), -- mengurangi sisa utang tagihan
  journal_entry_id  UUID NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by        UUID REFERENCES platform.users (id),
  voided_at         TIMESTAMPTZ,
  voided_by         UUID REFERENCES platform.users (id),
  void_reason       TEXT,
  UNIQUE (company_id, id),
  UNIQUE (company_id, number),
  FOREIGN KEY (company_id, supplier_id) REFERENCES core.contacts (company_id, id),
  FOREIGN KEY (company_id, bill_id) REFERENCES purchase.bills (company_id, id),
  FOREIGN KEY (company_id, journal_entry_id) REFERENCES ledger.journal_entries (company_id, id),
  CHECK (applied <= payable)
);
CREATE INDEX debit_notes_date_idx ON purchase.debit_notes (company_id, note_date DESC, id DESC);
CREATE INDEX debit_notes_bill_idx ON purchase.debit_notes (bill_id);

CREATE TABLE purchase.debit_note_lines (
  id             UUID PRIMARY KEY,
  company_id     UUID NOT NULL,
  debit_note_id  UUID NOT NULL,
  line_no        SMALLINT NOT NULL CHECK (line_no > 0),
  bill_line_id   UUID NOT NULL REFERENCES purchase.bill_lines (id),
  description    TEXT NOT NULL DEFAULT '',
  account_id     UUID NOT NULL,
  product_id     UUID,
  project_id     UUID,
  qty            NUMERIC(19,4) NOT NULL CHECK (qty > 0),
  unit_price     NUMERIC(19,4) NOT NULL CHECK (unit_price >= 0),
  discount_pct   NUMERIC(19,4) NOT NULL DEFAULT 0,
  discount       NUMERIC(19,4) NOT NULL DEFAULT 0,
  net            NUMERIC(19,4) NOT NULL,
  tax_code       TEXT NOT NULL,
  other_dpp      NUMERIC(19,4) NOT NULL DEFAULT 0,
  vat            NUMERIC(19,4) NOT NULL DEFAULT 0,
  UNIQUE (debit_note_id, line_no),
  FOREIGN KEY (company_id, debit_note_id) REFERENCES purchase.debit_notes (company_id, id),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id),
  FOREIGN KEY (company_id, product_id) REFERENCES inventory.products (company_id, id),
  FOREIGN KEY (company_id, project_id) REFERENCES project.projects (company_id, id)
);
CREATE INDEX debit_note_lines_bill_line_idx ON purchase.debit_note_lines (bill_line_id);

CREATE TRIGGER debit_notes_guard
  BEFORE UPDATE OR DELETE ON purchase.debit_notes
  FOR EACH ROW EXECUTE FUNCTION platform.guard_posted_document();

CREATE TRIGGER debit_note_lines_append_only
  BEFORE UPDATE OR DELETE ON purchase.debit_note_lines
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

-- Retur penjualan mengembalikan barang ke stok dengan biaya saat barang itu keluar:
-- penilaian "ratio" = nilai pergerakan cost_of × qty / qty pergerakan itu.
ALTER TABLE inventory.movements DROP CONSTRAINT movements_valuation_check;
ALTER TABLE inventory.movements
  ADD CONSTRAINT movements_valuation_check CHECK (valuation IN ('fixed', 'average', 'mirror', 'ratio')),
  ADD COLUMN cost_of UUID,
  ADD CONSTRAINT movements_cost_of_fkey FOREIGN KEY (company_id, cost_of) REFERENCES inventory.movements (company_id, id),
  ADD CONSTRAINT movements_cost_of_check CHECK ((valuation = 'ratio') = (cost_of IS NOT NULL));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION inventory.guard_movement() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE'
     AND (NEW.id, NEW.company_id, NEW.product_id, NEW.seq, NEW.move_date, NEW.qty, NEW.valuation,
          NEW.fixed_value, NEW.mirror_of, NEW.cost_of, NEW.owned, NEW.counter_account_id, NEW.project_id,
          NEW.source_type, NEW.source_id, NEW.source_number, NEW.memo)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.product_id, OLD.seq, OLD.move_date, OLD.qty, OLD.valuation,
          OLD.fixed_value, OLD.mirror_of, OLD.cost_of, OLD.owned, OLD.counter_account_id, OLD.project_id,
          OLD.source_type, OLD.source_id, OLD.source_number, OLD.memo) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'pergerakan stok tidak bisa diubah atau dihapus; gunakan void dokumennya'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

-- Nota kredit dan nota debit antar-PT ikut dicerminkan (ME-03).
ALTER TABLE consol.ic_links
  DROP CONSTRAINT ic_links_source_type_check,
  DROP CONSTRAINT ic_links_target_type_check,
  ADD CONSTRAINT ic_links_source_type_check
    CHECK (source_type IN ('sales.invoice', 'purchase.bill', 'sales.credit_note', 'purchase.debit_note')),
  ADD CONSTRAINT ic_links_target_type_check
    CHECK (target_type IN ('sales.invoice', 'purchase.bill', 'sales.credit_note', 'purchase.debit_note'));

ALTER TABLE platform.attachments DROP CONSTRAINT attachments_object_type_check;
ALTER TABLE platform.attachments ADD CONSTRAINT attachments_object_type_check CHECK (object_type IN (
  'journal', 'sales_invoice', 'sales_receipt', 'sales_credit_note', 'purchase_bill', 'purchase_payment',
  'purchase_debit_note', 'cash_transaction', 'inventory_adjustment', 'project', 'contact'));

-- +goose Down
DELETE FROM platform.attachments WHERE object_type IN ('sales_credit_note', 'purchase_debit_note');
ALTER TABLE platform.attachments DROP CONSTRAINT attachments_object_type_check;
ALTER TABLE platform.attachments ADD CONSTRAINT attachments_object_type_check CHECK (object_type IN (
  'journal', 'sales_invoice', 'sales_receipt', 'purchase_bill', 'purchase_payment',
  'cash_transaction', 'inventory_adjustment', 'project', 'contact'));

ALTER TABLE consol.ic_links
  DROP CONSTRAINT ic_links_source_type_check,
  DROP CONSTRAINT ic_links_target_type_check,
  ADD CONSTRAINT ic_links_source_type_check CHECK (source_type IN ('sales.invoice', 'purchase.bill')),
  ADD CONSTRAINT ic_links_target_type_check CHECK (target_type IN ('sales.invoice', 'purchase.bill'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION inventory.guard_movement() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE'
     AND (NEW.id, NEW.company_id, NEW.product_id, NEW.seq, NEW.move_date, NEW.qty, NEW.valuation,
          NEW.fixed_value, NEW.mirror_of, NEW.owned, NEW.counter_account_id, NEW.project_id,
          NEW.source_type, NEW.source_id, NEW.source_number, NEW.memo)
     IS NOT DISTINCT FROM
         (OLD.id, OLD.company_id, OLD.product_id, OLD.seq, OLD.move_date, OLD.qty, OLD.valuation,
          OLD.fixed_value, OLD.mirror_of, OLD.owned, OLD.counter_account_id, OLD.project_id,
          OLD.source_type, OLD.source_id, OLD.source_number, OLD.memo) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'pergerakan stok tidak bisa diubah atau dihapus; gunakan void dokumennya'
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

ALTER TABLE inventory.movements
  DROP CONSTRAINT movements_cost_of_check,
  DROP CONSTRAINT movements_cost_of_fkey,
  DROP COLUMN cost_of,
  DROP CONSTRAINT movements_valuation_check,
  ADD CONSTRAINT movements_valuation_check CHECK (valuation IN ('fixed', 'average', 'mirror'));

DROP TABLE purchase.debit_note_lines;
DROP TABLE purchase.debit_notes;
DROP TABLE sales.credit_note_lines;
DROP TABLE sales.credit_notes;
