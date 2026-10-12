-- +goose Up
-- Kunci idempotensi untuk POST dokumen keuangan (TDD, bagian API): klik ganda atau
-- pengiriman ulang dengan kunci yang sama mengembalikan respons pertama, bukan membuat
-- dokumen kedua. Kunci disimpan 24 jam.
CREATE TABLE platform.idempotency_keys (
  user_id       UUID NOT NULL REFERENCES platform.users (id) ON DELETE CASCADE,
  key           TEXT NOT NULL CHECK (length(key) BETWEEN 8 AND 100),
  request_hash  BYTEA NOT NULL,
  status        INT,
  content_type  TEXT,
  body          BYTEA,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_keys_created_idx ON platform.idempotency_keys (created_at);

-- +goose Down
DROP TABLE platform.idempotency_keys;
