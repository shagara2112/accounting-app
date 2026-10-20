-- +goose Up
-- Akun sistem per PT (piutang usaha, PPN masukan, utang PPh, dst.) dikunci ke akunnya lewat id
-- begitu kode akun itu dikoreksi, sehingga PT yang memakai skema kode sendiri tetap bisa
-- memposting otomatis. Peran yang belum punya baris di sini tetap dicari lewat kode bawaannya.
CREATE TABLE ledger.role_accounts (
  company_id UUID NOT NULL REFERENCES core.companies (id),
  role       TEXT NOT NULL,
  account_id UUID NOT NULL,
  pinned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  pinned_by  UUID REFERENCES platform.users (id),
  PRIMARY KEY (company_id, role),
  FOREIGN KEY (company_id, account_id) REFERENCES ledger.accounts (company_id, id)
);

-- +goose Down
DROP TABLE ledger.role_accounts;
