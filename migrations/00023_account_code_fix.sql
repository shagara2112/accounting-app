-- +goose Up
-- Kode akun boleh dikoreksi (mis. salah ketik) selama angka kepalanya tetap, sehingga kelas dan
-- letaknya di laporan tidak berubah. Kelas, induk, level, dan jenis akun tetap terkunci, akun
-- kelompok (kode satu angka) tidak bisa diganti kodenya, dan akun tetap tidak bisa dihapus.
-- Baris jurnal menunjuk akun lewat id, jadi riwayatnya ikut memakai kode baru.
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger.guard_account() RETURNS trigger
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
