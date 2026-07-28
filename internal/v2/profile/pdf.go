package profile

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/go-pdf/fpdf"
)

//go:embed assets/reup-wordmark.png
var invoiceLogo []byte

var invoiceFontCandidates = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/dejavu/DejaVuSans.ttf",
	"/System/Library/Fonts/Supplemental/Arial.ttf",
	"/Library/Fonts/Arial Unicode.ttf",
}

var invoiceBoldFontCandidates = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
	"/usr/share/fonts/dejavu/DejaVuSans-Bold.ttf",
	"/System/Library/Fonts/Supplemental/Arial Bold.ttf",
}

var invoiceFontRoots = []string{
	"/usr/share/fonts",
	"/System/Library/Fonts",
	"/Library/Fonts",
}

const (
	invoicePageLeft  = 12.0
	invoicePageRight = 198.0
	invoicePageWidth = invoicePageRight - invoicePageLeft
)

func BuildInvoicePDF(invoice Invoice, seller SellerProfile, buyer BillingOrganization) ([]byte, error) {
	fontPath, err := invoiceFontPath()
	if err != nil {
		return nil, err
	}
	fontData, err := readInvoiceFont(fontPath)
	if err != nil {
		return nil, err
	}
	boldFontData := fontData
	if boldPath, boldErr := invoiceFontPathFromCandidates(invoiceBoldFontCandidates); boldErr == nil {
		if data, readErr := readInvoiceFont(boldPath); readErr == nil {
			boldFontData = data
		}
	}

	document := fpdf.New("P", "mm", "A4", "")
	document.SetCompression(true)
	document.SetMargins(invoicePageLeft, 12, 12)
	document.SetAutoPageBreak(true, 16)
	document.AddUTF8FontFromBytes("invoice", "", fontData)
	document.AddUTF8FontFromBytes("invoice", "B", boldFontData)
	document.RegisterImageOptionsReader(
		"reup-wordmark",
		fpdf.ImageOptions{ImageType: "PNG", ReadDpi: false},
		bytes.NewReader(invoiceLogo),
	)
	if document.Error() != nil {
		return nil, fmt.Errorf("load invoice assets: %w", document.Error())
	}

	document.SetTitle("Счёт "+invoice.Number, true)
	document.SetAuthor("REUP.goals", true)
	document.AddPage()
	writeInvoiceHeader(document, seller)
	writeBankDetails(document, seller)

	document.SetXY(invoicePageLeft, 84)
	document.SetFont("invoice", "B", 15)
	document.SetTextColor(13, 27, 43)
	document.MultiCell(
		invoicePageWidth,
		7,
		fmt.Sprintf("Счёт на оплату № %s от %s", invoice.Number, invoiceLongDate(invoice)),
		"",
		"C",
		false,
	)
	document.SetDrawColor(33, 126, 236)
	document.SetLineWidth(0.8)
	document.Line(invoicePageLeft, document.GetY()+1.5, invoicePageRight, document.GetY()+1.5)
	document.Ln(6)

	supplierDetails := legalPartyDetails(
		seller.FullName,
		seller.INN,
		seller.KPP,
		seller.RegistrationNumber,
		seller.LegalAddress,
	)
	buyerDetails := legalPartyDetails(
		buyer.FullName,
		buyer.INN,
		buyer.KPP,
		buyer.RegistrationNumber,
		buyer.LegalAddress,
	)
	writeInvoiceParty(document, "Поставщик:", supplierDetails)
	writeInvoiceParty(document, "Покупатель:", buyerDetails)
	document.Ln(5)

	itemBottom := writeInvoiceItemTable(document, invoice)
	document.SetY(itemBottom + 5)
	writeInvoiceTotals(document, invoice)

	document.Ln(3)
	document.SetFont("invoice", "", 9.5)
	document.SetTextColor(13, 27, 43)
	document.MultiCell(
		invoicePageWidth,
		5,
		"Всего к оплате: "+russianMoneyWords(invoice.Amount, invoice.Currency),
		"",
		"L",
		false,
	)

	document.SetDrawColor(13, 27, 43)
	document.SetLineWidth(0.5)
	document.Line(invoicePageLeft, document.GetY()+2, invoicePageRight, document.GetY()+2)
	document.Ln(6)

	document.SetFont("invoice", "", 8.5)
	document.MultiCell(
		invoicePageWidth,
		4.5,
		fmt.Sprintf(
			"Назначение платежа: оплата по счёту № %s. %s. Счёт действителен до %s.",
			invoice.Number,
			valueOrDash(invoice.TaxLabel),
			invoice.DueDate,
		),
		"",
		"L",
		false,
	)

	writeInvoiceSignatureArea(document, seller)

	var output bytes.Buffer
	if err := document.Output(&output); err != nil {
		return nil, fmt.Errorf("render invoice PDF: %w", err)
	}
	return output.Bytes(), nil
}

