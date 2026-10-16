-- name: GetSettings :one
SELECT * FROM platform.mail_settings;

-- name: SaveSettings :execrows
UPDATE platform.mail_settings
SET enabled = $2, host = $3, port = $4, security = $5, username = $6, password = $7,
    from_addr = $8, from_name = $9, version = version + 1, updated_at = now()
WHERE version = $1;

-- name: Enqueue :execrows
INSERT INTO platform.mail_outbox (id, company_id, kind, dedupe_key, to_addrs, cc_addrs, subject, body_text,
  attachment_name, attachment_mime, attachment, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (dedupe_key) DO NOTHING;

-- name: ClaimQueued :many
-- Diambil dengan SKIP LOCKED supaya dua proses tidak mengirim email yang sama.
SELECT * FROM platform.mail_outbox
WHERE status = 'queued' AND next_attempt_at <= now()
ORDER BY next_attempt_at
LIMIT 10
FOR UPDATE SKIP LOCKED;

-- name: MarkSent :exec
UPDATE platform.mail_outbox SET status = 'sent', sent_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1;

-- name: MarkRetry :exec
UPDATE platform.mail_outbox
SET attempts = attempts + 1, last_error = $2, next_attempt_at = $3,
    status = CASE WHEN attempts + 1 >= 5 THEN 'failed' ELSE 'queued' END
WHERE id = $1;

-- name: ListOutbox :many
SELECT id, company_id, kind, to_addrs, cc_addrs, subject, attachment_name, status, attempts, last_error, created_at, sent_at
FROM platform.mail_outbox
ORDER BY created_at DESC
LIMIT 50;

-- name: AdminEmails :many
-- Pengguna aktif pemegang izin sistem di PT mana pun: penerima peringatan backup dan lisensi.
SELECT DISTINCT u.email FROM platform.users u
JOIN core.user_company_roles ucr ON ucr.user_id = u.id
JOIN platform.roles r ON r.id = ucr.role_id
WHERE u.active AND 'platform.system.manage' = ANY (r.permissions)
ORDER BY u.email;
