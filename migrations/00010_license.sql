-- +goose Up
-- Lisensi remote (License Console): satu baris konfigurasi dan token terakhir. License key
-- disimpan terenkripsi dengan kunci aplikasi; token JWS bertanda tangan console berlaku 72 jam.
CREATE TABLE platform.license (
  singleton      BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  console_url    TEXT NOT NULL,
  client_id      TEXT NOT NULL,
  license_key    BYTEA NOT NULL,
  token          TEXT,
  checked_at     TIMESTAMPTZ,           -- check-in sukses terakhir
  attempted_at   TIMESTAMPTZ,           -- percobaan check-in terakhir
  error_code     TEXT,                  -- galat percobaan terakhir; kosong bila sukses
  error_message  TEXT,
  -- Waktu tertinggi yang pernah terlihat; jam server yang mundur jauh di bawahnya
  -- membuat lisensi UNVERIFIED sampai check-in berikutnya berhasil.
  high_water     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE platform.license;