func writeInvoiceHeader(document *fpdf.Fpdf, seller SellerProfile) {
	document.ImageOptions(
		"reup-wordmark",
		invoicePageLeft,
		12,
		57,
		0,
		false,
		fpdf.ImageOptions{ImageType: "PNG", ReadDpi: false},
		0,
		"",
	)

	x := 82.0
	width := invoicePageRight - x
	document.SetXY(x, 12)
	document.SetTextColor(13, 27, 43)
	document.SetFont("invoice", "B", 8.5)
	document.MultiCell(width, 4.4, valueOrDash(seller.FullName), "", "L", false)
	document.SetX(x)
	document.SetFont("invoice", "", 7.2)
	document.SetTextColor(77, 88, 100)
	details := []string{
		fmt.Sprintf(
			"ИНН %s  ·  КПП %s  ·  ОГРН %s",
			valueOrDash(seller.INN),
			valueOrDash(seller.KPP),
			valueOrDash(seller.RegistrationNumber),
		),
	}
	if address := strings.TrimSpace(seller.LegalAddress); address != "" {
		details = append(details, address)
	}
	if email := strings.TrimSpace(seller.AccountingEmail); email != "" {
		details = append(details, email)
	}
	document.MultiCell(
		width,
		3.7,
		strings.Join(details, "\n"),
		"",
		"L",
		false,
	)
}

func writeBankDetails(document *fpdf.Fpdf, seller SellerProfile) {
	const (
		x          = invoicePageLeft
		y          = 39.0
		leftWidth  = 111.0
		labelWidth = 16.0
		valueWidth = invoicePageWidth - leftWidth - labelWidth
		topHeight  = 18.0
		rowHeight  = 9.0
		bottomY    = y + topHeight
		bottomH    = 21.0
	)

	document.SetLineWidth(0.25)
	document.SetDrawColor(90, 99, 109)
	document.SetTextColor(13, 27, 43)
	document.SetFont("invoice", "", 8.2)
	drawInvoiceCell(document, x, y, leftWidth, topHeight, valueOrDash(seller.BankName), "L", false)
	drawInvoiceCell(document, x+leftWidth, y, labelWidth, rowHeight, "БИК", "L", true)
	drawInvoiceCell(document, x+leftWidth+labelWidth, y, valueWidth, rowHeight, valueOrDash(seller.BIC), "L", false)
	drawInvoiceCell(document, x+leftWidth, y+rowHeight, labelWidth, rowHeight, "Сч. №", "L", true)
	drawInvoiceCell(
		document,
		x+leftWidth+labelWidth,
		y+rowHeight,
		valueWidth,
		rowHeight,
		valueOrDash(seller.CorrespondentAccount),
		"L",
		false,
	)
	document.SetXY(x+2, y+12.2)
	document.SetFont("invoice", "", 6.7)
	document.SetTextColor(98, 108, 119)
	document.CellFormat(leftWidth-4, 4, "Банк получателя", "", 0, "L", false, 0, "")

	const (
		innLabelWidth = 14.0
		innValueWidth = 40.0
		kppLabelWidth = 14.0
		kppValueWidth = leftWidth - innLabelWidth - innValueWidth - kppLabelWidth
	)
	drawInvoiceCell(document, x, bottomY, innLabelWidth, rowHeight, "ИНН", "L", true)
	drawInvoiceCell(document, x+innLabelWidth, bottomY, innValueWidth, rowHeight, valueOrDash(seller.INN), "L", false)
	drawInvoiceCell(document, x+innLabelWidth+innValueWidth, bottomY, kppLabelWidth, rowHeight, "КПП", "L", true)
	drawInvoiceCell(
		document,
		x+innLabelWidth+innValueWidth+kppLabelWidth,
		bottomY,
		kppValueWidth,
		rowHeight,
		valueOrDash(seller.KPP),
		"L",
		false,
	)
	drawInvoiceCell(document, x, bottomY+rowHeight, leftWidth, bottomH-rowHeight, valueOrDash(seller.FullName), "L", false)
	drawInvoiceCell(document, x+leftWidth, bottomY, labelWidth, bottomH, "Сч. №", "L", true)
	drawInvoiceCell(
		document,
		x+leftWidth+labelWidth,
		bottomY,
		valueWidth,
		bottomH,
		valueOrDash(seller.SettlementAccount),
		"L",
		false,
	)
	document.SetXY(x+2, bottomY+bottomH-4.4)
	document.SetFont("invoice", "", 6.7)
	document.SetTextColor(98, 108, 119)
	document.CellFormat(leftWidth-4, 4, "Получатель", "", 0, "L", false, 0, "")
}

