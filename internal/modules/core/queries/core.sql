-- name: CreateCompany :one
INSERT INTO core.companies (id, name, short_name, npwp, nitku, kind, industry)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListCompanies :many
SELECT * FROM core.companies WHERE active ORDER BY name;

-- name: ListAccessForUser :many
SELECT c.id, c.name, c.short_name, c.kind, c.industry, c.pkp,
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

-- name: GetCompany :one
SELECT * FROM core.companies WHERE id = $1;

-- name: ListContacts :many
SELECT * FROM core.contacts
WHERE company_id = @company_id
  AND (NOT @customers_only::bool OR is_customer)
  AND (NOT @suppliers_only::bool OR is_supplier)
  AND (@include_inactive::bool OR active)
  AND (sqlc.narg(search)::text IS NULL
       OR name ILIKE '%' || sqlc.narg(search)::text || '%'
       OR npwp LIKE sqlc.narg(search)::text || '%')
ORDER BY lower(name)
LIMIT 500;

-- name: GetContact :one
SELECT * FROM core.contacts WHERE company_id = $1 AND id = $2;

-- name: InsertContact :one
INSERT INTO core.contacts
  (id, company_id, name, is_customer, is_supplier, npwp, nik, nitku, pkp, address, email, phone,
   payment_terms_days, bank_name, bank_account, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: UpdateContact :one
UPDATE core.contacts
SET name = @name, is_customer = @is_customer, is_supplier = @is_supplier, npwp = @npwp, nik = @nik,
    nitku = @nitku, pkp = @pkp, address = @address, email = @email, phone = @phone,
    payment_terms_days = @payment_terms_days, bank_name = @bank_name, bank_account = @bank_account,
    active = @active, version = version + 1
WHERE company_id = @company_id AND id = @id AND version = @version
RETURNING *;

-- name: UpdateCompanyTaxProfile :one
UPDATE core.companies
SET npwp = @npwp, nitku = @nitku, address = @address, pkp = @pkp, version = version + 1
WHERE id = @id AND version = @version
RETURNING *;
