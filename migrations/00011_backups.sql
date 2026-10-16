-- +goose Up
-- Pengaturan backup (satu baris): jam backup harian, jumlah salinan yang disimpan, dan tujuan
-- salinan tambahan (folder lain/NAS dan S3-compatible). Secret S3 terenkripsi kunci aplikasi.
CREATE TABLE platform.backup_settings (
  singleton      BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  hour           SMALLINT NOT NULL DEFAULT 1 CHECK (hour BETWEEN 0 AND 23),
  keep           INT NOT NULL DEFAULT 30 CHECK (keep BETWEEN 3 AND 365),
  copy_dir       TEXT,
  s3_endpoint    TEXT,
  s3_region      TEXT,
  s3_bucket      TEXT,
  s3_prefix      TEXT,
  s3_access_key  TEXT,
  s3_secret      BYTEA,
  version        INT NOT NULL DEFAULT 1,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO platform.backup_settings DEFAULT VALUES;

CREATE TABLE platform.backups (
  id           UUID PRIMARY KEY,
  kind         TEXT NOT NULL CHECK (kind IN ('scheduled', 'manual', 'pre_update')),
  status       TEXT NOT NULL CHECK (status IN ('running', 'ok', 'failed')),
  started_at   TIMESTAMPTZ NOT NULL,
  finished_at  TIMESTAMPTZ,
  file_name    TEXT,
  size_bytes   BIGINT,
  sha256       TEXT,
  key_id       TEXT,
  copies       JSONB NOT NULL DEFAULT '{}',
  error        TEXT,
  pruned       BOOLEAN NOT NULL DEFAULT false,   -- file lokal sudah dihapus oleh retensi
  created_by   UUID REFERENCES platform.users (id)
);
CREATE INDEX backups_started_idx ON platform.backups (started_at DESC);

-- Uji pulih mingguan: dump dari backup dipulihkan ke basis data sementara, lalu rantai audit
-- trail-nya diverifikasi.
CREATE TABLE platform.restore_tests (
  id         UUID PRIMARY KEY,
  backup_id  UUID REFERENCES platform.backups (id) ON DELETE SET NULL,
  tested_at  TIMESTAMPTZ NOT NULL,
  ok         BOOLEAN NOT NULL,
  detail     TEXT NOT NULL
);

-- +goose Down
DROP TABLE platform.restore_tests;
DROP TABLE platform.backups;
DROP TABLE platform.backup_settings;
