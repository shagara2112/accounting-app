package coretax

import (
	"bytes"
	"encoding/xml"
	"strings"

	"lajur/internal/platform/civil"
	"lajur/internal/platform/money"
)

// FormatV1 adalah versi eksportir XML faktur keluaran. Struktur mengikuti template impor
// massal Coretax (TaxInvoiceBulk). Bila DJP merilis template baru, buat versi baru di samping
// yang ini dan uji keduanya dengan file contoh resmi (TDD, bagian Pajak dan ekspor Coretax).
const FormatV1 = "coretax-faktur-keluaran/v1"

// XMLSeller adalah identitas PT penjual.
type XMLSeller struct {
	NPWP  string
	NITKU string // kosong = NPWP + 000000
}

// XMLBuyer adalah identitas pembeli. NIK berfungsi sebagai NPWP orang pribadi (PMK 112/2022).
type XMLBuyer struct {
	Name    string
	NPWP    string // atau NIK 16 digit
	NITKU   string
	Address string
	Email   string
}

type XMLLine struct {
	Opt      string // A barang, B jasa
	Code     string // kode barang/jasa 6 digit
	Name     string
	Unit     string // kode satuan, mis. UM.0021
	Price    money.Amount
	Qty      money.Amount
	Discount money.Amount
	TaxBase  money.Amount // DPP
	OtherDPP money.Amount // DPP nilai lain
	RateBP   int
	VAT      money.Amount
}

type XMLInvoice struct {
	Date      civil.Date
	TrxCode   string
	Reference string // nomor faktur Lajur, dipakai mencocokkan nomor faktur pajak yang terbit
	Buyer     XMLBuyer
	Lines     []XMLLine
}

type taxInvoiceBulk struct {
	XMLName  xml.Name     `xml:"TaxInvoiceBulk"`
	XSI      string       `xml:"xmlns:xsi,attr"`
	Schema   string       `xml:"xsi:noNamespaceSchemaLocation,attr"`
	TIN      string       `xml:"TIN"`
	Invoices []taxInvoice `xml:"ListOfTaxInvoice>TaxInvoice"`
}

// Urutan field mengikuti template Coretax; ejaan BuyerAdress memang begitu di template DJP.
type taxInvoice struct {
	TaxInvoiceDate      string        `xml:"TaxInvoiceDate"`
	TaxInvoiceOpt       string        `xml:"TaxInvoiceOpt"`
	TrxCode             string        `xml:"TrxCode"`
	AddInfo             string        `xml:"AddInfo"`
	CustomDoc           string        `xml:"CustomDoc"`
	RefDesc             string        `xml:"RefDesc"`
	FacilityStamp       string        `xml:"FacilityStamp"`
	SellerIDTKU         string        `xml:"SellerIDTKU"`
	BuyerTin            string        `xml:"BuyerTin"`
	BuyerDocument       string        `xml:"BuyerDocument"`
	BuyerCountry        string        `xml:"BuyerCountry"`
	BuyerDocumentNumber string        `xml:"BuyerDocumentNumber"`
	BuyerName           string        `xml:"BuyerName"`
	BuyerAdress         string        `xml:"BuyerAdress"`
	BuyerEmail          string        `xml:"BuyerEmail"`
	BuyerIDTKU          string        `xml:"BuyerIDTKU"`
	Goods               []goodService `xml:"ListOfGoodService>GoodService"`
}

type goodService struct {
	Opt           string `xml:"Opt"`
	Code          string `xml:"Code"`
	Name          string `xml:"Name"`
	Unit          string `xml:"Unit"`
	Price         string `xml:"Price"`
	Qty           string `xml:"Qty"`
	TotalDiscount string `xml:"TotalDiscount"`
	TaxBase       string `xml:"TaxBase"`
	OtherTaxBase  string `xml:"OtherTaxBase"`
	VATRate       string `xml:"VATRate"`
	VAT           string `xml:"VAT"`
	STLGRate      string `xml:"STLGRate"`
	STLG          string `xml:"STLG"`
}

// idtku: NITKU bila ada, selain itu NPWP + 000000 (kantor pusat).
func idtku(npwp, nitku string) string {
	if nitku != "" {
		return nitku
	}
	return npwp + "000000"
}

// decimal2 menulis nominal dengan 2 desimal tanpa pemisah ribuan, mis. "1250000.00".
func decimal2(a money.Amount) string {
	s := a.MulFrac(1, 1, 2, money.HalfUp).String()
	return s[:len(s)-2]
}

// plain menulis angka tanpa nol di belakang, mis. "2" atau "2.5".
func plain(a money.Amount) string {
	s := strings.TrimRight(a.String(), "0")
	return strings.TrimSuffix(s, ".")
}

// rate menulis tarif dari basis poin, mis. 1200 → "12", 1175 → "11.75".
func rate(bp int) string { return plain(money.FromInt(int64(bp)).MulFrac(1, 100, 2, money.HalfUp)) }

// EncodeV1 menyusun file XML impor massal faktur keluaran Coretax.
func EncodeV1(seller XMLSeller, invoices []XMLInvoice) ([]byte, error) {
	doc := taxInvoiceBulk{
		XSI: "http://www.w3.org/2001/XMLSchema-instance", Schema: "TaxInvoice.xsd", TIN: seller.NPWP,
	}
	for _, inv := range invoices {
		ti := taxInvoice{
			TaxInvoiceDate: inv.Date.String(), TaxInvoiceOpt: "Normal", TrxCode: inv.TrxCode, RefDesc: inv.Reference,
			SellerIDTKU: idtku(seller.NPWP, seller.NITKU), BuyerTin: inv.Buyer.NPWP, BuyerDocument: "TIN",
			BuyerCountry: "IDN", BuyerName: inv.Buyer.Name, BuyerAdress: inv.Buyer.Address, BuyerEmail: inv.Buyer.Email,
			BuyerIDTKU: idtku(inv.Buyer.NPWP, inv.Buyer.NITKU),
		}
		for _, l := range inv.Lines {
			ti.Goods = append(ti.Goods, goodService{
				Opt: l.Opt, Code: l.Code, Name: l.Name, Unit: l.Unit, Price: decimal2(l.Price), Qty: plain(l.Qty),
				TotalDiscount: decimal2(l.Discount), TaxBase: decimal2(l.TaxBase), OtherTaxBase: decimal2(l.OtherDPP),
				VATRate: rate(l.RateBP), VAT: decimal2(l.VAT), STLGRate: "0", STLG: "0",
			})
		}
		doc.Invoices = append(doc.Invoices, ti)
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
