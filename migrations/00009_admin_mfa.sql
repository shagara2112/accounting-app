-- +goose Up
-- Satu baris per instalasi. UUID-nya ikut membentuk installationId lisensi.
CREATE TABLE platform.installation (
  id          UUID PRIMARY KEY,
  singleton   BOOLEAN NOT NULL DEFAULT true UNIQUE CHECK (singleton),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  setup_at    TIMESTAMPTZ
);

-- 2FA (TOTP). Rahasia disimpan terenkripsi dengan kunci aplikasi; totp_last_step
-- mencegah kode yang sama dipakai dua kali.
ALTER TABLE platform.users
  ADD COLUMN totp_secret      BYTEA,
  ADD COLUMN totp_enabled_at  TIMESTAMPTZ,
  ADD COLUMN totp_last_step   BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN last_login_at    TIMESTAMPTZ;

-- Sesi pengguna yang wajib 2FA tetapi belum memasangnya: hanya boleh memasang 2FA.
ALTER TABLE platform.sessions ADD COLUMN mfa_setup BOOLEAN NOT NULL DEFAULT false;

-- Tahap kedua login: kata sandi sudah benar, menunggu kode autentikator.
CREATE TABLE platform.login_challenges (
  id_hash     BYTEA PRIMARY KEY,
  user_id     UUID NOT NULL REFERENCES platform.users (id) ON DELETE CASCADE,
  ip          TEXT,
  expires_at  TIMESTAMPTZ NOT NULL,
  attempts    INT NOT NULL DEFAULT 0
);
CREATE INDEX login_challenges_user_idx ON platform.login_challenges (user_id);

-- Persona kustom bisa diubah; version untuk optimistic locking.
ALTER TABLE platform.roles ADD COLUMN version INT NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE platform.roles DROP COLUMN version;
DROP TABLE platform.login_challenges;
ALTER TABLE platform.sessions DROP COLUMN mfa_setup;
ALTER TABLE platform.users
  DROP COLUMN last_login_at, DROP COLUMN totp_last_step, DROP COLUMN totp_enabled_at, DROP COLUMN totp_secret;
DROP TABLE platform.installation;