func drawInvoiceCell(
	document *fpdf.Fpdf,
	x, y, width, height float64,
	text, align string,
	highlight bool,
) {
	if highlight {
		document.SetFillColor(243, 246, 250)
		document.Rect(x, y, width, height, "DF")
	} else {
		document.Rect(x, y, width, height, "D")
	}
	document.SetFont("invoice", "", 8)
	document.SetTextColor(13, 27, 43)
	lines := document.SplitText(text, width-4)
	if len(lines) == 0 {
		lines = []string{"—"}
	}
	lineHeight := 3.9
	textHeight := float64(len(lines)) * lineHeight
	textY := y + math.Max(1.4, (height-textHeight)/2)
	for _, line := range lines {
		document.SetXY(x+2, textY)
		document.CellFormat(width-4, lineHeight, line, "", 0, align, false, 0, "")
		textY += lineHeight
	}
}

func writeInvoiceParty(document *fpdf.Fpdf, label, details string) {
	const labelWidth = 24.0
	document.SetFont("invoice", "B", 8.7)
	document.SetTextColor(13, 27, 43)
	startY := document.GetY()
	document.SetXY(invoicePageLeft, startY)
	document.CellFormat(labelWidth, 4.6, label, "", 0, "L", false, 0, "")

	document.SetFont("invoice", "", 8.7)
	lines := document.SplitText(details, invoicePageWidth-labelWidth)
	if len(lines) == 0 {
		lines = []string{"—"}
	}
	document.SetXY(invoicePageLeft+labelWidth, startY)
	document.MultiCell(invoicePageWidth-labelWidth, 4.6, strings.Join(lines, "\n"), "", "L", false)
	document.SetY(math.Max(document.GetY(), startY+4.6))
	document.Ln(1)
}

func legalPartyDetails(fullName, inn, kpp, registrationNumber, address string) string {
	parts := []string{valueOrDash(fullName)}
	if strings.TrimSpace(inn) != "" {
		parts = append(parts, "ИНН "+strings.TrimSpace(inn))
	}
	if strings.TrimSpace(kpp) != "" {
		parts = append(parts, "КПП "+strings.TrimSpace(kpp))
	}
	if strings.TrimSpace(registrationNumber) != "" {
		parts = append(parts, "ОГРН / ОГРНИП "+strings.TrimSpace(registrationNumber))
	}
	if strings.TrimSpace(address) != "" {
		parts = append(parts, strings.TrimSpace(address))
	}
	return strings.Join(parts, ", ")
}

func writeInvoiceItemTable(document *fpdf.Fpdf, invoice Invoice) float64 {
	const (
		numberWidth      = 8.0
		descriptionWidth = 103.0
		quantityWidth    = 17.0
		unitWidth        = 14.0
		priceWidth       = 22.0
		amountWidth      = 22.0
		headerHeight     = 8.0
	)
	x := invoicePageLeft
	y := document.GetY()
	headers := []struct {
		width float64
		text  string
	}{
		{numberWidth, "№"},
		{descriptionWidth, "Товары (работы, услуги)"},
		{quantityWidth, "Кол-во"},
		{unitWidth, "Ед."},
		{priceWidth, "Цена"},
		{amountWidth, "Сумма"},
	}

	document.SetFont("invoice", "B", 7.5)
	document.SetFillColor(243, 246, 250)
	document.SetDrawColor(90, 99, 109)
	document.SetLineWidth(0.25)
	for _, header := range headers {
		document.SetXY(x, y)
		document.CellFormat(header.width, headerHeight, header.text, "1", 0, "C", true, 0, "")
		x += header.width
	}

	document.SetFont("invoice", "", 8)
	description := valueOrDash(invoice.Description)
	descriptionLines := document.SplitText(description, descriptionWidth-4)
	bodyHeight := math.Max(13, float64(len(descriptionLines))*4.2+4)
	bodyY := y + headerHeight
	x = invoicePageLeft
	values := []struct {
		width float64
		text  string
		align string
	}{
		{numberWidth, "1", "C"},
		{descriptionWidth, description, "L"},
		{quantityWidth, "1", "C"},
		{unitWidth, "шт.", "C"},
		{priceWidth, formatMoney(invoice.Amount), "R"},
		{amountWidth, formatMoney(invoice.Amount), "R"},
	}
	for _, value := range values {
		drawInvoiceCell(document, x, bodyY, value.width, bodyHeight, value.text, value.align, false)
		x += value.width
	}
	return bodyY + bodyHeight
}

