package coretax

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// Eksportir XML bukti potong unifikasi Coretax (TAX-08). Struktur dan urutan elemen mengikuti
// template XML resmi DJP (bppu.xml dan bpnr.xml, 7 November 2024; salinannya ada di testdata
// dan dibandingkan oleh tes). Bila DJP merilis template baru, buat versi baru di samping ini.
const (
	FormatBPPUV1 = "coretax-bppu/v1"
	FormatBPNRV1 = "coretax-bpnr/v1"
)

// XMLWithholder adalah PT pemotong.
type XMLWithholder struct {
	NPWP  string // 16 digit
	NITKU string // 22 digit; kosong = NPWP + 000000
}

// XMLSlip adalah satu bukti potong. Untuk BPPU, CounterpartTIN = NPWP/NIK 16 digit; untuk BPNR,
// CounterpartTIN = TIN di negara penerima penghasilan.
type XMLSlip struct {
	Period          civil.Date // masa pajak (bulan)
	CounterpartTIN  string
	CounterpartTKU  string // BPPU: NITKU penerima; kosong = TIN + 000000
	Name            string // BPNR
	Country         string // BPNR, ISO alfa-3
	Address         string // BPNR
	Facility        string // N/A, DTP, PP23, SKB, COD (SKD), ETC
	FacilityNumber  string // BPNR: nomor SKD
	TaxObjectCode   string
	TaxBase         money.Amount
	Deemed          float64 // BPNR: norma penghasilan neto (%)
	RateBP          int     // basis poin: 200 = 2%
	Document        string
	DocumentNumber  string
	DocumentDate    civil.Date
	WithholdingDate civil.Date
}

type nillable struct {
	Nil   string `xml:"xsi:nil,attr,omitempty"`
	Value string `xml:",chardata"`
}

func optional(v string) nillable {
	if strings.TrimSpace(v) == "" {
		return nillable{Nil: "true"}
	}
	return nillable{Value: v}
}

type bpuBulk struct {
	XMLName xml.Name `xml:"BpuBulk"`
	XSI     string   `xml:"xmlns:xsi,attr"`
	TIN     string   `xml:"TIN"`
	Slips   []bpu    `xml:"ListOfBpu>Bpu"`
}

type bpu struct {
	TaxPeriodMonth   int      `xml:"TaxPeriodMonth"`
	TaxPeriodYear    int      `xml:"TaxPeriodYear"`
	CounterpartTin   string   `xml:"CounterpartTin"`
	RecipientTKU     string   `xml:"IDPlaceOfBusinessActivityOfIncomeRecipient"`
	TaxCertificate   string   `xml:"TaxCertificate"`
	TaxObjectCode    string   `xml:"TaxObjectCode"`
	TaxBase          string   `xml:"TaxBase"`
	Rate             string   `xml:"Rate"`
	Document         string   `xml:"Document"`
	DocumentNumber   string   `xml:"DocumentNumber"`
	DocumentDate     string   `xml:"DocumentDate"`
	WithholderTKU    string   `xml:"IDPlaceOfBusinessActivity"`
	GovTreasurerOpt  string   `xml:"GovTreasurerOpt"`
	SP2DNumber       nillable `xml:"SP2DNumber"`
	WithholdingDate  string   `xml:"WithholdingDate"`
}

type bpnrBulk struct {
	XMLName xml.Name `xml:"BPNRBulk"`
	XSI     string   `xml:"xmlns:xsi,attr"`
	TIN     string   `xml:"TIN"`
	Slips   []bpnr   `xml:"ListOfBPNR>BPNR"`
}

