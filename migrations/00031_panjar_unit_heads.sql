-- +goose Up
-- Unit (departemen) yang dikelola setiap kepala unit, ditetapkan Manager atau Pemilik. Pengaju
-- panjar tanpa izin lihat semua atau memutuskan hanya bisa mengajukan untuk unit ini dan melihat
-- pengajuan unit ini (ditambah pengajuannya sendiri). Penetapan ikut terhapus bila akses pengguna
-- ke PT dicabut.
CREATE TABLE panjar.unit_heads (
  company_id     UUID NOT NULL,
  user_id        UUID NOT NULL,
  department_id  UUID NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by     UUID REFERENCES platform.users (id),
  PRIMARY KEY (company_id, user_id, department_id),
  FOREIGN KEY (user_id, company_id) REFERENCES core.user_company_roles (user_id, company_id) ON DELETE CASCADE,
  FOREIGN KEY (company_id, department_id) REFERENCES core.departments (company_id, id)
);
CREATE INDEX panjar_unit_heads_dept_idx ON panjar.unit_heads (company_id, department_id);

-- +goose Down
DROP TABLE panjar.unit_heads;
