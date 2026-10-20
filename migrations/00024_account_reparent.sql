-- +goose Up
-- Akun boleh dipindah ke induk lain (koreksi pengelompokan bagan akun) selama kelasnya tetap.
-- Penjaga memastikan pohon tetap sah: induk baru adalah akun header di PT dan kelas yang sama,
-- level = level induk + 1 (maks. 5), dan induk bukan akun itu sendiri atau turunannya. Akun
-- kelompok (level 1) tidak punya induk dan tidak bisa dipindah. Turunan akun header yang
-- dipindah ikut digeser levelnya satu per satu dari atas, sehingga setiap baris lolos
-- pemeriksaan yang sama. Kelas dan jenis akun tetap terkunci, dan akun tetap tidak bisa dihapus.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.guard_account() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  p RECORD;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'akun tidak bisa dihapus; nonaktifkan saja' USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.id, NEW.company_id, NEW.class, NEW.postable)
     IS DISTINCT FROM (OLD.id, OLD.company_id, OLD.class, OLD.postable) THEN
    RAISE EXCEPTION 'kelas dan jenis akun tidak bisa diubah' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.code IS DISTINCT FROM OLD.code
     AND (length(OLD.code) = 1 OR left(NEW.code, 1) IS DISTINCT FROM left(OLD.code, 1)) THEN
    RAISE EXCEPTION 'kode akun hanya bisa dikoreksi dengan angka kepala yang sama' USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.parent_id, NEW.level) IS DISTINCT FROM (OLD.parent_id, OLD.level) THEN
    IF OLD.parent_id IS NULL OR NEW.parent_id IS NULL THEN
      RAISE EXCEPTION 'akun kelompok tidak bisa dipindah' USING ERRCODE = 'restrict_violation';
    END IF;
    SELECT company_id, class, postable, level INTO p FROM ledger.accounts WHERE id = NEW.parent_id;
    IF NOT FOUND OR p.company_id <> NEW.company_id OR p.postable OR p.class <> NEW.class
       OR NEW.level <> p.level + 1 OR NEW.level > 5 THEN
      RAISE EXCEPTION 'induk akun tidak sah' USING ERRCODE = 'restrict_violation';
    END IF;
    IF NEW.parent_id IS DISTINCT FROM OLD.parent_id AND EXISTS (
      WITH RECURSIVE up AS (
        SELECT id, parent_id FROM ledger.accounts WHERE id = NEW.parent_id
        UNION ALL
        SELECT a.id, a.parent_id FROM ledger.accounts a JOIN up ON a.id = up.parent_id
      )
      SELECT 1 FROM up WHERE id = NEW.id
    ) THEN
      RAISE EXCEPTION 'induk akun tidak boleh akun itu sendiri atau turunannya' USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.guard_account() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'akun tidak bisa dihapus; nonaktifkan saja' USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.id, NEW.company_id, NEW.class, NEW.parent_id, NEW.level, NEW.postable)
     IS DISTINCT FROM (OLD.id, OLD.company_id, OLD.class, OLD.parent_id, OLD.level, OLD.postable) THEN
    RAISE EXCEPTION 'kelas, induk, dan jenis akun tidak bisa diubah' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.code IS DISTINCT FROM OLD.code
     AND (length(OLD.code) = 1 OR left(NEW.code, 1) IS DISTINCT FROM left(OLD.code, 1)) THEN
    RAISE EXCEPTION 'kode akun hanya bisa dikoreksi dengan angka kepala yang sama' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
