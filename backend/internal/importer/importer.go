// Package importer reads volunteer lists from real-world spreadsheets:
// .xlsx (Excel, LibreOffice, Google Sheets export) and .csv with ",", ";"
// or tab delimiters, in UTF-8 (with or without BOM) or Windows-1252, with
// quoted line breaks. It maps columns to HR fields (with suggestions from
// common English, Dutch, German and French headers) and flags typical Excel
// damage per row instead of guessing silently.
package importer

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
)

// Limits for one file.
const (
	MaxBytes = 10 << 20
	MaxRows  = 10000
)

// Fields HR can import.
var Fields = []string{
	"first_name", "last_name", "full_name", "nickname", "email", "org_email", "phone", "date_of_birth",
	"street", "postal_code", "city", "country", "emergency_name", "emergency_phone", "emergency_email",
	"department", "roles", "notes", "external_id",
}

var headerSynonyms = map[string][]string{
	"first_name":      {"firstname", "first", "givenname", "forename", "voornaam", "vorname", "prenom"},
	"last_name":       {"lastname", "surname", "familyname", "last", "achternaam", "nachname", "familienname", "nom", "nomdefamille"},
	"full_name":       {"name", "fullname", "naam", "volledigenaam", "vollstandigername", "nomcomplet"},
	"nickname":        {"nickname", "nick", "badgename", "fursona", "roepnaam", "bijnaam", "spitzname", "rufname", "surnom", "pseudo"},
	"email":           {"email", "emailaddress", "mail", "privateemail", "emailadres", "emailadresse", "courriel", "adresseemail"},
	"org_email":       {"orgemail", "workemail", "organizationemail", "workspaceemail"},
	"phone":           {"phone", "phonenumber", "telephone", "mobile", "cell", "cellphone", "telefoon", "telefoonnummer", "mobiel", "gsm", "telefon", "handy", "telefonnummer", "portable"},
	"date_of_birth":   {"dateofbirth", "birthday", "dob", "birthdate", "geboortedatum", "geburtsdatum", "geburtstag", "datedenaissance"},
	"street":          {"street", "address", "streetaddress", "straat", "adres", "strasse", "anschrift", "adresse", "rue"},
	"postal_code":     {"postalcode", "postcode", "zip", "zipcode", "plz", "postleitzahl", "codepostal"},
	"city":            {"city", "town", "plaats", "woonplaats", "stad", "ort", "wohnort", "stadt", "ville"},
	"country":         {"country", "land", "pays"},
	"emergency_name":  {"emergencycontact", "emergencyname", "emergencycontactname", "noodcontact", "notfallkontakt", "contactdurgence"},
	"emergency_phone": {"emergencyphone", "emergencynumber", "emergencycontactphone", "noodnummer", "notfallnummer", "telephonedurgence"},
	"emergency_email": {"emergencyemail", "emergencycontactemail"},
	"department":      {"department", "team", "dept", "afdeling", "abteilung", "departement", "equipe"},
	"roles":           {"roles", "role", "accessgroup", "accessgroups", "functie", "rolle", "rollen", "role"},
	"notes":           {"notes", "note", "remarks", "comments", "opmerkingen", "bemerkungen", "notizen", "remarques"},
	"external_id":     {"id", "volunteerid", "memberid", "number", "nummer", "mitgliedsnummer", "numero"},
}

// normHeader lowercases a header and keeps only letters (accents removed).
func normHeader(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case strings.ContainsRune("àáâä", r):
			b.WriteRune('a')
		case strings.ContainsRune("èéêë", r):
			b.WriteRune('e')
		case strings.ContainsRune("ìíîï", r):
			b.WriteRune('i')
		case strings.ContainsRune("òóôö", r):
			b.WriteRune('o')
		case strings.ContainsRune("ùúûü", r):
			b.WriteRune('u')
		case r == 'ß':
			b.WriteString("ss")
		case r == 'ç':
			b.WriteRune('c')
		}
	}
	return b.String()
}

// Suggest maps headers to fields: field -> header.
func Suggest(headers []string) map[string]string {
	out := map[string]string{}
	used := map[string]bool{}
	for _, field := range Fields {
		for _, h := range headers {
			n := normHeader(h)
			if n == "" || used[h] {
				continue
			}
			for _, syn := range append([]string{normHeader(field)}, headerSynonyms[field]...) {
				if n == syn {
					out[field] = h
					used[h] = true
					break
				}
			}
			if out[field] != "" {
				break
			}
		}
	}
	return out
}

// Cell is one spreadsheet value; Numeric is true when Excel stored a number.
type Cell struct {
	Value   string
	Numeric bool
}

