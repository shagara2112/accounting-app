-- Panjar unit: pengajuan kebutuhan unit (departemen) dan keputusan finance.

-- name: InsertRequest :exec
INSERT INTO panjar.requests (id, company_id, number, department_id, request_date, needed_date, purpose, total, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpdateRequest :execrows
UPDATE panjar.requests
SET department_id = @department_id, needed_date = sqlc.narg(needed_date), purpose = @purpose, total = @total, version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'diajukan' AND version = @version;

-- name: InsertLine :exec
INSERT INTO panjar.lines (id, company_id, request_id, line_no, description, account_id, amount)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: DeleteLines :exec
DELETE FROM panjar.lines WHERE company_id = $1 AND request_id = $2;

-- name: GetRequest :one
SELECT r.*, d.code AS department_code, d.name AS department_name, cu.name AS created_by_name, du.name AS decided_by_name
FROM panjar.requests r
JOIN core.departments d ON d.id = r.department_id
JOIN platform.users cu ON cu.id = r.created_by
LEFT JOIN platform.users du ON du.id = r.decided_by
WHERE r.company_id = $1 AND r.id = $2;

-- name: GetRequestForUpdate :one
SELECT * FROM panjar.requests WHERE company_id = $1 AND id = $2 FOR UPDATE;

-- name: RequestLines :many
SELECT l.*, a.code AS account_code, a.name AS account_name
FROM panjar.lines l
LEFT JOIN ledger.accounts a ON a.id = l.account_id
WHERE l.company_id = $1 AND l.request_id = $2
ORDER BY l.line_no;

-- name: ListRequests :many
SELECT r.*, d.code AS department_code, d.name AS department_name, cu.name AS created_by_name
FROM panjar.requests r
JOIN core.departments d ON d.id = r.department_id
JOIN platform.users cu ON cu.id = r.created_by
WHERE r.company_id = @company_id
  AND r.request_date >= @from_date AND r.request_date <= @to_date
  -- Pembaca terbatas (kepala unit): pengajuannya sendiri dan pengajuan unit yang dikelolanya.
  AND (sqlc.narg(viewer)::uuid IS NULL OR r.created_by = sqlc.narg(viewer)::uuid OR r.department_id = ANY(@units::uuid[]))
  AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
  AND (sqlc.narg(department_id)::uuid IS NULL OR r.department_id = sqlc.narg(department_id)::uuid)
ORDER BY r.request_date DESC, r.id DESC
LIMIT 500;

-- name: CountPending :one
-- Pengajuan yang menunggu keputusan finance (untuk penanda di menu).
SELECT count(*) FROM panjar.requests WHERE company_id = $1 AND status = 'diajukan';

-- name: Decide :execrows
UPDATE panjar.requests
SET status = @status, approved_amount = sqlc.narg(approved_amount), decided_at = now(), decided_by = @decided_by,
    decision_note = @decision_note, version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'diajukan' AND version = @version;

-- name: CancelRequest :execrows
UPDATE panjar.requests SET status = 'dibatalkan', version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'diajukan' AND version = @version;

-- name: MarkPaid :execrows
UPDATE panjar.requests SET status = 'dibayar', paid_at = @paid_at, paid_ref = @paid_ref, version = version + 1
WHERE company_id = @company_id AND id = @id AND status = 'disetujui' AND version = @version;

-- name: Committed :one
-- Panjar unit yang sudah disetujui tetapi belum dibayar: sudah terikat, belum masuk realisasi.
SELECT coalesce(sum(approved_amount), 0)::numeric AS committed
FROM panjar.requests
WHERE company_id = $1 AND department_id = $2 AND status = 'disetujui';

-- name: UserUnits :many
-- Unit yang dikelola seorang kepala unit.
SELECT department_id FROM panjar.unit_heads WHERE company_id = $1 AND user_id = $2;

-- name: UnitHeads :many
-- Pengguna PT ini yang bisa mengajukan panjar tetapi tidak melihat semua dan tidak memutuskan,
-- beserta unit yang dikelolanya.
SELECT u.id, u.name, u.email, u.active, r.name AS role_name,
       coalesce(array_agg(h.department_id ORDER BY d.code) FILTER (WHERE h.department_id IS NOT NULL), '{}')::uuid[] AS department_ids
FROM core.user_company_roles ucr
JOIN platform.users u ON u.id = ucr.user_id
JOIN platform.roles r ON r.id = ucr.role_id
LEFT JOIN panjar.unit_heads h ON h.company_id = ucr.company_id AND h.user_id = ucr.user_id
LEFT JOIN core.departments d ON d.company_id = h.company_id AND d.id = h.department_id
WHERE ucr.company_id = @company_id AND @create_perm::text = ANY(r.permissions)
  AND NOT (@view_perm::text = ANY(r.permissions)) AND NOT (@approve_perm::text = ANY(r.permissions))
GROUP BY u.id, u.name, u.email, u.active, r.name
ORDER BY lower(u.name), u.email;

-- name: MemberPerms :one
-- Izin peran pengguna di PT ini; tidak ada baris bila pengguna tidak punya akses ke PT itu.
SELECT r.permissions FROM core.user_company_roles ucr
JOIN platform.roles r ON r.id = ucr.role_id
WHERE ucr.company_id = $1 AND ucr.user_id = $2
FOR UPDATE OF ucr;

-- name: ClearUnits :exec
DELETE FROM panjar.unit_heads WHERE company_id = $1 AND user_id = $2;

-- name: AddUnits :exec
INSERT INTO panjar.unit_heads (company_id, user_id, department_id, created_by)
SELECT @company_id, @user_id, unnest(@department_ids::uuid[]), sqlc.narg(created_by)::uuid;
