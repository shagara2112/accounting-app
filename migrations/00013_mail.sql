-- +goose Up
-- Email lewat SMTP pelanggan (PLT-08): pengaturan satu baris dan antrean kirim (outbox).
CREATE TABLE platform.mail_settings (
  singleton   BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  enabled     BOOLEAN NOT NULL DEFAULT false,
  host        TEXT NOT NULL DEFAULT '',
  port        INT NOT NULL DEFAULT 587 CHECK (port BETWEEN 1 AND 65535),
  security    TEXT NOT NULL DEFAULT 'starttls' CHECK (security IN ('starttls', 'tls', 'none')),
  username    TEXT NOT NULL DEFAULT '',
  password    BYTEA,                       -- terenkripsi kunci aplikasi
  from_addr   TEXT NOT NULL DEFAULT '',
  from_name   TEXT NOT NULL DEFAULT 'Lajur',
  version     INT NOT NULL DEFAULT 1,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO platform.mail_settings DEFAULT VALUES;

CREATE TABLE platform.mail_outbox (
  id               UUID PRIMARY KEY,
  company_id       UUID REFERENCES core.companies (id),
  kind             TEXT NOT NULL,          -- invoice, backup_alert, license_alert, test
  dedupe_key       TEXT UNIQUE,            -- peringatan yang sama tidak dikirim dua kali
  to_addrs         TEXT[] NOT NULL,
  cc_addrs         TEXT[] NOT NULL DEFAULT '{}',
  subject          TEXT NOT NULL,
  body_text        TEXT NOT NULL,
  attachment_name  TEXT,
  attachment_mime  TEXT,
  attachment       BYTEA,
  status           TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'failed')),
  attempts         INT NOT NULL DEFAULT 0,
  next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error       TEXT,
  created_by       UUID REFERENCES platform.users (id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at          TIMESTAMPTZ
);
CREATE INDEX mail_outbox_queue_idx ON platform.mail_outbox (next_attempt_at) WHERE status = 'queued';
CREATE INDEX mail_outbox_created_idx ON platform.mail_outbox (created_at DESC);

-- Logo PT untuk kop faktur PDF (AR-03); disimpan sebagai JPEG ≤ 512 KB.
ALTER TABLE core.companies ADD COLUMN logo BYTEA;

-- +goose Down
ALTER TABLE core.companies DROP COLUMN logo;
DROP TABLE platform.mail_outbox;
DROP TABLE platform.mail_settings;