type bpnr struct {
	TaxPeriodMonth           int      `xml:"TaxPeriodMonth"`
	TaxPeriodYear            int      `xml:"TaxPeriodYear"`
	CounterpartTin           string   `xml:"CounterpartTin"`
	CounterpartReceiptNumber nillable `xml:"CounterpartReceiptNumber"`
	CounterpartName          string   `xml:"CounterpartName"`
	CounterpartCountry       string   `xml:"CounterpartCountry"`
	CounterpartAddress       string   `xml:"CounterpartAddress"`
	CounterpartDob           nillable `xml:"CounterpartDob"`
	CounterpartBirthCity     nillable `xml:"CounterpartBirthCity"`
	CounterpartPassport      nillable `xml:"CounterpartPassport"`
	CounterpartKitas         nillable `xml:"CounterpartKitas"`
	TaxCertificate           string   `xml:"TaxCertificate"`
	TaxObjectCode            string   `xml:"TaxObjectCode"`
	GrossIncome              string   `xml:"GrossIncome"`
	Deemed                   string   `xml:"Deemed"`
	Rate                     string   `xml:"Rate"`
	Document                 string   `xml:"Document"`
	DocumentNumber           string   `xml:"DocumentNumber"`
	DocumentDate             string   `xml:"DocumentDate"`
	GovTreasurerOpt          string   `xml:"GovTreasurerOpt"`
	SP2DNumber               nillable `xml:"SP2DNumber"`
	WithholdingDate          string   `xml:"WithholdingDate"`
	WithholderTKU            string   `xml:"IDPlaceOfBusinessActivity"`
}

// rateText menulis tarif basis poin sebagai persen desimal bertitik, mis. 175 → "1.75".
func rateText(bp int) string {
	return strconv.FormatFloat(float64(bp)/100, 'f', -1, 64)
}

// wholeRupiah menulis nominal sebagai rupiah bulat tanpa desimal.
func wholeRupiah(a money.Amount) string {
	s := a.MulFrac(1, 1, 0, money.HalfUp).String()
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	return s
}

func tku(tin, nitku string) string {
	if nitku != "" {
		return nitku
	}
	return tin + "000000"
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xmlHeader)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "\t")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// BuildBPPU menyusun XML impor bukti potong unifikasi (PPh 23 dan 4(2)).
func BuildBPPU(w XMLWithholder, slips []XMLSlip) ([]byte, error) {
	doc := bpuBulk{XSI: "http://www.w3.org/2001/XMLSchema-instance", TIN: w.NPWP}
	for _, s := range slips {
		doc.Slips = append(doc.Slips, bpu{
			TaxPeriodMonth: int(s.Period.Month()), TaxPeriodYear: s.Period.Year(),
			CounterpartTin: s.CounterpartTIN, RecipientTKU: tku(s.CounterpartTIN, s.CounterpartTKU),
			TaxCertificate: s.Facility, TaxObjectCode: s.TaxObjectCode, TaxBase: wholeRupiah(s.TaxBase), Rate: rateText(s.RateBP),
			Document: s.Document, DocumentNumber: s.DocumentNumber, DocumentDate: s.DocumentDate.String(),
			WithholderTKU: tku(w.NPWP, w.NITKU), GovTreasurerOpt: "N/A", SP2DNumber: optional(""),
			WithholdingDate: s.WithholdingDate.String(),
		})
	}
	return marshal(doc)
}

// BuildBPNR menyusun XML impor bukti potong non residen (PPh 26).
func BuildBPNR(w XMLWithholder, slips []XMLSlip) ([]byte, error) {
	doc := bpnrBulk{XSI: "http://www.w3.org/2001/XMLSchema-instance", TIN: w.NPWP}
	for _, s := range slips {
		doc.Slips = append(doc.Slips, bpnr{
			TaxPeriodMonth: int(s.Period.Month()), TaxPeriodYear: s.Period.Year(),
			CounterpartTin: s.CounterpartTIN, CounterpartReceiptNumber: optional(s.FacilityNumber),
			CounterpartName: s.Name, CounterpartCountry: s.Country, CounterpartAddress: s.Address,
			CounterpartDob: optional(""), CounterpartBirthCity: optional(""), CounterpartPassport: optional(""), CounterpartKitas: optional(""),
			TaxCertificate: s.Facility, TaxObjectCode: s.TaxObjectCode, GrossIncome: wholeRupiah(s.TaxBase),
			Deemed: strconv.FormatFloat(s.Deemed, 'f', -1, 64), Rate: rateText(s.RateBP),
			Document: s.Document, DocumentNumber: s.DocumentNumber, DocumentDate: s.DocumentDate.String(),
			GovTreasurerOpt: "N/A", SP2DNumber: optional(""), WithholdingDate: s.WithholdingDate.String(),
			WithholderTKU: tku(w.NPWP, w.NITKU),
		})
	}
	return marshal(doc)
}
