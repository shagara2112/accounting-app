package coretax

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/modules/purchase"
	"lajur/internal/modules/tax"
	"lajur/internal/modules/tax/internal/store"
	"lajur/internal/platform/audit"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/money"
	"lajur/internal/platform/sheet"
)

// Bukti potong PPh 23, 4(2), dan 26 (TAX-08). Setiap tagihan yang memotong PPh menghasilkan
// satu bukti potong: BPPU untuk pemasok dalam negeri, BPNR untuk pemasok luar negeri (PPh 26).
// Alurnya sama dengan faktur keluaran: lengkapi kode objek pajak, ekspor XML, unggah di
// Coretax, lalu catat nomor bukti potong yang terbit. Nilainya bersih sesudah nota debit.

type Slip struct {
	BillID            uuid.UUID    `json:"billId"`
	Number            string       `json:"number"`
	Date              civil.Date   `json:"date"`
	SupplierID        uuid.UUID    `json:"supplierId"`
	SupplierName      string       `json:"supplierName"`
	CounterpartTIN    string       `json:"counterpartTin"`
	Foreign           bool         `json:"foreign"`
	Country           string       `json:"country"`
	SupplierInvoiceNo string       `json:"supplierInvoiceNo"`
	Code              string       `json:"code"` // kode PPh tagihan, mis. PPH23
	CodeName          string       `json:"codeName"`
	Kind              string       `json:"kind"` // bppu atau bpnr
	RateBP            int          `json:"rateBp"`
	TaxBase           money.Amount `json:"taxBase"`
	Amount            money.Amount `json:"amount"`
	Noted             bool         `json:"noted"` // ada nota debit yang mengurangi
	TaxObjectCode     string       `json:"taxObjectCode"`
	TaxObjectName     string       `json:"taxObjectName"`
	Facility          string       `json:"facility"`
	FacilityNumber    string       `json:"facilityNumber"`
	Deemed            float64      `json:"deemed"`
	Document          string       `json:"document"`
	DocumentNumber    string       `json:"documentNumber"`
	DocumentDate      civil.Date   `json:"documentDate"`
	WithholdingDate   civil.Date   `json:"withholdingDate"`
	Status            string       `json:"status"`
	SlipNumber        *string      `json:"slipNumber,omitempty"`
	ApprovedDate      *civil.Date  `json:"approvedDate,omitempty"`
	Changed           bool         `json:"changed"` // nilainya berubah sesudah diekspor (nota debit)
	Note              string       `json:"note"`
	Problems          []string     `json:"problems"`
}

type SlipList struct {
	Month    string            `json:"month"`
	Problems []string          `json:"problems"`
	Items    []Slip            `json:"items"`
	Objects  map[string]string `json:"objects"` // KOP bawaan per kode PPh
}

type SlipUpdate struct {
	TaxObjectCode   string     `json:"taxObjectCode"`
	Facility        string     `json:"facility"`
	FacilityNumber  string     `json:"facilityNumber"`
	Deemed          float64    `json:"deemed"`
	Document        string     `json:"document"`
	DocumentNumber  string     `json:"documentNumber"`
	DocumentDate    civil.Date `json:"documentDate"`
	WithholdingDate civil.Date `json:"withholdingDate"`
	Note            string     `json:"note"`
}

