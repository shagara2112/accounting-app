-- name: GetUserByEmail :one
SELECT * FROM platform.users WHERE lower(email) = lower(sqlc.arg(email)::text);

-- name: GetUserByID :one
SELECT * FROM platform.users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO platform.users (id, email, name, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: SetLoginFailures :exec
UPDATE platform.users SET failed_logins = $2, locked_until = $3 WHERE id = $1;

-- name: CreateSession :exec
INSERT INTO platform.sessions (id_hash, user_id, active_company_id, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSession :one
SELECT s.id_hash, s.user_id, s.active_company_id, s.last_seen_at, s.expires_at,
       u.email, u.name, u.active
FROM platform.sessions s
JOIN platform.users u ON u.id = s.user_id
WHERE s.id_hash = $1;

-- name: TouchSession :exec
UPDATE platform.sessions SET last_seen_at = now() WHERE id_hash = $1;

-- name: SetSessionCompany :exec
UPDATE platform.sessions SET active_company_id = $2 WHERE id_hash = $1;

-- name: DeleteSession :exec
DELETE FROM platform.sessions WHERE id_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM platform.sessions WHERE user_id = $1;

-- name: UpsertBuiltinRole :exec
INSERT INTO platform.roles (id, key, name, builtin, permissions)
VALUES ($1, $2, $3, true, $4)
ON CONFLICT (key) DO UPDATE
SET name = EXCLUDED.name, permissions = EXCLUDED.permissions, builtin = true;

-- name: LockAuditChain :exec
SELECT pg_advisory_xact_lock(7240001);

-- name: LastAuditHash :one
SELECT hash FROM platform.audit_log ORDER BY seq DESC LIMIT 1;

-- name: InsertAudit :one
INSERT INTO platform.audit_log
  (at, user_id, company_id, action, object_type, object_id, before, after, ip, prev_hash, hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING seq;

-- name: ListAudit :many
SELECT * FROM platform.audit_log
WHERE company_id = sqlc.arg(company_id) OR company_id IS NULL
ORDER BY seq DESC
LIMIT sqlc.arg(row_limit);

-- name: AuditChain :many
SELECT * FROM platform.audit_log ORDER BY seq ASC;

-- name: ClaimIdempotencyKey :one
-- Bila kunci sedang dipakai transaksi lain, perintah ini menunggu sampai transaksi itu
-- selesai; tidak ada baris yang dikembalikan bila kunci sudah tersimpan.
INSERT INTO platform.idempotency_keys (user_id, key, request_hash)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, key) DO NOTHING
RETURNING key;

-- name: GetIdempotencyKey :one
SELECT * FROM platform.idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: SaveIdempotentResponse :exec
UPDATE platform.idempotency_keys
SET status = $3, content_type = $4, body = $5
WHERE user_id = $1 AND key = $2;

-- name: PurgeIdempotencyKeys :execrows
DELETE FROM platform.idempotency_keys WHERE created_at < now() - interval '24 hours';