// Sheet is a parsed table.
type Sheet struct {
	Name string
	Rows [][]Cell
}

// Workbook is a parsed file.
type Workbook struct {
	Format    string // xlsx | csv
	Encoding  string // csv: utf-8, utf-8-bom, windows-1252
	Delimiter string // csv
	Sheets    []Sheet
}

// ErrUnsupported is returned for legacy .xls, .ods and other formats.
var ErrUnsupported = errors.New("unsupported file: save it as .xlsx (Excel workbook) or .csv and upload that")

// Parse reads an .xlsx or .csv file.
func Parse(name string, data []byte) (*Workbook, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("the file is larger than %d MB", MaxBytes>>20)
	}
	lower := strings.ToLower(name)
	switch {
	case bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0}) || strings.HasSuffix(lower, ".xls") || strings.HasSuffix(lower, ".ods"):
		return nil, ErrUnsupported
	case bytes.HasPrefix(data, []byte("PK")):
		return parseXLSX(data)
	case strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".txt") || utf8.Valid(data) || !bytes.ContainsRune(data, 0):
		return parseCSV(data)
	}
	return nil, ErrUnsupported
}

func parseXLSX(data []byte) (*Workbook, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not read the workbook: %w", err)
	}
	defer func() { _ = f.Close() }()
	wb := &Workbook{Format: "xlsx"}
	for _, name := range f.GetSheetList() {
		rows, err := f.Rows(name)
		if err != nil {
			return nil, err
		}
		sh := Sheet{Name: name}
		r := 0
		for rows.Next() {
			r++
			if r > MaxRows+50 {
				break
			}
			cols, err := rows.Columns()
			if err != nil {
				return nil, err
			}
			row := make([]Cell, len(cols))
			for c, v := range cols {
				row[c] = Cell{Value: v}
				if v != "" {
					ref, _ := excelize.CoordinatesToCellName(c+1, r)
					if t, err := f.GetCellType(name, ref); err == nil && (t == excelize.CellTypeNumber || t == excelize.CellTypeUnset && isNumber(v)) {
						row[c].Numeric = true
						if raw, err := f.GetCellValue(name, ref, excelize.Options{RawCellValue: true}); err == nil && raw != "" {
							row[c].Value = raw
						}
					}
				}
			}
			sh.Rows = append(sh.Rows, row)
		}
		_ = rows.Close()
		wb.Sheets = append(wb.Sheets, sh)
	}
	return wb, nil
}

var numberRE = regexp.MustCompile(`^-?\d+(\.\d+)?(E[+-]?\d+)?$`)

func isNumber(s string) bool { return numberRE.MatchString(strings.TrimSpace(s)) }

func parseCSV(data []byte) (*Workbook, error) {
	wb := &Workbook{Format: "csv", Encoding: "utf-8"}
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data, wb.Encoding = data[3:], "utf-8-bom"
	} else if !utf8.Valid(data) {
		dec, err := charmap.Windows1252.NewDecoder().Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("unknown text encoding: save the file as CSV UTF-8")
		}
		data, wb.Encoding = dec, "windows-1252"
	}
	wb.Delimiter = detectDelimiter(data)
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = []rune(wb.Delimiter)[0]
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	sh := Sheet{Name: "CSV"}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("could not read the CSV file: %w", err)
		}
		row := make([]Cell, len(rec))
		for i, v := range rec {
			row[i] = Cell{Value: v}
		}
		sh.Rows = append(sh.Rows, row)
		if len(sh.Rows) > MaxRows+50 {
			break
		}
	}
	wb.Sheets = []Sheet{sh}
	return wb, nil
}

// detectDelimiter picks ",", ";" or tab, whichever appears most often
// outside quotes in the first ten lines (a title row may have none).
func detectDelimiter(data []byte) string {
	counts := map[rune]int{}
	inQuote := false
	lines := 0
	for _, r := range string(data) {
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if r == '\n' && !inQuote {
			if lines++; lines >= 10 {
				break
			}
		}
		if !inQuote && (r == ',' || r == ';' || r == '\t') {
			counts[r]++
		}
	}
	best, n := ',', -1
	for _, r := range []rune{',', ';', '\t'} {
		if counts[r] > n {
			best, n = r, counts[r]
		}
	}
	return string(best)
}

// Table is a sheet with its header row picked.
type Table struct {
	Headers []string
	// Rows are the data rows; RowNumbers are their 1-based numbers in the
	// sheet (for the error report).
	Rows       [][]Cell
	RowNumbers []int
}

