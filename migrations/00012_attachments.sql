-- +goose Up
-- Lampiran dokumen (PLT-08, GL-02, AP-04): PDF atau gambar nota yang menempel di transaksi.
-- Berkasnya di folder data (ikut backup); di sini hanya metadata. Lampiran tidak pernah
-- dihapus fisik: "hapus" hanya menyembunyikannya dan tercatat di audit trail.
CREATE TABLE platform.attachments (
  id            UUID PRIMARY KEY,
  company_id    UUID NOT NULL REFERENCES core.companies (id),
  object_type   TEXT NOT NULL CHECK (object_type IN (
                  'journal', 'sales_invoice', 'sales_receipt', 'purchase_bill', 'purchase_payment',
                  'cash_transaction', 'inventory_adjustment', 'project', 'contact')),
  object_id     UUID NOT NULL,
  file_name     TEXT NOT NULL,
  mime          TEXT NOT NULL,
  size_bytes    BIGINT NOT NULL,
  sha256        TEXT NOT NULL,
  storage_path  TEXT NOT NULL,
  uploaded_by   UUID REFERENCES platform.users (id),
  uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ,
  deleted_by    UUID REFERENCES platform.users (id)
);
CREATE INDEX attachments_object_idx ON platform.attachments (company_id, object_type, object_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE platform.attachments;
