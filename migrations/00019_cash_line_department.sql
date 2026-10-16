-- +goose Up
-- Departemen per baris transaksi kas (revisi klien): satu pengeluaran bisa dibagi ke beberapa
-- departemen, mis. listrik kantor untuk operasional dan pemasaran. Baris tanpa departemen
-- memakai departemen kepala transaksi.
ALTER TABLE cash.transaction_lines
  ADD COLUMN department_id UUID,
  ADD FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id);

-- +goose Down
ALTER TABLE cash.transaction_lines DROP COLUMN department_id;
