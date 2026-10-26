-- +goose Up
-- Lupa kata sandi dari halaman masuk: kode 6 digit dikirim ke email pengguna (bila email server
-- sudah diatur), berlaku 15 menit, paling banyak 5 kali salah, dan sekali pakai. Yang disimpan
-- hanya hash kodenya. Permintaannya juga ditandai di pengguna, supaya Admin bisa membuatkan
-- kata sandi baru bila email tidak tersedia.
CREATE TABLE platform.password_resets (
  id          UUID PRIMARY KEY,
  user_id     UUID NOT NULL REFERENCES platform.users (id),
  code_hash   BYTEA NOT NULL,
  expires_at  TIMESTAMPTZ NOT NULL,
  attempts    INT NOT NULL DEFAULT 0,
  used_at     TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_ip  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX password_resets_user_idx ON platform.password_resets (user_id, created_at DESC);

ALTER TABLE platform.users ADD COLUMN reset_requested_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE platform.users DROP COLUMN reset_requested_at;
DROP TABLE platform.password_resets;
