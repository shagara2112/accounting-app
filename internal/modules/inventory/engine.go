package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/ledger"
	"lajur/internal/modules/inventory/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
)

// Cara menilai satu pergerakan (lihat migrasi 00006).
const (
	valFixed   = "fixed"
	valAverage = "average"
	valMirror  = "mirror"
	valRatio   = "ratio" // retur: nilai pergerakan cost_of × qty / qty pergerakan itu
)

// move adalah pergerakan baru yang akan dicatat.
type move struct {
	productID  uuid.UUID
	date       civil.Date
	qty        money.Amount // bertanda: + masuk, − keluar
	valuation  string
	fixedValue *money.Amount
	mirrorOf   *uuid.UUID
	costOf     *uuid.UUID
	owned      bool
	counter    *uuid.UUID // akun lawan persediaan untuk pergerakan owned
	projectID  *uuid.UUID
	branchID   *uuid.UUID
	deptID     *uuid.UUID
	sourceType string
	sourceID   uuid.UUID
	number     string
	memo       string
}

// delta adalah selisih nilai pergerakan owned yang belum dijurnal.
type delta struct {
	store.InventoryMovement
	amount  money.Amount // nilai baru − nilai yang sudah dijurnal
	initial bool         // belum pernah dijurnal sama sekali
}

func (s *service) IssueGoods(ctx context.Context, companyID uuid.UUID, doc Doc, lines []Issue, actor audit.Actor) error {
	if len(lines) == 0 {
		return nil
	}
	products, err := s.goods(ctx, companyID, issueIDs(lines))
	if err != nil {
		return err
	}
	moves := make([]move, len(lines))
	for i, l := range lines {
		p := products[l.ProductID]
		if l.Qty.Sign() <= 0 {
			return fmt.Errorf("kuantitas keluar %s harus positif", p.Code)
		}
		moves[i] = move{
			productID: l.ProductID, date: doc.Date, qty: l.Qty.Neg(), valuation: valAverage, owned: true,
			counter: p.ExpenseAccountID, projectID: doc.ProjectID, branchID: doc.BranchID, deptID: doc.DepartmentID, sourceType: doc.SourceType,
			sourceID: doc.SourceID, number: doc.Number, memo: memoOr(l.Memo, p.Name),
		}
	}
	return s.record(ctx, companyID, moves, actor, nil)
}

func (s *service) ReceiveGoods(ctx context.Context, companyID uuid.UUID, doc Doc, lines []Receipt, actor audit.Actor) error {
	if len(lines) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		ids[i] = l.ProductID
	}
	products, err := s.goods(ctx, companyID, ids)
	if err != nil {
		return err
	}
	moves := make([]move, len(lines))
	for i, l := range lines {
		if l.Qty.Sign() <= 0 || l.Value.Sign() < 0 {
			return fmt.Errorf("barang masuk %s harus berkuantitas positif dan bernilai tidak negatif", products[l.ProductID].Code)
		}
		v := l.Value
		moves[i] = move{
			productID: l.ProductID, date: doc.Date, qty: l.Qty, valuation: valFixed, fixedValue: &v,
			projectID: doc.ProjectID, branchID: doc.BranchID, deptID: doc.DepartmentID, sourceType: doc.SourceType, sourceID: doc.SourceID, number: doc.Number,
			memo: memoOr(l.Memo, products[l.ProductID].Name),
		}
	}
	return s.record(ctx, companyID, moves, actor, nil)
}