// WithholdingObjects mengembalikan KOP bawaan per kode PPh. Kode PPh 4(2) bawaan Lajur sudah
// punya pasangan KOP yang pasti; PPh 23 dan 26 bergantung pada jenis jasanya.
func (s *service) WithholdingObjects(ctx context.Context, companyID uuid.UUID) (map[string]string, error) {
	out := map[string]string{
		"PPH42-SEWA": "28-403-02", "PPH42-K175": "28-409-22", "PPH42-K265": "28-409-24", "PPH42-K4": "28-409-23",
	}
	r, err := s.q(ctx).GetTaxSettings(ctx, companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	saved := map[string]string{}
	if len(r.WithholdingObjects) > 0 {
		if err := json.Unmarshal(r.WithholdingObjects, &saved); err != nil {
			return nil, err
		}
	}
	for k, v := range saved {
		if v == "" {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out, nil
}

func (s *service) SetWithholdingObjects(ctx context.Context, companyID uuid.UUID, objects map[string]string, actor audit.Actor) (map[string]string, error) {
	f := map[string]string{}
	clean := map[string]string{}
	for code, kop := range objects {
		kop = strings.TrimSpace(kop)
		if kop != "" && !kopPattern.MatchString(kop) {
			f[code] = "Format kode objek pajak 99-999-99."
		}
		clean[strings.TrimSpace(code)] = kop
	}
	if len(f) > 0 {
		return nil, httpx.Unprocessable("tax.objects", firstField(f)).WithFields(f)
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	var out map[string]string
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		if err := s.q(ctx).SetWithholdingObjects(ctx, store.SetWithholdingObjectsParams{CompanyID: companyID, WithholdingObjects: raw, UpdatedBy: actor.UserRef()}); err != nil {
			return err
		}
		if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.withholding_objects", ObjectType: "company", ObjectID: companyID.String(), IP: actor.IP,
			After: map[string]any{"objects": clean},
		}); err != nil {
			return err
		}
		out, err = s.WithholdingObjects(ctx, companyID)
		return err
	})
	return out, err
}

var kopPattern = regexp.MustCompile(`^[0-9]{2}-[0-9]{3}-[0-9]{2}$`)

func (s *service) slipStates(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]store.TaxWithholdingSlip, error) {
	rows, err := s.q(ctx).WithholdingSlipStates(ctx, store.WithholdingSlipStatesParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]store.TaxWithholdingSlip, len(rows))
	for _, r := range rows {
		out[r.BillID] = r
	}
	return out, nil
}

func defaultSlip(b purchase.WithholdingBill, companyID uuid.UUID) store.TaxWithholdingSlip {
	doc := b.SupplierInvoiceNo
	if doc == "" {
		doc = b.Number
	}
	date := b.Date
	return store.TaxWithholdingSlip{
		BillID: b.BillID, CompanyID: companyID, Facility: "N/A", Document: "CommercialInvoice", DocumentNumber: doc,
		DocumentDate: &date, WithholdingDate: &date, Status: Ready,
	}
}

func amountFloat(a *money.Amount) float64 {
	if a == nil {
		return 0
	}
	var f float64
	fmt.Sscan(a.String(), &f)
	return f
}

