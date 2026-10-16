-- name: InsertAttachment :exec
INSERT INTO platform.attachments (id, company_id, object_type, object_id, file_name, mime, size_bytes, sha256, storage_path, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListAttachments :many
SELECT a.*, u.name AS uploader_name
FROM platform.attachments a
LEFT JOIN platform.users u ON u.id = a.uploaded_by
WHERE a.company_id = $1 AND a.object_type = $2 AND a.object_id = $3 AND a.deleted_at IS NULL
ORDER BY a.uploaded_at;

-- name: GetAttachment :one
SELECT * FROM platform.attachments WHERE company_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: DeleteAttachment :execrows
UPDATE platform.attachments SET deleted_at = now(), deleted_by = $3
WHERE company_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: CountAttachments :many
SELECT object_id, count(*)::int AS n FROM platform.attachments
WHERE company_id = $1 AND object_type = $2 AND object_id = ANY (sqlc.arg(ids)::uuid[]) AND deleted_at IS NULL
GROUP BY object_id;
