-- name: CreateCompany :one
INSERT INTO core.companies (id, name, short_name, npwp, nitku, kind, industry)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListCompanies :many
SELECT * FROM core.companies WHERE active ORDER BY name;

-- name: ListAccessForUser :many
SELECT c.id, c.name, c.short_name, c.kind, c.industry,
       r.key AS role_key, r.name AS role_name, r.permissions
FROM core.user_company_roles ucr
JOIN core.companies c ON c.id = ucr.company_id
JOIN platform.roles r ON r.id = ucr.role_id
WHERE ucr.user_id = $1 AND c.active
ORDER BY c.kind DESC, c.name;

-- name: GetAccess :one
SELECT r.key, r.permissions
FROM core.user_company_roles ucr
JOIN core.companies c ON c.id = ucr.company_id
JOIN platform.roles r ON r.id = ucr.role_id
WHERE ucr.user_id = $1 AND ucr.company_id = $2 AND c.active;

-- name: AssignRoleByKey :execrows
INSERT INTO core.user_company_roles (user_id, company_id, role_id)
SELECT sqlc.arg(user_id)::uuid, sqlc.arg(company_id)::uuid, r.id
FROM platform.roles r
WHERE r.key = sqlc.arg(role_key)::text
ON CONFLICT (user_id, company_id) DO UPDATE SET role_id = EXCLUDED.role_id;
