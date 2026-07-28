package profile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pdf/fpdf"
)

var invoiceFontCandidates = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/dejavu/DejaVuSans.ttf",
	"/System/Library/Fonts/Supplemental/Arial.ttf",
	"/Library/Fonts/Arial Unicode.ttf",
}

func BuildInvoicePDF(invoice Invoice, seller SellerProfile, buyer BillingOrganization) ([]byte, error) {
	fontPath, err := invoiceFontPath()
	if err != nil {
		return nil, err
	}
	fontData, err := os.ReadFile(fontPath)
	if err != nil {
		return nil, fmt.Errorf("read invoice font: %w", err)
	}

	document := fpdf.New("P", "mm", "A4", "")
	document.SetCompression(true)
	document.SetMargins(18, 16, 18)
	document.SetAutoPageBreak(true, 18)
	document.AddUTF8FontFromBytes("invoice", "", fontData)
	if document.Error() != nil {
		return nil, fmt.Errorf("load invoice font: %w", document.Error())
	}
	document.SetTitle("Счёт "+invoice.Number, true)
	document.SetAuthor("REUP.goals", true)
	document.AddPage()

	document.SetFont("invoice", "", 17)
	document.SetTextColor(13, 27, 43)
	document.CellFormat(0, 9, "REUP.goals", "", 1, "L", false, 0, "")
	document.SetFont("invoice", "", 14)
	document.MultiCell(0, 8, "Счёт на оплату № "+invoice.Number, "", "L", false)
	document.Ln(3)

	document.SetDrawColor(220, 225, 230)
	document.Line(18, document.GetY(), 192, document.GetY())
	document.Ln(5)

	writeInvoiceSection(document, "Поставщик", [][2]string{
		{"Наименование", seller.FullName},
		{"ИНН / КПП", seller.INN + " / " + valueOrDash(seller.KPP)},
		{"ОГРН", seller.RegistrationNumber},
		{"Юридический адрес", seller.LegalAddress},
		{"Банк", seller.BankName},
		{"Расчётный счёт", seller.SettlementAccount},
		{"Корреспондентский счёт", seller.CorrespondentAccount},
		{"БИК", seller.BIC},
	})
	writeInvoiceSection(document, "Покупатель", [][2]string{
		{"Наименование", buyer.FullName},
		{"ИНН / КПП", buyer.INN + " / " + valueOrDash(buyer.KPP)},
		{"ОГРН / ОГРНИП", buyer.RegistrationNumber},
		{"Юридический адрес", buyer.LegalAddress},
	})
	writeInvoiceSection(document, "Основание платежа", [][2]string{
		{"Услуга", invoice.Description},
		{"Сумма", fmt.Sprintf("%.2f %s", invoice.Amount, invoice.Currency)},
		{"Налог", valueOrDash(invoice.TaxLabel)},
		{"Дата выставления", invoice.IssuedDate},
		{"Оплатить до", invoice.DueDate},
		{"Назначение платежа", "Оплата по счёту " + invoice.Number + ". " + valueOrDash(invoice.TaxLabel)},
	})

	document.Ln(3)
	document.SetFont("invoice", "", 8.5)
	document.SetTextColor(86, 96, 106)
	document.MultiCell(
		0,
		4.5,
		"Счёт сформирован автоматически в REUP.goals. При оплате укажите номер счёта в назначении платежа.",
		"",
		"L",
		false,
	)

	var output bytes.Buffer
	if err := document.Output(&output); err != nil {
		return nil, fmt.Errorf("render invoice PDF: %w", err)
	}
	return output.Bytes(), nil
}

func writeInvoiceSection(document *fpdf.Fpdf, title string, rows [][2]string) {
	document.SetFont("invoice", "", 11)
	document.SetTextColor(13, 27, 43)
	document.CellFormat(0, 7, title, "", 1, "L", false, 0, "")
	document.SetFont("invoice", "", 9)
	for _, row := range rows {
		label := strings.TrimSpace(row[0])
		value := valueOrDash(strings.TrimSpace(row[1]))
		document.SetTextColor(100, 109, 119)
		document.MultiCell(0, 4.5, label, "", "L", false)
		document.SetTextColor(13, 27, 43)
		document.MultiCell(0, 5, value, "", "L", false)
		document.Ln(1)
	}
	document.Ln(2)
}

func invoiceFontPath() (string, error) {
	candidates := invoiceFontCandidates
	if configured := strings.TrimSpace(os.Getenv("INVOICE_FONT_PATH")); configured != "" {
		candidates = append([]string{configured}, candidates...)
	}
	for _, candidate := range candidates {
		path := filepath.Clean(candidate)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("invoice_font_not_found")
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