// Table cuts a sheet at the header row (1-based; 0 picks the first row
// with at least two non-empty cells, skipping title rows above it).
func (s Sheet) Table(headerRow int) (*Table, error) {
	if headerRow <= 0 {
		for i, row := range s.Rows {
			filled := 0
			for _, c := range row {
				if strings.TrimSpace(c.Value) != "" {
					filled++
				}
			}
			if filled >= 2 {
				headerRow = i + 1
				break
			}
		}
	}
	if headerRow <= 0 || headerRow > len(s.Rows) {
		return nil, errors.New("no header row found")
	}
	t := &Table{}
	for _, c := range s.Rows[headerRow-1] {
		t.Headers = append(t.Headers, strings.TrimSpace(c.Value))
	}
	for i := headerRow; i < len(s.Rows); i++ {
		empty := true
		for _, c := range s.Rows[i] {
			if strings.TrimSpace(c.Value) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		t.Rows = append(t.Rows, s.Rows[i])
		t.RowNumbers = append(t.RowNumbers, i+1)
	}
	if len(t.Rows) > MaxRows {
		return nil, fmt.Errorf("the sheet has more than %d rows; split it into several files", MaxRows)
	}
	return t, nil
}

// Value returns the cell under a header ("" when missing).
func (t *Table) Value(row int, header string) Cell {
	for i, h := range t.Headers {
		if h == header && i < len(t.Rows[row]) {
			c := t.Rows[row][i]
			c.Value = strings.TrimSpace(c.Value)
			return c
		}
	}
	return Cell{}
}

var mailtoRE = regexp.MustCompile(`(?i)^mailto:`)

// CleanEmail removes "mailto:" and whitespace; changed reports a cleanup.
func CleanEmail(s string) (clean string, changed bool) {
	clean = mailtoRE.ReplaceAllString(strings.TrimSpace(s), "")
	clean = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, clean)
	clean = strings.ToLower(clean)
	return clean, clean != strings.ToLower(strings.TrimSpace(s))
}

// excelEpoch is day 0 of Excel's 1900 date system (with its leap-year bug).
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// ParseDate reads a date of birth. It accepts YYYY-MM-DD, DD.MM.YYYY,
// DD-MM-YYYY, DD/MM/YYYY and Excel serial numbers; note explains a
// conversion the reviewer should check.
func ParseDate(c Cell) (date, note string, err error) {
	v := strings.TrimSpace(c.Value)
	if v == "" {
		return "", "", nil
	}
	if isNumber(v) {
		f, _ := strconv.ParseFloat(v, 64)
		if f < 1 || f > 80000 {
			return "", "", fmt.Errorf("%q is not a date", v)
		}
		d := excelEpoch.AddDate(0, 0, int(f)).Format("2006-01-02")
		return d, fmt.Sprintf("date of birth was stored as the number %s; read as %s", v, d), nil
	}
	if i := strings.IndexAny(v, " T"); i > 0 {
		v = v[:i]
	}
	for _, layout := range []string{"2006-01-02", "02.01.2006", "2.1.2006", "02-01-2006", "02/01/2006", "2/1/2006"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("2006-01-02"), "", nil
		}
	}
	return "", "", fmt.Errorf("%q is not a date (use YYYY-MM-DD or DD.MM.YYYY)", v)
}

// PhoneWarning flags phone numbers Excel damaged: stored as a number (lost
// "+" or leading zero).
func PhoneWarning(c Cell) string {
	v := strings.TrimSpace(c.Value)
	if v == "" {
		return ""
	}
	if c.Numeric || isNumber(v) {
		return fmt.Sprintf("phone %s was stored as a number, so a leading + or 0 may be missing; check it", v)
	}
	return ""
}

// PhoneDigits turns a numeric cell ("4.4207946E+11") back into digits.
func PhoneDigits(c Cell) string {
	v := strings.TrimSpace(c.Value)
	if c.Numeric || isNumber(v) {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return strconv.FormatFloat(f, 'f', 0, 64)
		}
	}
	return v
}

// SplitValues splits a cell holding several values ("Sec; Info").
func SplitValues(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ErrorReport builds an .xlsx with the given columns plus "problem".
func ErrorReport(columns []string, rows []map[string]string) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)
	header := append([]string{"row"}, append(columns, "problem")...)
	for i, h := range header {
		ref, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellStr(sheet, ref, h)
	}
	for r, row := range rows {
		for i, h := range header {
			ref, _ := excelize.CoordinatesToCellName(i+1, r+2)
			// Text cells, so phone numbers keep their + and leading zeros.
			_ = f.SetCellStr(sheet, ref, row[h])
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