func writeInvoiceTotals(document *fpdf.Fpdf, invoice Invoice) {
	const (
		labelWidth = 42.0
		valueWidth = 32.0
		rowHeight  = 5.2
	)
	x := invoicePageRight - labelWidth - valueWidth
	y := document.GetY()
	rows := [][2]string{
		{"Итого:", formatMoney(invoice.Amount) + " " + invoiceCurrencyLabel(invoice.Currency)},
		{"Налог:", valueOrDash(invoice.TaxLabel)},
		{"Всего к оплате:", formatMoney(invoice.Amount) + " " + invoiceCurrencyLabel(invoice.Currency)},
	}
	for index, row := range rows {
		style := ""
		if index == len(rows)-1 {
			style = "B"
		}
		document.SetFont("invoice", style, 8.5)
		document.SetTextColor(13, 27, 43)
		document.SetXY(x, y)
		document.CellFormat(labelWidth, rowHeight, row[0], "", 0, "R", false, 0, "")
		document.SetXY(x+labelWidth, y)
		document.CellFormat(valueWidth, rowHeight, row[1], "", 0, "R", false, 0, "")
		y += rowHeight
	}
	document.SetY(y)
}

func writeInvoiceSignatureArea(document *fpdf.Fpdf, seller SellerProfile) {
	y := math.Max(document.GetY()+12, 214)
	if y > 260 {
		document.AddPage()
		y = 28
	}

	document.SetFont("invoice", "", 8.5)
	document.SetTextColor(13, 27, 43)
	document.SetXY(invoicePageLeft, y)
	document.CellFormat(27, 5, "Руководитель", "", 0, "L", false, 0, "")

	signatureStart := invoicePageLeft + 30
	signatureEnd := signatureStart + 54
	nameStart := signatureEnd + 6
	document.SetDrawColor(90, 99, 109)
	document.SetLineWidth(0.25)
	document.Line(signatureStart, y+4.2, signatureEnd, y+4.2)
	document.Line(nameStart, y+4.2, invoicePageRight, y+4.2)

	document.SetFont("invoice", "", 6.5)
	document.SetTextColor(98, 108, 119)
	document.SetXY(signatureStart, y+4.5)
	document.CellFormat(signatureEnd-signatureStart, 4, "подпись", "", 0, "C", false, 0, "")
	document.SetXY(nameStart, y+4.5)
	document.CellFormat(invoicePageRight-nameStart, 4, "расшифровка подписи", "", 0, "C", false, 0, "")

	document.SetFont("invoice", "", 8)
	document.SetTextColor(13, 27, 43)
	document.SetXY(nameStart, y-0.5)
	document.CellFormat(
		invoicePageRight-nameStart,
		4.5,
		strings.TrimSpace(seller.DirectorName),
		"",
		0,
		"C",
		false,
		0,
		"",
	)
	document.SetXY(invoicePageLeft, y+18)
	document.CellFormat(12, 5, "М.П.", "", 0, "L", false, 0, "")
}

func readInvoiceFont(path string) ([]byte, error) {
	// #nosec G304 -- invoiceFontPath restricts paths to trusted system font directories.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read invoice font: %w", err)
	}
	return data, nil
}

func invoiceFontPath() (string, error) {
	candidates := invoiceFontCandidates
	if configured := strings.TrimSpace(os.Getenv("INVOICE_FONT_PATH")); configured != "" {
		candidates = append([]string{configured}, candidates...)
	}
	return invoiceFontPathFromCandidates(candidates)
}

func invoiceFontPathFromCandidates(candidates []string) (string, error) {
	for _, candidate := range candidates {
		path, allowed := allowedInvoiceFontPath(candidate)
		if !allowed {
			continue
		}
		// #nosec G703 -- allowedInvoiceFontPath confines the resolved path to trusted system font roots.
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", errors.New("invoice_font_not_found")
}

func allowedInvoiceFontPath(candidate string) (string, bool) {
	cleaned, err := filepath.Abs(filepath.Clean(strings.TrimSpace(candidate)))
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", false
	}
	for _, root := range invoiceFontRoots {
		allowedRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr != nil {
			continue
		}
		relative, relativeErr := filepath.Rel(allowedRoot, resolved)
		if relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return resolved, true
		}
	}
	return "", false
}