// ReturnGoods mengembalikan barang retur penjualan ke stok dengan biaya saat barang itu keluar
// di dokumen asalnya (origType, origID), sebanding kuantitasnya. HPP-nya dibalik ke akun lawan
// dan proyek pergerakan asal.
func (s *service) ReturnGoods(ctx context.Context, companyID uuid.UUID, doc Doc, origType string, origID uuid.UUID, lines []Issue, actor audit.Actor) error {
	if len(lines) == 0 {
		return nil
	}
	rows, err := s.q(ctx).MovementsBySource(ctx, store.MovementsBySourceParams{CompanyID: companyID, SourceType: origType, SourceID: origID})
	if err != nil {
		return err
	}
	issued := map[uuid.UUID]store.MovementsBySourceRow{}
	for _, r := range rows {
		if _, ok := issued[r.ProductID]; !ok && r.Qty.Sign() < 0 && r.Owned {
			issued[r.ProductID] = r
		}
	}
	products, err := s.goods(ctx, companyID, issueIDs(lines))
	if err != nil {
		return err
	}
	moves := make([]move, len(lines))
	for i, l := range lines {
		p := products[l.ProductID]
		o, ok := issued[l.ProductID]
		switch {
		case !ok:
			return fmt.Errorf("barang %s tidak keluar dari stok di dokumen asal", p.Code)
		case l.Qty.Sign() <= 0:
			return fmt.Errorf("kuantitas retur %s harus positif", p.Code)
		}
		id := o.ID
		moves[i] = move{
			productID: l.ProductID, date: doc.Date, qty: l.Qty, valuation: valRatio, costOf: &id, owned: true,
			counter: o.CounterAccountID, projectID: o.ProjectID, branchID: o.BranchID, deptID: o.DepartmentID, sourceType: doc.SourceType, sourceID: doc.SourceID,
			number: doc.Number, memo: memoOr(l.Memo, p.Name),
		}
	}
	return s.record(ctx, companyID, moves, actor, nil)
}

// SendBack mengeluarkan barang retur pembelian dengan nilai tetap yang sudah dijurnal dokumen
// sumbernya (kredit akun persediaan produk), yaitu harga di tagihan asalnya.
func (s *service) SendBack(ctx context.Context, companyID uuid.UUID, doc Doc, lines []Receipt, actor audit.Actor) error {
	if len(lines) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		ids[i] = l.ProductID
	}
	products, err := s.goods(ctx, companyID, ids)
	if err != nil {
		return err
	}
	moves := make([]move, len(lines))
	for i, l := range lines {
		if l.Qty.Sign() <= 0 || l.Value.Sign() < 0 {
			return fmt.Errorf("barang retur %s harus berkuantitas positif dan bernilai tidak negatif", products[l.ProductID].Code)
		}
		v := l.Value.Neg()
		moves[i] = move{
			productID: l.ProductID, date: doc.Date, qty: l.Qty.Neg(), valuation: valFixed, fixedValue: &v,
			projectID: doc.ProjectID, branchID: doc.BranchID, deptID: doc.DepartmentID, sourceType: doc.SourceType, sourceID: doc.SourceID, number: doc.Number,
			memo: memoOr(l.Memo, products[l.ProductID].Name),
		}
	}
	return s.record(ctx, companyID, moves, actor, nil)
}

// ReverseSource membalik semua pergerakan satu dokumen pada tanggal void. Nilai pembaliknya
// selalu sama persis dengan nilai pergerakan asalnya, termasuk bila nanti dihitung ulang.
func (s *service) ReverseSource(ctx context.Context, companyID uuid.UUID, sourceType string, sourceID uuid.UUID, date civil.Date, actor audit.Actor) error {
	rows, err := s.q(ctx).MovementsBySource(ctx, store.MovementsBySourceParams{CompanyID: companyID, SourceType: sourceType, SourceID: sourceID})
	if err != nil {
		return err
	}
	var moves []move
	for _, r := range rows {
		if r.Reversed {
			continue
		}
		id := r.ID
		moves = append(moves, move{
			productID: r.ProductID, date: date, qty: r.Qty.Neg(), valuation: valMirror, mirrorOf: &id,
			owned: r.Owned, counter: r.CounterAccountID, projectID: r.ProjectID, branchID: r.BranchID, deptID: r.DepartmentID, sourceType: r.SourceType,
			sourceID: r.SourceID, number: r.SourceNumber, memo: "Void " + r.SourceNumber,
		})
	}
	return s.record(ctx, companyID, moves, actor, nil)
}

// sortedIDs mengembalikan id unik yang terurut, untuk urutan penguncian yang tetap.
func sortedIDs(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func issueIDs(lines []Issue) []uuid.UUID {
	ids := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		ids[i] = l.ProductID
	}
	return ids
}

