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
INSERT INTO platform.sessions (id_hash, user_id, active_company_id, expires_at, ip, user_agent, mfa_setup)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetSession :one
SELECT s.id_hash, s.user_id, s.active_company_id, s.last_seen_at, s.expires_at, s.mfa_setup,
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

-- name: DeleteOtherSessions :exec
DELETE FROM platform.sessions WHERE user_id = $1 AND id_hash <> $2;

-- name: ClearSessionMFASetup :exec
UPDATE platform.sessions SET mfa_setup = false WHERE user_id = $1;

-- name: CountUsers :one
SELECT count(*) FROM platform.users;

-- name: SetLastLogin :exec
UPDATE platform.users SET last_login_at = now() WHERE id = $1;

-- name: SetPassword :exec
UPDATE platform.users
SET password_hash = $2, failed_logins = 0, locked_until = NULL, version = version + 1
WHERE id = $1;

-- name: CountRecentPasswordResets :one
SELECT count(*) FROM platform.password_resets WHERE user_id = $1 AND created_at > $2;

-- name: InsertPasswordReset :exec
INSERT INTO platform.password_resets (id, user_id, code_hash, expires_at, created_ip) VALUES ($1, $2, $3, $4, $5);

-- name: MarkResetRequested :exec
UPDATE platform.users SET reset_requested_at = now() WHERE id = $1;

-- name: LatestPasswordReset :one
-- Kode terakhir yang belum dipakai; dikunci supaya dua percobaan bersamaan dihitung benar.
SELECT * FROM platform.password_resets
WHERE user_id = $1 AND used_at IS NULL
ORDER BY created_at DESC
LIMIT 1
FOR UPDATE;

-- name: BumpPasswordReset :exec
UPDATE platform.password_resets SET attempts = attempts + 1 WHERE id = $1;

-- name: UsePasswordResets :exec
UPDATE platform.password_resets SET used_at = now() WHERE user_id = $1 AND used_at IS NULL;

-- name: SetPasswordFromReset :exec
UPDATE platform.users
SET password_hash = $2, failed_logins = 0, locked_until = NULL, reset_requested_at = NULL, version = version + 1
WHERE id = $1;

-- name: UserPrivileged :one
-- Pengguna yang memegang salah satu izin ini di PT mana pun wajib memakai 2FA.
SELECT EXISTS (
  SELECT 1 FROM core.user_company_roles ucr
  JOIN platform.roles r ON r.id = ucr.role_id
  WHERE ucr.user_id = sqlc.arg(user_id) AND r.permissions && sqlc.arg(perms)::text[]
)::boolean;

-- name: SetPendingTOTP :exec
-- Rahasia baru belum aktif sampai kode pertama dikonfirmasi.
UPDATE platform.users SET totp_secret = $2, totp_enabled_at = NULL, totp_last_step = 0 WHERE id = $1;

-- name: EnableTOTP :execrows
UPDATE platform.users SET totp_enabled_at = now(), totp_last_step = $2
WHERE id = $1 AND totp_secret IS NOT NULL AND totp_enabled_at IS NULL;

-- name: ClearTOTP :exec
UPDATE platform.users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = 0 WHERE id = $1;

-- name: UseTOTPStep :execrows
-- Kode hanya sah sekali: langkah waktu harus lebih baru dari yang terakhir dipakai.
UPDATE platform.users SET totp_last_step = $2 WHERE id = $1 AND totp_last_step < $2;

-- name: InsertLoginChallenge :exec
INSERT INTO platform.login_challenges (id_hash, user_id, ip, expires_at) VALUES ($1, $2, $3, $4);

-- name: GetLoginChallenge :one
SELECT * FROM platform.login_challenges WHERE id_hash = $1 FOR UPDATE;

-- name: BumpLoginChallenge :exec
UPDATE platform.login_challenges SET attempts = attempts + 1 WHERE id_hash = $1;

-- name: DeleteLoginChallenge :exec
DELETE FROM platform.login_challenges WHERE id_hash = $1;

-- name: PurgeLoginChallenges :execrows
DELETE FROM platform.login_challenges WHERE expires_at < now();

-- name: GetInstallation :one
SELECT * FROM platform.installation LIMIT 1;

-- name: EnsureInstallation :exec
INSERT INTO platform.installation (id) VALUES ($1) ON CONFLICT (singleton) DO NOTHING;

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

