package importer

import (
	"errors"
	"testing"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
)

func TestParseCSVDelimiterEncodingAndQuotes(t *testing.T) {
	raw := "Titel: Vrijwilligers 2026\n" +
		"Voornaam;Achternaam;E-mail;Telefoon;Opmerkingen\n" +
		"Zoë;Müller;mailto: Zoe@Example.org ;0612345678;\"two\nlines\"\n"
	win, _ := charmap.Windows1252.NewEncoder().String(raw)
	wb, err := Parse("list.csv", []byte(win))
	if err != nil {
		t.Fatal(err)
	}
	if wb.Encoding != "windows-1252" || wb.Delimiter != ";" {
		t.Fatalf("encoding %s delimiter %q", wb.Encoding, wb.Delimiter)
	}
	tbl, err := wb.Sheets[0].Table(0)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Headers[0] != "Voornaam" || len(tbl.Rows) != 1 || tbl.RowNumbers[0] != 3 {
		t.Fatalf("header row should skip the title: %+v", tbl)
	}
	if v := tbl.Value(0, "Voornaam").Value; v != "Zoë" {
		t.Fatalf("Windows-1252 not decoded: %q", v)
	}
	if v := tbl.Value(0, "Opmerkingen").Value; v != "two\nlines" {
		t.Fatalf("quoted line break: %q", v)
	}
	m := Suggest(tbl.Headers)
	if m["first_name"] != "Voornaam" || m["last_name"] != "Achternaam" || m["email"] != "E-mail" || m["phone"] != "Telefoon" || m["notes"] != "Opmerkingen" {
		t.Fatalf("suggestions: %v", m)
	}
	if e, changed := CleanEmail(tbl.Value(0, "E-mail").Value); e != "zoe@example.org" || !changed {
		t.Fatalf("email cleanup: %q %v", e, changed)
	}

	bom := append([]byte{0xEF, 0xBB, 0xBF}, []byte("Name,Email\tx\nA,a@b.c\n")...)
	wb, err = Parse("x.csv", bom)
	if err != nil || wb.Encoding != "utf-8-bom" || wb.Delimiter != "," {
		t.Fatalf("BOM file: %+v %v", wb, err)
	}
}

func TestParseXLSXDamage(t *testing.T) {
	f := excelize.NewFile()
	s := f.GetSheetName(0)
	_ = f.SetSheetRow(s, "A1", &[]any{"First name", "Nachname", "Phone", "Date of birth", "Geburtsdatum"})
	_ = f.SetSheetRow(s, "A2", &[]any{"Ada", "Lovelace", 31612345678, 33124, "10.12.1990"})
	_, _ = f.NewSheet("Second")
	buf, _ := f.WriteToBuffer()
	wb, err := Parse("list.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(wb.Sheets) != 2 || wb.Sheets[1].Name != "Second" {
		t.Fatalf("sheets %+v", wb.Sheets)
	}
	tbl, err := wb.Sheets[0].Table(1)
	if err != nil {
		t.Fatal(err)
	}
	phone := tbl.Value(0, "Phone")
	if PhoneWarning(phone) == "" || PhoneDigits(phone) != "31612345678" {
		t.Fatalf("numeric phone %+v: warning %q digits %q", phone, PhoneWarning(phone), PhoneDigits(phone))
	}
	d, note, err := ParseDate(tbl.Value(0, "Date of birth"))
	if err != nil || d != "1990-09-08" || note == "" {
		t.Fatalf("serial date: %q %q %v", d, note, err)
	}
	d, note, err = ParseDate(tbl.Value(0, "Geburtsdatum"))
	if err != nil || d != "1990-12-10" || note != "" {
		t.Fatalf("European date: %q %q %v", d, note, err)
	}
	if _, _, err := ParseDate(Cell{Value: "soon"}); err == nil {
		t.Fatal("bad date accepted")
	}
}

func TestParseRejectsLegacyFormats(t *testing.T) {
	if _, err := Parse("old.xls", []byte{0xD0, 0xCF, 0x11, 0xE0, 0, 0}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf(".xls: %v", err)
	}
	if _, err := Parse("calc.ods", []byte("PK...")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf(".ods: %v", err)
	}
}

func TestErrorReportKeepsText(t *testing.T) {
	data, err := ErrorReport([]string{"email", "phone"}, []map[string]string{{"row": "3", "email": "x", "phone": "+316123", "problem": "bad email"}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytesReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := f.GetCellValue(f.GetSheetName(0), "C2"); v != "+316123" {
		t.Fatalf("phone cell %q", v)
	}
	if v, _ := f.GetCellValue(f.GetSheetName(0), "D1"); v != "problem" {
		t.Fatalf("problem column %q", v)
	}
}