// slip menyusun bukti potong dari tagihan, status tersimpan, dan KOP bawaan.
func slip(b purchase.WithholdingBill, r store.TaxWithholdingSlip, code tax.Code, objects map[string]string) Slip {
	ref := Ref()
	out := Slip{
		BillID: b.BillID, Number: b.Number, Date: b.Date, SupplierID: b.Supplier.ID, SupplierName: b.Supplier.Name,
		Foreign: b.Supplier.Foreign(), Country: b.Supplier.CountryCode, SupplierInvoiceNo: b.SupplierInvoiceNo,
		Code: b.Code, CodeName: code.Name, Kind: BatchBPPU, RateBP: code.RateBP,
		TaxBase: b.DPP.Sub(b.NotedDPP), Amount: b.Withholding.Sub(b.NotedWithholding), Noted: b.NotedWithholding.Sign() > 0,
		Facility: r.Facility, FacilityNumber: r.FacilityNumber, Document: r.Document, DocumentNumber: r.DocumentNumber,
		Status: r.Status, SlipNumber: r.SlipNumber, ApprovedDate: r.ApprovedDate, Note: r.Note, Problems: []string{},
	}
	if r.DocumentDate != nil {
		out.DocumentDate = *r.DocumentDate
	}
	if r.WithholdingDate != nil {
		out.WithholdingDate = *r.WithholdingDate
	}
	objectsList, facilities := ref.BPPUObjects, ref.BPPUFacilities
	if code.Article == tax.ArticlePPh26 {
		out.Kind, objectsList, facilities = BatchBPNR, ref.BPNRObjects, ref.BPNRFacilities
		out.CounterpartTIN = b.Supplier.ForeignTIN
	} else {
		out.CounterpartTIN = buyerTIN(b.Supplier)
	}
	out.TaxObjectCode = objects[b.Code]
	if r.TaxObjectCode != nil {
		out.TaxObjectCode = *r.TaxObjectCode
	}
	obj, known := findObject(objectsList, out.TaxObjectCode)
	out.TaxObjectName = obj.Name
	out.Deemed = 100
	if len(obj.Deemed) > 0 {
		out.Deemed = obj.Deemed[0]
	}
	if r.Deemed != nil {
		out.Deemed = amountFloat(r.Deemed)
	}
	if r.ExportedAmount != nil && (out.Status == Exported || out.Status == Approved) && !r.ExportedAmount.Equal(out.Amount) {
		out.Changed = true
	}

	p := func(msg string) { out.Problems = append(out.Problems, msg) }
	switch {
	case out.Amount.Sign() <= 0:
		p("PPh tagihan ini sudah dibatalkan seluruhnya oleh nota debit.")
	case out.TaxObjectCode == "":
		p("Pilih kode objek pajak.")
	case !known:
		p(fmt.Sprintf("Kode objek pajak %s tidak ada di referensi %s.", out.TaxObjectCode, strings.ToUpper(out.Kind)))
	case out.Facility == "N/A" && obj.Rate*100 != float64(code.RateBP):
		p(fmt.Sprintf("Tarif kode objek pajak %s%% berbeda dengan tarif PPh tagihan %s%%.", rateText(int(obj.Rate*100)), rateText(code.RateBP)))
	}
	if !hasItem(facilities, out.Facility) {
		p(fmt.Sprintf("Fasilitas %s tidak dikenal.", out.Facility))
	}
	if out.Facility == "COD" && out.FacilityNumber == "" {
		p("Isi nomor Surat Keterangan Domisili (SKD).")
	}
	if !hasItem(ref.Documents, out.Document) {
		p(fmt.Sprintf("Jenis dokumen %s tidak dikenal.", out.Document))
	}
	if out.DocumentNumber == "" {
		p("Isi nomor dokumen referensi.")
	}
	if out.Kind == BatchBPNR {
		if out.CounterpartTIN == "" {
			p("Isi TIN luar negeri pemasok di menu Kontak.")
		}
		if b.Supplier.Address == "" {
			p("Isi alamat pemasok di menu Kontak.")
		}
	} else if len(out.CounterpartTIN) != 16 {
		p("Pemasok belum punya NPWP atau NIK 16 digit.")
	}
	return out
}

// Slips: bukti potong dari tagihan bertanggal masa month.
func (s *service) Slips(ctx context.Context, companyID uuid.UUID, month civil.Date) (SlipList, error) {
	month = month.MonthStart()
	company, err := s.Core.Company(ctx, companyID)
	if err != nil {
		return SlipList{}, err
	}
	objects, err := s.WithholdingObjects(ctx, companyID)
	if err != nil {
		return SlipList{}, err
	}
	bills, err := s.Purchase.WithholdingBills(ctx, companyID, month, month.AddMonths(1))
	if err != nil {
		return SlipList{}, err
	}
	table, err := s.Tax.Table(ctx)
	if err != nil {
		return SlipList{}, err
	}
	ids := make([]uuid.UUID, len(bills))
	for i, b := range bills {
		ids[i] = b.BillID
	}
	states, err := s.slipStates(ctx, companyID, ids)
	if err != nil {
		return SlipList{}, err
	}
	out := SlipList{Month: month.MonthKey(), Problems: []string{}, Items: []Slip{}, Objects: objects}
	if len(company.NPWP) != 16 {
		out.Problems = append(out.Problems, "NPWP PT harus 16 digit untuk mengekspor bukti potong. Lengkapi di menu Pajak → Pengaturan atau profil PT.")
	}
	for _, b := range bills {
		r, ok := states[b.BillID]
		if !ok {
			r = defaultSlip(b, companyID)
		}
		code, err := table.Find(b.Code, b.Date)
		if err != nil {
			return SlipList{}, err
		}
		out.Items = append(out.Items, slip(b, r, code, objects))
	}
	return out, nil
}