func memoOr(memo, fallback string) string {
	if m := strings.TrimSpace(memo); m != "" {
		return m
	}
	return fallback
}

// goods memastikan semua produk ada di PT ini dan berjenis barang.
func (s *service) goods(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Product, error) {
	products, err := s.ProductsByIDs(ctx, companyID, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		p, ok := products[id]
		if !ok {
			return nil, httpx.Unprocessable("product.not_found", "Produk tidak ditemukan di PT ini.")
		}
		if !p.Goods() {
			return nil, fmt.Errorf("produk %s adalah jasa dan tidak punya stok", p.Code)
		}
	}
	return products, nil
}

// lockStock mengunci saldo stok produk (urut id, supaya dua transaksi tidak saling menunggu).
func (s *service) lockStock(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) error {
	q := s.q(ctx)
	if err := q.EnsureStock(ctx, store.EnsureStockParams{CompanyID: companyID, ProductIds: ids}); err != nil {
		return err
	}
	_, err := q.LockStock(ctx, store.LockStockParams{CompanyID: companyID, ProductIds: ids})
	return err
}

// record mencatat pergerakan baru, menghitung ulang setiap produk sejak tanggal pergerakan
// barunya yang paling awal, lalu menjurnal selisih nilai pergerakan owned. primary memetakan
// dokumen ke nomornya untuk jurnal pertama dokumen itu (mis. PS/2026/10/0001).
func (s *service) record(ctx context.Context, companyID uuid.UUID, moves []move, actor audit.Actor, primary map[uuid.UUID]string) error {
	if len(moves) == 0 {
		return nil
	}
	from := map[uuid.UUID]civil.Date{}
	var ids []uuid.UUID
	for _, m := range moves {
		d, ok := from[m.productID]
		if !ok {
			ids = append(ids, m.productID)
		}
		if !ok || m.date.Before(d) {
			from[m.productID] = m.date
		}
	}
	ids = sortedIDs(ids)
	if err := s.lockStock(ctx, companyID, ids); err != nil {
		return err
	}
	products, err := s.ProductsByIDs(ctx, companyID, ids)
	if err != nil {
		return err
	}

	q := s.q(ctx)
	for _, m := range moves {
		if _, err := q.InsertMovement(ctx, store.InsertMovementParams{
			ID: uuid.Must(uuid.NewV7()), CompanyID: companyID, ProductID: m.productID, MoveDate: m.date, Qty: m.qty,
			Valuation: m.valuation, FixedValue: m.fixedValue, MirrorOf: m.mirrorOf, CostOf: m.costOf, Owned: m.owned,
			CounterAccountID: m.counter, ProjectID: m.projectID, SourceType: m.sourceType, SourceID: m.sourceID,
			SourceNumber: m.number, Memo: m.memo, BranchID: m.branchID, DepartmentID: m.deptID,
		}); err != nil {
			return err
		}
	}

	var deltas []delta
	for _, id := range ids {
		d, err := s.recalc(ctx, companyID, products[id], from[id])
		if err != nil {
			return err
		}
		deltas = append(deltas, d...)
	}
	return s.postDeltas(ctx, companyID, products, deltas, actor, primary)
}

