-- name: CountUsers :one
SELECT count(*) FROM platform.users;

-- name: LockSetup :exec
-- Dua browser yang menjalankan setup bersamaan tidak boleh sama-sama berhasil.
SELECT pg_advisory_xact_lock(7240002);

-- name: MarkSetupDone :exec
UPDATE platform.installation SET setup_at = now() WHERE setup_at IS NULL;

-- name: ListUsers :many
SELECT id, email, name, active, locked_until, totp_enabled_at, last_login_at, created_at, version, reset_requested_at
FROM platform.users
ORDER BY lower(name), email;

-- name: ListAccess :many
SELECT ucr.user_id, ucr.company_id, c.name AS company_name, c.short_name,
       r.id AS role_id, r.key AS role_key, r.name AS role_name, r.permissions, ucr.branch_ids
FROM core.user_company_roles ucr
JOIN core.companies c ON c.id = ucr.company_id
JOIN platform.roles r ON r.id = ucr.role_id
ORDER BY c.name;

-- name: GetUserForUpdate :one
SELECT * FROM platform.users WHERE id = $1 FOR UPDATE;

-- name: EmailTaken :one
SELECT EXISTS (
  SELECT 1 FROM platform.users WHERE lower(email) = lower(sqlc.arg(email)::text) AND id <> sqlc.arg(id)
)::boolean;

-- name: InsertUser :exec
INSERT INTO platform.users (id, email, name, password_hash) VALUES ($1, $2, $3, $4);

-- name: UpdateUser :execrows
UPDATE platform.users
SET name = $3, email = $4, active = $5, version = version + 1
WHERE id = $1 AND version = $2;

-- name: SetUserPassword :exec
-- Admin membuatkan kata sandi baru; permintaan lupa kata sandi pengguna ikut selesai.
UPDATE platform.users
SET password_hash = $2, failed_logins = 0, locked_until = NULL, reset_requested_at = NULL, version = version + 1
WHERE id = $1;

-- name: UnlockUser :exec
UPDATE platform.users SET failed_logins = 0, locked_until = NULL WHERE id = $1;

-- name: ResetTOTP :exec
UPDATE platform.users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = 0 WHERE id = $1;

-- name: DeleteUserSessions :exec
DELETE FROM platform.sessions WHERE user_id = $1;

-- name: GetRoleByKey :one
SELECT * FROM platform.roles WHERE key = $1;

-- name: GetAccessRole :one
SELECT r.* FROM core.user_company_roles ucr
JOIN platform.roles r ON r.id = ucr.role_id
WHERE ucr.user_id = $1 AND ucr.company_id = $2;

-- name: SetAccess :exec
INSERT INTO core.user_company_roles (user_id, company_id, role_id, branch_ids)
VALUES (@user_id, @company_id, @role_id, coalesce(sqlc.narg(branch_ids)::uuid[], '{}'))
ON CONFLICT (user_id, company_id) DO UPDATE SET role_id = EXCLUDED.role_id, branch_ids = EXCLUDED.branch_ids;

-- name: CompanyBranchCount :one
-- Berapa dari cabang ini yang benar-benar milik PT itu.
SELECT count(*) FROM core.branches WHERE company_id = @company_id AND id = ANY(@ids::uuid[]);

-- name: RemoveAccess :execrows
DELETE FROM core.user_company_roles WHERE user_id = $1 AND company_id = $2;

-- name: CountActiveHolders :one
-- Pengguna aktif yang memegang izin ini di PT mana pun.
SELECT count(DISTINCT u.id) FROM platform.users u
JOIN core.user_company_roles ucr ON ucr.user_id = u.id
JOIN platform.roles r ON r.id = ucr.role_id
WHERE u.active AND sqlc.arg(perm)::text = ANY (r.permissions);

-- name: LockAdmins :exec
-- Perubahan akses diserialkan agar dua admin tidak saling mencabut sampai tak tersisa.
SELECT pg_advisory_xact_lock(7240003);

-- name: ListRoles :many
SELECT r.id, r.key, r.name, r.builtin, r.permissions, r.version,
       (SELECT count(*) FROM core.user_company_roles ucr WHERE ucr.role_id = r.id)::int AS assignments
FROM platform.roles r
ORDER BY r.builtin DESC, r.name;

-- name: GetRoleForUpdate :one
SELECT * FROM platform.roles WHERE id = $1 FOR UPDATE;

-- name: RoleKeyExists :one
SELECT EXISTS (SELECT 1 FROM platform.roles WHERE key = $1)::boolean;

-- name: RoleNameTaken :one
SELECT EXISTS (
  SELECT 1 FROM platform.roles WHERE lower(name) = lower(sqlc.arg(name)::text) AND id <> sqlc.arg(id)
)::boolean;

-- name: InsertRole :exec
INSERT INTO platform.roles (id, key, name, builtin, permissions) VALUES ($1, $2, $3, false, $4);

-- name: UpdateRole :execrows
UPDATE platform.roles SET name = $3, permissions = $4, version = version + 1
WHERE id = $1 AND version = $2 AND NOT builtin;

-- name: DeleteRole :execrows
DELETE FROM platform.roles r
WHERE r.id = $1 AND NOT r.builtin
  AND NOT EXISTS (SELECT 1 FROM core.user_company_roles ucr WHERE ucr.role_id = r.id);

-- name: CompanyNameTaken :one
SELECT EXISTS (SELECT 1 FROM core.companies WHERE lower(name) = lower(sqlc.arg(name)::text))::boolean;

-- name: CompanyNameTakenByOther :one
SELECT EXISTS (
  SELECT 1 FROM core.companies WHERE lower(name) = lower(sqlc.arg(name)::text) AND id <> sqlc.arg(id)::uuid
)::boolean;

-- name: LockCompanyNames :one
SELECT name, short_name FROM core.companies WHERE id = $1 AND kind = 'pt' FOR UPDATE;

-- name: RenameCompany :exec
UPDATE core.companies SET name = @name, short_name = @short_name, version = version + 1 WHERE id = @id;

-- name: ListCompanies :many
SELECT c.id, c.name, c.short_name, c.industry, c.npwp, c.active, c.created_at,
       (SELECT count(*) FROM core.user_company_roles ucr WHERE ucr.company_id = c.id)::int AS users
FROM core.companies c
WHERE c.kind = 'pt'
ORDER BY c.name;