func invoiceLongDate(invoice Invoice) string {
	date := invoice.IssuedAt
	if parsed, err := time.Parse("02.01.2006", strings.TrimSpace(invoice.IssuedDate)); err == nil {
		date = parsed
	}
	if date.IsZero() {
		return valueOrDash(invoice.IssuedDate)
	}
	months := [...]string{
		"",
		"января",
		"февраля",
		"марта",
		"апреля",
		"мая",
		"июня",
		"июля",
		"августа",
		"сентября",
		"октября",
		"ноября",
		"декабря",
	}
	return fmt.Sprintf("%d %s %d г.", date.Day(), months[date.Month()], date.Year())
}

func formatMoney(amount float64) string {
	value := fmt.Sprintf("%.2f", math.Abs(amount))
	parts := strings.SplitN(value, ".", 2)
	integer := parts[0]
	for index := len(integer) - 3; index > 0; index -= 3 {
		integer = integer[:index] + " " + integer[index:]
	}
	if amount < 0 {
		integer = "-" + integer
	}
	return integer + "," + parts[1]
}

func invoiceCurrencyLabel(currency string) string {
	if strings.EqualFold(strings.TrimSpace(currency), "RUB") {
		return "руб."
	}
	return valueOrDash(strings.ToUpper(strings.TrimSpace(currency)))
}

func russianMoneyWords(amount float64, currency string) string {
	if !strings.EqualFold(strings.TrimSpace(currency), "RUB") {
		return formatMoney(amount) + " " + invoiceCurrencyLabel(currency)
	}
	kopecksTotal := int64(math.Round(math.Abs(amount) * 100))
	rubles := kopecksTotal / 100
	kopecks := kopecksTotal % 100
	words := russianIntegerWords(rubles)
	if amount < 0 {
		words = "минус " + words
	}
	result := fmt.Sprintf(
		"%s %s %02d %s.",
		words,
		russianForm(rubles, [3]string{"рубль", "рубля", "рублей"}),
		kopecks,
		russianForm(kopecks, [3]string{"копейка", "копейки", "копеек"}),
	)
	runes := []rune(result)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return string(runes)
}

func russianIntegerWords(value int64) string {
	if value == 0 {
		return "ноль"
	}
	type group struct {
		divisor int64
		female  bool
		forms   [3]string
	}
	groups := []group{
		{1_000_000_000, false, [3]string{"миллиард", "миллиарда", "миллиардов"}},
		{1_000_000, false, [3]string{"миллион", "миллиона", "миллионов"}},
		{1_000, true, [3]string{"тысяча", "тысячи", "тысяч"}},
	}
	parts := make([]string, 0, 12)
	remainder := value
	for _, current := range groups {
		count := remainder / current.divisor
		if count == 0 {
			continue
		}
		parts = append(parts, russianTriadWords(int(count), current.female)...)
		parts = append(parts, russianForm(count, current.forms))
		remainder %= current.divisor
	}
	if remainder > 0 {
		parts = append(parts, russianTriadWords(int(remainder), false)...)
	}
	return strings.Join(parts, " ")
}

func russianTriadWords(value int, female bool) []string {
	hundreds := [...]string{"", "сто", "двести", "триста", "четыреста", "пятьсот", "шестьсот", "семьсот", "восемьсот", "девятьсот"}
	tens := [...]string{"", "", "двадцать", "тридцать", "сорок", "пятьдесят", "шестьдесят", "семьдесят", "восемьдесят", "девяносто"}
	teens := [...]string{"десять", "одиннадцать", "двенадцать", "тринадцать", "четырнадцать", "пятнадцать", "шестнадцать", "семнадцать", "восемнадцать", "девятнадцать"}
	ones := [...]string{"", "один", "два", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"}
	femaleOnes := [...]string{"", "одна", "две", "три", "четыре", "пять", "шесть", "семь", "восемь", "девять"}

	parts := make([]string, 0, 3)
	if value/100 > 0 {
		parts = append(parts, hundreds[value/100])
	}
	remainder := value % 100
	if remainder >= 10 && remainder <= 19 {
		return append(parts, teens[remainder-10])
	}
	if remainder/10 > 0 {
		parts = append(parts, tens[remainder/10])
	}
	unit := remainder % 10
	if unit > 0 {
		if female {
			parts = append(parts, femaleOnes[unit])
		} else {
			parts = append(parts, ones[unit])
		}
	}
	return parts
}

func russianForm(value int64, forms [3]string) string {
	lastTwo := value % 100
	if lastTwo >= 11 && lastTwo <= 14 {
		return forms[2]
	}
	switch value % 10 {
	case 1:
		return forms[0]
	case 2, 3, 4:
		return forms[1]
	default:
		return forms[2]
	}
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}