// slipFor membaca satu bukti potong beserta statusnya yang dikunci untuk diubah.
func (s *service) slipFor(ctx context.Context, companyID, billID uuid.UUID) (purchase.WithholdingBill, store.TaxWithholdingSlip, tax.Code, error) {
	b, err := s.Purchase.Bill(ctx, companyID, billID)
	if err != nil {
		return purchase.WithholdingBill{}, store.TaxWithholdingSlip{}, tax.Code{}, err
	}
	rows, err := s.Purchase.WithholdingBills(ctx, companyID, b.Date, b.Date.AddDays(1))
	if err != nil {
		return purchase.WithholdingBill{}, store.TaxWithholdingSlip{}, tax.Code{}, err
	}
	var wb *purchase.WithholdingBill
	for i := range rows {
		if rows[i].BillID == billID {
			wb = &rows[i]
		}
	}
	if wb == nil {
		return purchase.WithholdingBill{}, store.TaxWithholdingSlip{}, tax.Code{}, httpx.Unprocessable("tax.slip_bill", "Hanya tagihan diposting yang memotong PPh yang punya bukti potong.")
	}
	r, err := s.q(ctx).GetWithholdingSlipForUpdate(ctx, store.GetWithholdingSlipForUpdateParams{CompanyID: companyID, BillID: billID})
	if errors.Is(err, pgx.ErrNoRows) {
		r = defaultSlip(*wb, companyID)
	} else if err != nil {
		return purchase.WithholdingBill{}, store.TaxWithholdingSlip{}, tax.Code{}, err
	}
	table, err := s.Tax.Table(ctx)
	if err != nil {
		return purchase.WithholdingBill{}, store.TaxWithholdingSlip{}, tax.Code{}, err
	}
	code, err := table.Find(wb.Code, wb.Date)
	return *wb, r, code, err
}

func (s *service) saveSlip(ctx context.Context, r store.TaxWithholdingSlip, actor audit.Actor) (store.TaxWithholdingSlip, error) {
	return s.q(ctx).UpsertWithholdingSlip(ctx, store.UpsertWithholdingSlipParams{
		BillID: r.BillID, CompanyID: r.CompanyID, TaxObjectCode: r.TaxObjectCode, Facility: r.Facility, FacilityNumber: r.FacilityNumber,
		Deemed: r.Deemed, Document: r.Document, DocumentNumber: r.DocumentNumber, DocumentDate: r.DocumentDate,
		WithholdingDate: r.WithholdingDate, Status: r.Status, SlipNumber: r.SlipNumber, ApprovedDate: r.ApprovedDate,
		ExportedBase: r.ExportedBase, ExportedAmount: r.ExportedAmount, LastBatchID: r.LastBatchID, Note: r.Note, UpdatedBy: actor.UserRef(),
	})
}