// recalc menghitung ulang nilai semua pergerakan produk sejak tanggal from dengan rata-rata
// tertimbang (TDD: c_baru = (q_lama·c_lama + q_masuk·c_masuk) / (q_lama + q_masuk)). Stok tidak
// boleh negatif di titik mana pun; transaksi yang membuatnya negatif ditolak.
func (s *service) recalc(ctx context.Context, companyID uuid.UUID, p Product, from civil.Date) ([]delta, error) {
	q := s.q(ctx)
	var qty, value money.Amount
	prev, err := q.MovementBefore(ctx, store.MovementBeforeParams{CompanyID: companyID, ProductID: p.ID, Before: from})
	switch {
	case err == nil:
		qty, value = prev.QtyAfter, prev.ValueAfter
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	rows, err := q.MovementsFrom(ctx, store.MovementsFromParams{CompanyID: companyID, ProductID: p.ID, FromDate: from})
	if err != nil {
		return nil, err
	}

	type calc struct{ value, qty money.Amount }
	values := map[uuid.UUID]calc{}
	origin := func(id uuid.UUID) (calc, error) {
		if c, ok := values[id]; ok {
			return c, nil
		}
		r, err := q.MovementValue(ctx, store.MovementValueParams{CompanyID: companyID, ID: id})
		return calc{r.Value, r.Qty}, err
	}
	var out []delta
	for _, r := range rows {
		var v money.Amount
		switch r.Valuation {
		case valFixed:
			v = *r.FixedValue
		case valAverage:
			taken := r.Qty.Neg()
			if qty.Cmp(taken) < 0 {
				return nil, shortage(p, r, qty)
			}
			if qty.Equal(taken) {
				v = value.Neg() // stok habis: seluruh nilai keluar, tanpa sisa pembulatan
			} else {
				v = value.MulDiv(taken, qty, 0, money.HalfUp).Neg()
			}
		case valMirror:
			orig, err := origin(*r.MirrorOf)
			if err != nil {
				return nil, err
			}
			v = orig.value.Neg()
		case valRatio:
			orig, err := origin(*r.CostOf)
			if err != nil {
				return nil, err
			}
			v = orig.value.MulDiv(r.Qty, orig.qty, 0, money.HalfUp)
		default:
			return nil, fmt.Errorf("penilaian pergerakan %q tidak dikenal", r.Valuation)
		}
		qtyAfter, valueAfter := qty.Add(r.Qty), value.Add(v)
		if qtyAfter.Sign() < 0 {
			return nil, shortage(p, r, qty)
		}
		values[r.ID] = calc{v, r.Qty}
		if !v.Equal(r.Value) || !qtyAfter.Equal(r.QtyAfter) || !valueAfter.Equal(r.ValueAfter) {
			if err := q.UpdateMovementCalc(ctx, store.UpdateMovementCalcParams{
				CompanyID: companyID, ID: r.ID, Value: v, QtyAfter: qtyAfter, ValueAfter: valueAfter,
			}); err != nil {
				return nil, err
			}
		}
		if r.Owned && !v.Equal(r.PostedValue) {
			r.Value = v
			out = append(out, delta{InventoryMovement: r, amount: v.Sub(r.PostedValue), initial: r.PostedValue.IsZero()})
		}
		qty, value = qtyAfter, valueAfter
	}
	return out, q.UpdateStock(ctx, store.UpdateStockParams{CompanyID: companyID, ProductID: p.ID, Qty: qty, Value: value})
}

func shortage(p Product, r store.InventoryMovement, available money.Amount) error {
	need := r.Qty.Neg()
	return httpx.Unprocessable("inventory.insufficient", fmt.Sprintf(
		"Stok %s %s tidak cukup untuk %s tanggal %s: tersedia %s %s, dibutuhkan %s %s.",
		p.Code, p.Name, r.SourceNumber, r.MoveDate.Indonesian(), FormatQty(available), p.Unit, FormatQty(need), p.Unit))
}

// postDeltas menjurnal selisih nilai per dokumen dan tanggal: satu jurnal berisi akun
// persediaan produk melawan akun HPP atau selisih persediaan, dengan dimensi proyeknya.
func (s *service) postDeltas(ctx context.Context, companyID uuid.UUID, products map[uuid.UUID]Product, deltas []delta, actor audit.Actor, primary map[uuid.UUID]string) error {
	type groupKey struct {
		source uuid.UUID
		date   civil.Date
	}
	groups := map[groupKey][]delta{}
	var keys []groupKey
	for _, d := range deltas {
		k := groupKey{d.SourceID, d.MoveDate}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], d)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := groups[keys[i]][0], groups[keys[j]][0]
		if c := a.MoveDate.Compare(b.MoveDate); c != 0 {
			return c < 0
		}
		return a.SourceNumber < b.SourceNumber
	})

	q := s.q(ctx)
	for _, k := range keys {
		group := groups[k]
		lines := deltaLines(products, group)
		if len(lines) > 0 {
			first := group[0]
			initial := true
			mirror := true
			returned := true
			for _, d := range group {
				initial = initial && d.initial
				mirror = mirror && d.MirrorOf != nil
				returned = returned && d.CostOf != nil
			}
			memo := costMemo(first.SourceType, first.SourceNumber, initial, mirror)
			if returned && initial {
				memo = "Pembalikan HPP retur " + first.SourceNumber
			}
			number := ""
			if n, ok := primary[first.SourceID]; ok && initial && !mirror {
				number = n
				delete(primary, first.SourceID)
			} else {
				var err error
				if number, err = s.Ledger.NextNumber(ctx, companyID, seriesCost, first.MoveDate); err != nil {
					return err
				}
			}
			// Jurnal penyesuaian stok bersumber dokumennya sendiri; HPP penjualan bersumber
			// inventory.cost dengan id faktur, supaya buku besar bisa menautkan dokumennya.
			sourceType, sourceID := SourceCost, first.SourceID
			switch first.SourceType {
			case SourceAdjustment:
				sourceType = SourceAdjustment
			case sourceCreditNote:
				sourceType = SourceReturnCost
			case sourceDaily:
				sourceType = SourceDailyCost
			}
			if _, err := s.Ledger.Post(ctx, ledger.EntryDraft{
				CompanyID: companyID, Kind: ledger.General, Date: first.MoveDate, Memo: memo, SourceType: sourceType,
				SourceID: &sourceID, Number: number, Lines: lines, Actor: actor,
			}); err != nil {
				return err
			}
		}
		for _, d := range group {
			if err := q.MarkMovementPosted(ctx, store.MarkMovementPostedParams{CompanyID: companyID, ID: d.ID, PostedValue: d.Value}); err != nil {
				return err
			}
		}
	}
	return nil
}

