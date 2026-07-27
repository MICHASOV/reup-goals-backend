package profile

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf16"
)

func BuildInvoicePDF(invoice Invoice, seller SellerProfile, buyer BillingOrganization) []byte {
	lines := []string{
		"REUP.goals",
		"Счёт на оплату № " + invoice.Number,
		"Поставщик: " + seller.FullName,
		"ИНН / КПП: " + seller.INN + " / " + seller.KPP,
		"ОГРН: " + seller.RegistrationNumber,
		"Юридический адрес: " + seller.LegalAddress,
		"Банк: " + seller.BankName,
		"Расчётный счёт: " + seller.SettlementAccount,
		"Корр. счёт: " + seller.CorrespondentAccount,
		"БИК: " + seller.BIC,
		"Покупатель: " + buyer.FullName,
		"ИНН / КПП: " + buyer.INN + " / " + valueOrDash(buyer.KPP),
		"ОГРН / ОГРНИП: " + buyer.RegistrationNumber,
		"Адрес покупателя: " + buyer.LegalAddress,
		"Услуга: " + invoice.Description,
		fmt.Sprintf("Сумма: %.2f %s", invoice.Amount, invoice.Currency),
		"Налог: " + valueOrDash(invoice.TaxLabel),
		"Дата выставления: " + invoice.IssuedAt.Format("02.01.2006"),
		"Оплатить до: " + invoice.DueAt.Format("02.01.2006"),
		"Назначение платежа: оплата по счёту " + invoice.Number,
	}

	var content strings.Builder
	content.WriteString("BT\n/F1 18 Tf\n50 790 Td\n")
	for index, line := range lines {
		if index == 1 {
			content.WriteString("/F1 14 Tf\n")
		} else if index == 2 {
			content.WriteString("/F1 10 Tf\n")
		}
		content.WriteString("<")
		content.WriteString(pdfUTF16Hex(line))
		content.WriteString("> Tj\n0 -22 Td\n")
	}
	content.WriteString("ET\n")

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type0 /BaseFont /Arial /Encoding /Identity-H /DescendantFonts [6 0 R] >>",
		"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Arial /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /CIDToGIDMap /Identity >>",
	}

	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n", len(objects)+1)
	output.WriteString("0000000000 65535 f \n")
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return output.Bytes()
}

func pdfUTF16Hex(value string) string {
	encoded := utf16.Encode([]rune(value))
	var result strings.Builder
	result.WriteString("FEFF")
	for _, code := range encoded {
		fmt.Fprintf(&result, "%04X", code)
	}
	return result.String()
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