// UpdateSlip mengubah isian bukti potong yang belum terbit.
func (s *service) UpdateSlip(ctx context.Context, companyID, billID uuid.UUID, in SlipUpdate, actor audit.Actor) (Slip, error) {
	var out Slip
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		b, r, code, err := s.slipFor(ctx, companyID, billID)
		if err != nil {
			return err
		}
		if r.Status == Approved || r.Status == Cancelled {
			return httpx.Conflict("tax.slip_locked", "Bukti potong ini sudah terbit di Coretax; koreksi lewat pembetulan di Coretax.")
		}
		f := map[string]string{}
		in.TaxObjectCode = strings.TrimSpace(in.TaxObjectCode)
		switch {
		case in.TaxObjectCode == "":
			r.TaxObjectCode = nil
		case !kopPattern.MatchString(in.TaxObjectCode):
			f["taxObjectCode"] = "Format kode objek pajak 99-999-99."
		default:
			k := in.TaxObjectCode
			r.TaxObjectCode = &k
		}
		if in.Facility != "" {
			r.Facility = in.Facility
		}
		r.FacilityNumber = strings.TrimSpace(in.FacilityNumber)
		if utf8.RuneCountInString(r.FacilityNumber) > 60 {
			f["facilityNumber"] = "Nomor fasilitas maksimal 60 karakter."
		}
		if in.Deemed > 0 {
			if in.Deemed > 100 {
				f["deemed"] = "Norma 0–100%."
			} else {
				d := money.MustParse(fmt.Sprintf("%.4f", in.Deemed))
				r.Deemed = &d
			}
		}
		if in.Document != "" {
			r.Document = in.Document
		}
		r.DocumentNumber = strings.TrimSpace(in.DocumentNumber)
		if r.DocumentNumber == "" || utf8.RuneCountInString(r.DocumentNumber) > 60 {
			f["documentNumber"] = "Nomor dokumen referensi wajib diisi (maksimal 60 karakter)."
		}
		if !in.DocumentDate.IsZero() {
			d := in.DocumentDate
			r.DocumentDate = &d
		}
		if !in.WithholdingDate.IsZero() {
			if !in.WithholdingDate.SameMonth(b.Date) {
				f["withholdingDate"] = "Tanggal pemotongan harus di masa tagihan (" + b.Date.MonthLabel() + ")."
			} else {
				d := in.WithholdingDate
				r.WithholdingDate = &d
			}
		}
		r.Note = strings.TrimSpace(in.Note)
		if len(f) > 0 {
			return httpx.Unprocessable("tax.slip_invalid", firstField(f)).WithFields(f)
		}
		saved, err := s.saveSlip(ctx, r, actor)
		if err != nil {
			return err
		}
		objects, err := s.WithholdingObjects(ctx, companyID)
		if err != nil {
			return err
		}
		out = slip(b, saved, code, objects)
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.slip.update", ObjectType: "purchase_bill", ObjectID: billID.String(), IP: actor.IP,
			After: map[string]any{"taxObjectCode": out.TaxObjectCode, "facility": out.Facility, "document": out.Document, "documentNumber": out.DocumentNumber},
		})
		return err
	})
	return out, err
}

// SetSlipObject memasang satu kode objek pajak ke beberapa bukti potong sekaligus.
func (s *service) SetSlipObject(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID, kop string, actor audit.Actor) error {
	if !kopPattern.MatchString(kop) {
		return httpx.Unprocessable("tax.objects", "Format kode objek pajak 99-999-99.")
	}
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		for _, id := range ids {
			_, r, _, err := s.slipFor(ctx, companyID, id)
			if err != nil {
				return err
			}
			if r.Status != Ready && r.Status != Exported {
				continue
			}
			k := kop
			r.TaxObjectCode = &k
			if _, err := s.saveSlip(ctx, r, actor); err != nil {
				return err
			}
		}
		_, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.slip.object", ObjectType: "company", ObjectID: companyID.String(), IP: actor.IP,
			After: map[string]any{"taxObjectCode": kop, "slips": len(ids)},
		})
		return err
	})
}

