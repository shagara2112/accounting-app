-- Modul laporan hanya membaca tabel buku besar (TDD, aturan antar modul nomor 3).

-- name: NetByAccount :many
-- Mutasi debit dan kredit per akun untuk satu rentang tanggal. Bulan penuh dibaca dari saldo
-- agregat (ledger.balances); hari-hari di bulan yang tidak penuh dibaca dari baris jurnal.
-- skip_closing menyingkirkan jurnal penutup, mis. untuk laba rugi dan arus kas.
SELECT x.account_id, sum(x.debit)::numeric AS debit, sum(x.credit)::numeric AS credit
FROM (
  SELECT b.account_id, b.debit, b.credit
  FROM ledger.balances b
  WHERE b.company_id = @company_id AND b.month >= @full_from AND b.month < @full_to
    AND (NOT @skip_closing::boolean OR b.kind <> 'penutup')
  UNION ALL
  SELECT l.account_id, l.debit, l.credit
  FROM ledger.journal_lines l
  JOIN ledger.journal_entries e ON e.id = l.entry_id
  WHERE l.company_id = @company_id
    AND ((l.entry_date >= @a_from AND l.entry_date < @a_to) OR (l.entry_date >= @b_from AND l.entry_date <= @b_to))
    AND (NOT @skip_closing::boolean OR e.kind <> 'penutup')
) x
GROUP BY x.account_id;

-- name: MonthlyResult :many
-- Pendapatan (kelas 4, 7) dan beban (kelas 5, 6, 8, 9) per bulan, untuk tren di dashboard.
SELECT b.month,
  coalesce(sum(b.credit - b.debit) FILTER (WHERE a.class IN (4, 7)), 0)::numeric AS revenue,
  coalesce(sum(b.debit - b.credit) FILTER (WHERE a.class IN (5, 6, 8, 9)), 0)::numeric AS expense
FROM ledger.balances b
JOIN ledger.accounts a ON a.id = b.account_id
WHERE b.company_id = @company_id AND b.month >= @from_month AND b.month <= @to_month
  AND a.class >= 4 AND b.kind <> 'penutup'
GROUP BY b.month
ORDER BY b.month;

-- name: NetByDimension :many
-- Mutasi per akun dari baris jurnal untuk rentang tanggal, disaring cabang dan/atau departemen
-- (GL-09). Tanpa jurnal penutup. branch_null/department_null memilih baris tanpa dimensi itu.
SELECT l.account_id, coalesce(l.branch_id, '00000000-0000-0000-0000-000000000000'::uuid) AS branch_id,
       coalesce(l.department_id, '00000000-0000-0000-0000-000000000000'::uuid) AS department_id,
       sum(l.debit)::numeric AS debit, sum(l.credit)::numeric AS credit
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
WHERE l.company_id = @company_id AND l.entry_date >= @from_date AND l.entry_date <= @to_date AND e.kind <> 'penutup'
  AND (sqlc.narg(branch_id)::uuid IS NULL OR l.branch_id = sqlc.narg(branch_id)::uuid)
  AND (NOT @branch_null::boolean OR l.branch_id IS NULL)
  AND (sqlc.narg(department_id)::uuid IS NULL OR l.department_id = sqlc.narg(department_id)::uuid)
  AND (NOT @department_null::boolean OR l.department_id IS NULL)
GROUP BY 1, 2, 3;

-- name: ClosingNet :many
-- Jurnal penutup (reklas laba akhir tahun) bertanggal [from_date, before_date) per akun.
SELECT l.account_id, sum(l.debit - l.credit)::numeric AS net
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
WHERE l.company_id = @company_id AND e.kind = 'penutup' AND l.entry_date >= @from_date AND l.entry_date < @before_date
GROUP BY l.account_id;

-- name: EquityMovements :many
-- Mutasi akun ekuitas (kelas 3) per jurnal dalam rentang, tanpa jurnal penutup. Jurnal void dan
-- pembaliknya yang sama-sama di dalam rentang saling meniadakan, jadi keduanya disembunyikan.
SELECT l.account_id, e.id AS entry_id, e.source_type, sum(l.debit - l.credit)::numeric AS net
FROM ledger.journal_lines l
JOIN ledger.journal_entries e ON e.id = l.entry_id
JOIN ledger.accounts a ON a.company_id = l.company_id AND a.id = l.account_id
WHERE l.company_id = @company_id AND a.class = 3 AND e.kind <> 'penutup'
  AND l.entry_date >= @from_date AND l.entry_date <= @to_date
  AND NOT (e.status = 'void' AND EXISTS (
        SELECT 1 FROM ledger.journal_entries r
        WHERE r.company_id = e.company_id AND r.reversal_of = e.id AND r.source_type = 'void' AND r.entry_date <= @to_date))
  AND NOT (e.source_type = 'void' AND EXISTS (
        SELECT 1 FROM ledger.journal_entries o
        WHERE o.company_id = e.company_id AND o.id = e.reversal_of AND o.entry_date >= @from_date))
GROUP BY l.account_id, e.id, e.source_type
HAVING sum(l.debit - l.credit) <> 0;

-- name: CashCounterparts :many
-- Arus kas metode langsung: baris akun selain kas pada jurnal yang menyentuh rekening kas/bank.
-- Kredit − debit baris itu = kas yang diterima (positif) atau dibayar (negatif) karena akun itu.
SELECT l.account_id, sum(l.credit - l.debit)::numeric AS amount
FROM ledger.journal_lines l
WHERE l.company_id = @company_id AND l.entry_date >= @from_date AND l.entry_date <= @to_date
  AND NOT (l.account_id = ANY(@cash_accounts::uuid[]))
  AND EXISTS (SELECT 1 FROM ledger.journal_lines c
              WHERE c.company_id = l.company_id AND c.entry_id = l.entry_id AND c.account_id = ANY(@cash_accounts::uuid[]))
GROUP BY l.account_id
HAVING sum(l.credit - l.debit) <> 0;
