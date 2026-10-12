-- +goose Up
CREATE SCHEMA IF NOT EXISTS platform;
CREATE SCHEMA IF NOT EXISTS core;

-- Dipakai trigger untuk tabel yang hanya boleh ditambah (append-only).
-- +goose StatementBegin
CREATE FUNCTION platform.reject_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'tabel %.% bersifat append-only', TG_TABLE_SCHEMA, TG_TABLE_NAME
    USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TABLE core.companies (
  id          UUID PRIMARY KEY,
  name        TEXT NOT NULL,
  short_name  TEXT NOT NULL,
  npwp        TEXT,
  nitku       TEXT,
  kind        TEXT NOT NULL DEFAULT 'pt' CHECK (kind IN ('pt', 'consolidation')),
  industry    TEXT CHECK (industry IN ('jasa', 'konstruksi', 'retail')),
  active      BOOLEAN NOT NULL DEFAULT true,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  version     INT NOT NULL DEFAULT 1
);

CREATE TABLE platform.users (
  id             UUID PRIMARY KEY,
  email          TEXT NOT NULL,
  name           TEXT NOT NULL,
  password_hash  TEXT NOT NULL,
  active         BOOLEAN NOT NULL DEFAULT true,
  failed_logins  INT NOT NULL DEFAULT 0,
  locked_until   TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  version        INT NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX users_email_key ON platform.users (lower(email));

-- Peran bawaan disinkronkan dari kode saat aplikasi start; persona kustom dibuat Admin pelanggan.
CREATE TABLE platform.roles (
  id           UUID PRIMARY KEY,
  key          TEXT NOT NULL UNIQUE,
  name         TEXT NOT NULL,
  builtin      BOOLEAN NOT NULL DEFAULT false,
  permissions  TEXT[] NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hak akses diberikan per PT: satu orang bisa Manager di PT A dan Staf akuntansi di PT B.
CREATE TABLE core.user_company_roles (
  user_id     UUID NOT NULL REFERENCES platform.users (id) ON DELETE CASCADE,
  company_id  UUID NOT NULL REFERENCES core.companies (id),
  role_id     UUID NOT NULL REFERENCES platform.roles (id),
  PRIMARY KEY (user_id, company_id)
);

-- Token sesi tidak pernah disimpan; yang disimpan hanya hash SHA-256-nya.
CREATE TABLE platform.sessions (
  id_hash            BYTEA PRIMARY KEY,
  user_id            UUID NOT NULL REFERENCES platform.users (id) ON DELETE CASCADE,
  active_company_id  UUID REFERENCES core.companies (id),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at         TIMESTAMPTZ NOT NULL,
  ip                 TEXT,
  user_agent         TEXT
);
CREATE INDEX sessions_user_idx ON platform.sessions (user_id);

-- Audit trail dengan hash chain: setiap baris menyimpan hash baris sebelumnya.
CREATE TABLE platform.audit_log (
  seq          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  at           TIMESTAMPTZ NOT NULL,
  user_id      UUID,
  company_id   UUID,
  action       TEXT NOT NULL,
  object_type  TEXT NOT NULL,
  object_id    TEXT,
  before       JSONB,
  after        JSONB,
  ip           TEXT,
  prev_hash    BYTEA NOT NULL,
  hash         BYTEA NOT NULL
);
CREATE INDEX audit_log_company_idx ON platform.audit_log (company_id, seq DESC);

CREATE TRIGGER audit_log_append_only
  BEFORE UPDATE OR DELETE ON platform.audit_log
  FOR EACH ROW EXECUTE FUNCTION platform.reject_change();

CREATE TRIGGER audit_log_no_truncate
  BEFORE TRUNCATE ON platform.audit_log
  FOR EACH STATEMENT EXECUTE FUNCTION platform.reject_change();

-- +goose Down
DROP SCHEMA core CASCADE;
DROP SCHEMA platform CASCADE;
