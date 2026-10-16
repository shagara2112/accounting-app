-- name: InsertLink :exec
INSERT INTO consol.ic_links
  (id, source_company_id, source_type, source_id, target_company_id, target_type, target_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: LinkOf :one
SELECT * FROM consol.ic_links
WHERE (source_type = @doc_type AND source_id = @doc_id) OR (target_type = @doc_type AND target_id = @doc_id);

-- name: GroupAccounts :many
SELECT * FROM consol.group_accounts ORDER BY code;

-- name: UpsertGroupAccount :exec
INSERT INTO consol.group_accounts (code, name, parent_code, postable, class, updated_by)
VALUES (@code, @name, @parent_code, @postable, @class, @updated_by)
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: InsertGroupAccountIfMissing :exec
INSERT INTO consol.group_accounts (code, name, parent_code, postable, class, updated_by)
VALUES (@code, @name, @parent_code, @postable, @class, @updated_by)
ON CONFLICT (code) DO NOTHING;

-- name: AccountMap :many
SELECT * FROM consol.account_map;

-- name: SetAccountMap :exec
INSERT INTO consol.account_map (company_id, account_id, group_code, updated_by)
VALUES (@company_id, @account_id, @group_code, @updated_by)
ON CONFLICT (company_id, account_id) DO UPDATE
  SET group_code = EXCLUDED.group_code, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: DeleteAccountMap :exec
DELETE FROM consol.account_map WHERE company_id = $1 AND account_id = $2;