// deltaLines menjumlahkan selisih per akun dan proyek. Selisih positif (nilai persediaan
// bertambah) mendebit persediaan dan mengkredit akun lawan; negatif sebaliknya.
func deltaLines(products map[uuid.UUID]Product, group []delta) []ledger.LineDraft {
	type key struct {
		account, project, branch, dept uuid.UUID
	}
	net := map[key]money.Amount{}
	memo := map[key]string{}
	var order []key
	add := func(account uuid.UUID, project *uuid.UUID, d delta, a money.Amount, m string) {
		k := key{account: account}
		if project != nil {
			k.project = *project
		}
		if d.BranchID != nil {
			k.branch = *d.BranchID
		}
		if d.DepartmentID != nil {
			k.dept = *d.DepartmentID
		}
		if _, ok := net[k]; !ok {
			order = append(order, k)
			memo[k] = m
		}
		net[k] = net[k].Add(a)
	}
	for _, d := range group {
		p := products[d.ProductID]
		add(*p.InventoryAccountID, d.ProjectID, d, d.amount, "Persediaan "+p.Name)
		add(*d.CounterAccountID, d.ProjectID, d, d.amount.Neg(), d.Memo)
	}
	var lines []ledger.LineDraft
	for _, k := range order {
		a := net[k]
		if a.IsZero() {
			continue
		}
		ln := ledger.LineDraft{AccountID: k.account, Memo: truncate(memo[k], 250)}
		if k.project != uuid.Nil {
			p := k.project
			ln.ProjectID = &p
		}
		if k.branch != uuid.Nil {
			b := k.branch
			ln.BranchID = &b
		}
		if k.dept != uuid.Nil {
			dp := k.dept
			ln.DepartmentID = &dp
		}
		if a.Sign() > 0 {
			ln.Debit = a
		} else {
			ln.Credit = a.Neg()
		}
		lines = append(lines, ln)
	}
	return lines
}

func costMemo(sourceType, number string, initial, mirror bool) string {
	label, lower := "HPP", "HPP"
	if sourceType == SourceAdjustment {
		label, lower = "Penyesuaian stok", "penyesuaian stok"
	}
	switch {
	case mirror:
		return "Pembalikan " + lower + " " + number
	case initial:
		return label + " " + number
	default:
		return "Koreksi " + lower + " " + number + " karena transaksi bertanggal mundur"
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