-- name: GetLicense :one
-- last_activity: waktu audit terakhir, ikut menjadi batas bawah deteksi jam mundur.
SELECT l.*, GREATEST(l.high_water, COALESCE((SELECT max(at) FROM platform.audit_log), l.high_water))::timestamptz AS last_activity
FROM platform.license l;

-- name: SaveLicenseConfig :exec
INSERT INTO platform.license (console_url, client_id, license_key, token, checked_at, attempted_at, high_water)
VALUES ($1, $2, $3, $4, $5, $5, $5)
ON CONFLICT (singleton) DO UPDATE
SET console_url = EXCLUDED.console_url, client_id = EXCLUDED.client_id, license_key = EXCLUDED.license_key,
    token = EXCLUDED.token, checked_at = EXCLUDED.checked_at, attempted_at = EXCLUDED.attempted_at,
    error_code = NULL, error_message = NULL,
    high_water = GREATEST(platform.license.high_water, EXCLUDED.high_water), updated_at = now();

-- name: SaveCheckin :exec
UPDATE platform.license
SET token = $1, checked_at = $2, attempted_at = $2, error_code = NULL, error_message = NULL,
    high_water = GREATEST(high_water, sqlc.arg(seen)::timestamptz), updated_at = now();

-- name: SaveCheckinError :exec
UPDATE platform.license SET attempted_at = $1, error_code = $2, error_message = $3, updated_at = now();

-- name: BumpHighWater :exec
UPDATE platform.license SET high_water = GREATEST(high_water, sqlc.arg(seen)::timestamptz);

-- name: HealthCounts :one
SELECT (SELECT count(*) FROM core.companies WHERE kind = 'pt')::int AS companies,
       (SELECT count(*) FROM platform.users WHERE active)::int AS users,
       (pg_database_size(current_database()) / 1048576)::int AS db_mb;

-- name: GetBackupSettings :one
SELECT * FROM platform.backup_settings;

-- name: SaveBackupSettings :execrows
UPDATE platform.backup_settings
SET hour = $2, keep = $3, copy_dir = $4, s3_endpoint = $5, s3_region = $6, s3_bucket = $7, s3_prefix = $8,
    s3_access_key = $9, s3_secret = $10, version = version + 1, updated_at = now()
WHERE version = $1;

-- name: InsertBackup :exec
INSERT INTO platform.backups (id, kind, status, started_at, key_id, created_by)
VALUES ($1, $2, 'running', $3, $4, $5);

-- name: FinishBackup :exec
UPDATE platform.backups
SET status = $2, finished_at = $3, file_name = $4, size_bytes = $5, sha256 = $6, copies = $7, error = $8
WHERE id = $1;

-- name: ListBackups :many
SELECT * FROM platform.backups ORDER BY started_at DESC LIMIT sqlc.arg(row_limit);

-- name: GetBackup :one
SELECT * FROM platform.backups WHERE id = $1;

-- name: BackupsToPrune :many
-- Backup sukses yang filenya masih ada, selain N terbaru.
SELECT * FROM platform.backups
WHERE status = 'ok' AND NOT pruned
ORDER BY started_at DESC
OFFSET sqlc.arg(keep);

-- name: MarkPruned :exec
UPDATE platform.backups SET pruned = true WHERE id = $1;

-- name: FailStaleBackups :exec
-- Backup yang tertinggal "running" karena aplikasi mati di tengah jalan.
UPDATE platform.backups SET status = 'failed', finished_at = now(), error = 'Aplikasi berhenti saat backup berjalan.'
WHERE status = 'running';

-- name: LastOKBackup :one
SELECT * FROM platform.backups WHERE status = 'ok' ORDER BY started_at DESC LIMIT 1;

-- name: LastScheduledBackup :one
SELECT * FROM platform.backups WHERE kind = 'scheduled' ORDER BY started_at DESC LIMIT 1;

-- name: LastFailedBackup :one
SELECT * FROM platform.backups WHERE status = 'failed' ORDER BY started_at DESC LIMIT 1;

-- name: InsertRestoreTest :exec
INSERT INTO platform.restore_tests (id, backup_id, tested_at, ok, detail) VALUES ($1, $2, $3, $4, $5);

-- name: LastRestoreTest :one
SELECT * FROM platform.restore_tests ORDER BY tested_at DESC LIMIT 1;