// ExportSlips membuat XML BPPU dan/atau BPNR untuk bukti potong terpilih masa month.
func (s *service) ExportSlips(ctx context.Context, companyID uuid.UUID, month civil.Date, ids []uuid.UUID, actor audit.Actor) ([]Batch, error) {
	month = month.MonthStart()
	if len(ids) == 0 || len(ids) > 1000 {
		return nil, httpx.Unprocessable("tax.selection", "Pilih 1–1.000 bukti potong.")
	}
	var out []Batch
	err := db.Run(ctx, s.Pool, func(ctx context.Context) error {
		company, err := s.Core.Company(ctx, companyID)
		if err != nil {
			return err
		}
		if len(company.NPWP) != 16 {
			return httpx.Unprocessable("tax.company_profile", "NPWP PT harus 16 digit sebelum mengekspor bukti potong.")
		}
		list, err := s.Slips(ctx, companyID, month)
		if err != nil {
			return err
		}
		byID := map[uuid.UUID]Slip{}
		for _, it := range list.Items {
			byID[it.BillID] = it
		}
		groups := map[string][]Slip{}
		var problems []string
		for _, id := range ids {
			it, ok := byID[id]
			switch {
			case !ok:
				problems = append(problems, "tagihan bukan masa "+month.MonthLabel())
			case it.Status != Ready && it.Status != Exported:
				problems = append(problems, it.Number+": bukti potong sudah terbit atau dibatalkan")
			case len(it.Problems) > 0:
				problems = append(problems, it.Number+": "+it.Problems[0])
			default:
				groups[it.Kind] = append(groups[it.Kind], it)
			}
		}
		if len(problems) > 0 {
			return httpx.Unprocessable("tax.slip_problems", "Belum bisa diekspor: "+strings.Join(problems, "; "))
		}
		w := XMLWithholder{NPWP: company.NPWP, NITKU: company.NITKU}
		for _, kind := range []string{BatchBPPU, BatchBPNR} {
			items := groups[kind]
			if len(items) == 0 {
				continue
			}
			slips := make([]XMLSlip, len(items))
			for i, it := range items {
				slips[i] = XMLSlip{
					Period: month, CounterpartTIN: it.CounterpartTIN, Name: it.SupplierName, Country: it.Country,
					Facility: it.Facility, FacilityNumber: it.FacilityNumber, TaxObjectCode: it.TaxObjectCode, TaxBase: it.TaxBase,
					Deemed: it.Deemed, RateBP: it.RateBP, Document: it.Document, DocumentNumber: it.DocumentNumber,
					DocumentDate: it.DocumentDate, WithholdingDate: it.WithholdingDate,
				}
				if kind == BatchBPNR {
					c, err := s.Core.Contact(ctx, companyID, it.SupplierID)
					if err != nil {
						return err
					}
					slips[i].Address = c.Address
				} else {
					c, err := s.Core.Contact(ctx, companyID, it.SupplierID)
					if err != nil {
						return err
					}
					slips[i].CounterpartTKU = c.NITKU
				}
			}
			var content []byte
			format := FormatBPPUV1
			if kind == BatchBPNR {
				format = FormatBPNRV1
				content, err = BuildBPNR(w, slips)
			} else {
				content, err = BuildBPPU(w, slips)
			}
			if err != nil {
				return err
			}
			batchID := uuid.Must(uuid.NewV7())
			b := Batch{
				ID: batchID, Period: month.MonthKey(), Format: format, InvoiceCount: len(items), Kind: kind,
				FileName: fmt.Sprintf("%s-%s-%s.xml", strings.ToUpper(kind), month.MonthKey(), batchID.String()[:8]),
			}
			if err := s.q(ctx).InsertExportBatch(ctx, store.InsertExportBatchParams{
				ID: batchID, CompanyID: companyID, Period: month, Format: format, InvoiceCount: int32(len(items)),
				FileName: b.FileName, Content: content, CreatedBy: actor.UserRef(), Kind: kind,
			}); err != nil {
				return err
			}
			for _, it := range items {
				_, r, _, err := s.slipFor(ctx, companyID, it.BillID)
				if err != nil {
					return err
				}
				base, amount := it.TaxBase, it.Amount
				r.Status, r.ExportedBase, r.ExportedAmount, r.LastBatchID = Exported, &base, &amount, &batchID
				if r.TaxObjectCode == nil {
					k := it.TaxObjectCode
					r.TaxObjectCode = &k
				}
				if _, err := s.saveSlip(ctx, r, actor); err != nil {
					return err
				}
			}
			if _, err := audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
				UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.slip.export", ObjectType: "tax_export", ObjectID: batchID.String(), IP: actor.IP,
				After: map[string]any{"kind": kind, "month": month.MonthKey(), "slips": len(items), "file": b.FileName},
			}); err != nil {
				return err
			}
			out = append(out, b)
		}
		return nil
	})
	return out, err
}

var slipNumberPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9./-]{0,39}$`)

// ApproveSlip mencatat nomor bukti potong yang terbit di Coretax.
func (s *service) ApproveSlip(ctx context.Context, companyID, billID uuid.UUID, number string, date civil.Date, actor audit.Actor) error {
	number = strings.ToUpper(strings.TrimSpace(number))
	if !slipNumberPattern.MatchString(number) {
		msg := "Isi nomor bukti potong dari Coretax (huruf dan angka)."
		return httpx.Unprocessable("tax.slip_number", msg).WithFields(map[string]string{"number": msg})
	}
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		_, r, _, err := s.slipFor(ctx, companyID, billID)
		if err != nil {
			return err
		}
		if r.Status == Approved || r.Status == Cancelled {
			return httpx.Conflict("tax.slip_locked", "Bukti potong ini sudah tercatat terbit.")
		}
		if other, err := s.q(ctx).WithholdingSlipByNumber(ctx, store.WithholdingSlipByNumberParams{CompanyID: companyID, SlipNumber: &number}); err == nil && other != billID {
			msg := "Nomor bukti potong ini sudah dicatat di tagihan lain."
			return httpx.Unprocessable("tax.slip_number", msg).WithFields(map[string]string{"number": msg})
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if date.IsZero() {
			date = civil.Today(civil.Jakarta)
		}
		r.Status, r.SlipNumber, r.ApprovedDate = Approved, &number, &date
		if _, err := s.saveSlip(ctx, r, actor); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.slip.approve", ObjectType: "purchase_bill", ObjectID: billID.String(), IP: actor.IP,
			After: map[string]any{"slipNumber": number, "date": date.String()},
		})
		return err
	})
}

// ResetSlip mengembalikan bukti potong yang sudah diekspor (belum terbit) ke status siap,
// mis. bila draf di Coretax dihapus. CancelSlip menandai bukti potong terbit yang dibatalkan.
func (s *service) ResetSlip(ctx context.Context, companyID, billID uuid.UUID, cancel bool, note string, actor audit.Actor) error {
	return db.Run(ctx, s.Pool, func(ctx context.Context) error {
		_, r, _, err := s.slipFor(ctx, companyID, billID)
		if err != nil {
			return err
		}
		switch {
		case cancel && r.Status != Approved:
			return httpx.Conflict("tax.slip_state", "Hanya bukti potong yang sudah terbit yang bisa ditandai dibatalkan.")
		case cancel:
			r.Status = Cancelled
		case r.Status != Exported:
			return httpx.Conflict("tax.slip_state", "Hanya bukti potong yang sudah diekspor dan belum terbit yang bisa dikembalikan.")
		default:
			r.Status, r.ExportedBase, r.ExportedAmount = Ready, nil, nil
		}
		if note = strings.TrimSpace(note); note != "" {
			r.Note = note
		}
		if _, err := s.saveSlip(ctx, r, actor); err != nil {
			return err
		}
		_, err = audit.Write(ctx, db.Conn(ctx, s.Pool), audit.Entry{
			UserID: actor.UserRef(), CompanyID: &companyID, Action: "tax.slip.reset", ObjectType: "purchase_bill", ObjectID: billID.String(), IP: actor.IP,
			After: map[string]any{"status": r.Status, "note": note},
		})
		return err
	})
}

// ImportSlipNumbers mencatat nomor bukti potong dari berkas daftar bukti potong Coretax (Excel
// atau CSV): kolom nomor bukti potong dicocokkan lewat nomor dokumen referensinya.
func (s *service) ImportSlipNumbers(ctx context.Context, companyID uuid.UUID, month civil.Date, name string, data []byte, actor audit.Actor) (ImportResult, error) {
	rows, err := sheet.Read(name, data)
	if err != nil {
		return ImportResult{}, err
	}
	numCol, docCol, dateCol, header := -1, -1, -1, -1
	for i := 0; i < len(rows) && i < 30 && header < 0; i++ {
		n, d, t := -1, -1, -1
		for j, h := range rows[i] {
			h = strings.ToLower(strings.TrimSpace(h))
			switch {
			case (strings.Contains(h, "nomor bukti") || strings.Contains(h, "no bukti") || strings.Contains(h, "nomor bupot")) && n < 0:
				n = j
			case strings.Contains(h, "dokumen") && (strings.Contains(h, "nomor") || strings.Contains(h, "no")) && d < 0:
				d = j
			case strings.Contains(h, "tanggal") && t < 0:
				t = j
			}
		}
		if n >= 0 && d >= 0 {
			numCol, docCol, dateCol, header = n, d, t, i
		}
	}
	if header < 0 {
		return ImportResult{}, httpx.Unprocessable("tax.slip_import", "Kolom Nomor Bukti Potong dan Nomor Dokumen Referensi tidak ditemukan.")
	}
	list, err := s.Slips(ctx, companyID, month)
	if err != nil {
		return ImportResult{}, err
	}
	out := ImportResult{Recorded: []string{}, Skipped: []string{}}
	err = db.Run(ctx, s.Pool, func(ctx context.Context) error {
		for i := header + 1; i < len(rows); i++ {
			r := rows[i]
			if numCol >= len(r) || docCol >= len(r) {
				continue
			}
			number, doc := strings.TrimSpace(r[numCol]), strings.TrimSpace(r[docCol])
			if number == "" || doc == "" {
				continue
			}
			var date civil.Date
			if dateCol >= 0 && dateCol < len(r) {
				date, _ = sheet.Date(strings.TrimSpace(r[dateCol]))
			}
			var match *Slip
			for k := range list.Items {
				if strings.EqualFold(list.Items[k].DocumentNumber, doc) && list.Items[k].Status != Approved && list.Items[k].Status != Cancelled {
					match = &list.Items[k]
					break
				}
			}
			if match == nil {
				out.Skipped = append(out.Skipped, fmt.Sprintf("Baris %d: dokumen %s tidak ditemukan di bukti potong %s yang belum terbit.", i+1, doc, month.MonthLabel()))
				continue
			}
			if err := s.ApproveSlip(ctx, companyID, match.BillID, number, date, actor); err != nil {
				var p *httpx.Problem
				if errors.As(err, &p) {
					out.Skipped = append(out.Skipped, fmt.Sprintf("Baris %d (%s): %s", i+1, doc, p.Detail))
					continue
				}
				return err
			}
			match.Status = Approved
			out.Recorded = append(out.Recorded, match.Number)
		}
		return nil
	})
	return out, err
}

// CanVoidBill menolak void tagihan yang bukti potongnya sudah terbit di Coretax, atau yang PPN
// masukannya sudah dijurnal tidak dikreditkan.
func (s *service) CanVoidBill(ctx context.Context, companyID, billID uuid.UUID) error {
	slips, err := s.slipStates(ctx, companyID, []uuid.UUID{billID})
	if err != nil {
		return err
	}
	if r, ok := slips[billID]; ok && r.Status == Approved {
		return httpx.Unprocessable("tax.slip_locked", fmt.Sprintf(
			"Bukti potong %s sudah terbit di Coretax. Batalkan dulu di Coretax, tandai dibatalkan di menu Pajak, lalu void tagihannya.", deref(r.SlipNumber)))
	}
	inputs, err := s.inputStates(ctx, companyID, []uuid.UUID{billID})
	if err != nil {
		return err
	}
	if r, ok := inputs[billID]; ok && r.ReclassEntryID != nil {
		return httpx.Unprocessable("tax.input_reclass", "PPN masukan tagihan ini sudah dijurnal tidak dikreditkan. Tandai dikreditkan lagi di menu Pajak → Faktur masukan, lalu void tagihannya.")
	}
	return nil
}

// CanChangeBillVAT menolak nota debit (atau void-nya) atas tagihan yang PPN masukannya sudah
// dijurnal tidak dikreditkan: jurnal itu memakai PPN bersih saat dibuat.
func (s *service) CanChangeBillVAT(ctx context.Context, companyID, billID uuid.UUID) error {
	inputs, err := s.inputStates(ctx, companyID, []uuid.UUID{billID})
	if err != nil {
		return err
	}
	if r, ok := inputs[billID]; ok && r.ReclassEntryID != nil {
		return httpx.Unprocessable("tax.input_reclass", "PPN masukan tagihan ini sudah dijurnal tidak dikreditkan. Tandai dikreditkan dulu di menu Pajak → Faktur masukan, buat atau void nota debitnya, lalu tandai tidak dikreditkan lagi.")
	}
	return nil
}
