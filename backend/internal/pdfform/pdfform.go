// Package pdfform lists and fills AcroForm fields with pdfcpu.
package pdfform

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/filter"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Field is one AcroForm field discovered in a PDF.
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"` // text | choice | button
}

// ListFields returns the named form fields in pdfBytes.
//
// pdfcpu's FormFields path fails on some common samples (LibreOffice
// OoPdfFormExample.pdf hits "corrupt choice field" while parsing Opt).
// We only need names + types for mapping, so we walk AcroForm.Fields ourselves.
func ListFields(pdfBytes []byte) ([]Field, error) {
	if len(pdfBytes) == 0 {
		return nil, fmt.Errorf("empty pdf")
	}
	ctx, err := readPDF(pdfBytes)
	if err != nil {
		return nil, err
	}
	arr, err := formFields(ctx)
	if err != nil || arr == nil {
		return nil, err
	}
	out := make([]Field, 0, len(arr))
	seen := map[string]bool{}
	if err := walkFields(ctx.XRefTable, arr, "", seen, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func readPDF(pdfBytes []byte) (*model.Context, error) {
	rs := bytes.NewReader(pdfBytes)
	conf := model.NewDefaultConfiguration()
	conf.Cmd = model.LISTFORMFIELDS
	ctx, err := api.ReadValidateAndOptimize(rs, conf)
	if err != nil {
		return nil, fmt.Errorf("read pdf: %w", err)
	}
	return ctx, nil
}

func formFields(ctx *model.Context) (types.Array, error) {
	if ctx.Form == nil {
		return nil, nil
	}
	o, ok := ctx.Form.Find("Fields")
	if !ok {
		return nil, nil
	}
	arr, err := ctx.DereferenceArray(o)
	if err != nil {
		return nil, fmt.Errorf("form fields: %w", err)
	}
	return arr, nil
}

func walkFields(xRef *model.XRefTable, arr types.Array, parentName string, seen map[string]bool, out *[]Field) error {
	for _, v := range arr {
		indRef, ok := v.(types.IndirectRef)
		if !ok {
			continue
		}
		d, err := xRef.DereferenceDict(indRef)
		if err != nil || len(d) == 0 {
			continue
		}
		name := parentName
		if t, err := d.StringOrHexLiteralEntry("T"); err == nil && t != nil && strings.TrimSpace(*t) != "" {
			part := strings.TrimSpace(*t)
			if name == "" {
				name = part
			} else {
				name = name + "." + part
			}
		}
		ft := d.NameEntry("FT")
		kidsObj, hasKids := d.Find("Kids")
		if hasKids {
			kids, err := xRef.DereferenceArray(kidsObj)
			if err != nil {
				continue
			}
			if ft != nil && name != "" && !seen[name] {
				seen[name] = true
				*out = append(*out, Field{Name: name, Type: ftName(*ft)})
			}
			if err := walkFields(xRef, kids, name, seen, out); err != nil {
				return err
			}
			continue
		}
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		typ := "text"
		if ft != nil {
			typ = ftName(*ft)
		}
		*out = append(*out, Field{Name: name, Type: typ})
	}
	return nil
}

func ftName(ft string) string {
	switch ft {
	case "Btn":
		return "button"
	case "Ch":
		return "choice"
	default:
		return "text"
	}
}

// Fill writes values into AcroForm fields of pdfBytes and returns a new PDF.
// Unknown field names are ignored. Empty values are skipped.
//
// We set /V on the field dictionaries ourselves, write a simple /AP so
// viewers that ignore NeedAppearances (LibreOffice) still show the text,
// and turn on NeedAppearances for Acrobat/Chrome. pdfcpu's FillForm fails
// on some LibreOffice samples ("corrupt choice field") even when only text
// fields need filling.
func Fill(pdfBytes []byte, values map[string]string) ([]byte, error) {
	if len(values) == 0 {
		return pdfBytes, nil
	}
	want := map[string]string{}
	for name, val := range values {
		name = strings.TrimSpace(name)
		val = strings.TrimSpace(val)
		if name == "" || val == "" {
			continue
		}
		want[name] = val
	}
	if len(want) == 0 {
		return pdfBytes, nil
	}

	ctx, err := readPDF(pdfBytes)
	if err != nil {
		return nil, err
	}
	arr, err := formFields(ctx)
	if err != nil {
		return nil, err
	}
	if arr == nil {
		return pdfBytes, nil
	}
	if ctx.Form != nil {
		ctx.Form.Update("NeedAppearances", types.Boolean(true))
	}
	filled := 0
	if err := applyValues(ctx.XRefTable, arr, "", want, &filled); err != nil {
		return nil, err
	}
	if filled == 0 {
		return pdfBytes, nil
	}
	var out bytes.Buffer
	if err := api.WriteContext(ctx, &out); err != nil {
		return nil, fmt.Errorf("write pdf: %w", err)
	}
	return out.Bytes(), nil
}

func applyValues(xRef *model.XRefTable, arr types.Array, parentName string, want map[string]string, filled *int) error {
	for _, v := range arr {
		indRef, ok := v.(types.IndirectRef)
		if !ok {
			continue
		}
		d, err := xRef.DereferenceDict(indRef)
		if err != nil || len(d) == 0 {
			continue
		}
		name := parentName
		if t, err := d.StringOrHexLiteralEntry("T"); err == nil && t != nil && strings.TrimSpace(*t) != "" {
			part := strings.TrimSpace(*t)
			if name == "" {
				name = part
			} else {
				name = name + "." + part
			}
		}
		ft := d.NameEntry("FT")
		kidsObj, hasKids := d.Find("Kids")
		if hasKids {
			if ft != nil && name != "" {
				if val, ok := want[name]; ok {
					if err := setFieldValue(xRef, d, *ft, val); err != nil {
						return err
					}
					*filled++
				}
			}
			kids, err := xRef.DereferenceArray(kidsObj)
			if err != nil {
				continue
			}
			if err := applyValues(xRef, kids, name, want, filled); err != nil {
				return err
			}
			continue
		}
		if name == "" {
			continue
		}
		val, ok := want[name]
		if !ok {
			continue
		}
		ftStr := "Tx"
		if ft != nil {
			ftStr = *ft
		}
		if err := setFieldValue(xRef, d, ftStr, val); err != nil {
			return err
		}
		*filled++
	}
	return nil
}

func setFieldValue(xRef *model.XRefTable, d types.Dict, ft, val string) error {
	switch ft {
	case "Btn":
		on := strings.EqualFold(val, "true") || strings.EqualFold(val, "yes") || strings.EqualFold(val, "1") || strings.EqualFold(val, "on") || strings.EqualFold(val, "x")
		if on {
			d.Update("V", types.Name("Yes"))
			d.Update("AS", types.Name("Yes"))
		} else {
			d.Update("V", types.Name("Off"))
			d.Update("AS", types.Name("Off"))
		}
		return nil
	default:
		enc, err := types.EscapedUTF16String(val)
		if err != nil || enc == nil {
			d.Update("V", types.StringLiteral(val))
		} else {
			d.Update("V", types.StringLiteral(*enc))
		}
		return setTextAppearance(xRef, d, val)
	}
}

// setTextAppearance writes a minimal /AP /N Form XObject so viewers that
// ignore NeedAppearances still paint the filled value.
func setTextAppearance(xRef *model.XRefTable, d types.Dict, val string) error {
	w, h := fieldSize(d)
	if w <= 0 || h <= 0 {
		d.Delete("AP")
		return nil
	}
	fontSize := parseDAFontSize(d)
	if fontSize <= 0 {
		fontSize = 11
	}
	// Baseline a little above the bottom of the widget.
	baseline := fontSize * 0.25
	if baseline < 2 {
		baseline = 2
	}
	escaped, err := types.Escape(pdfDocApprox(val))
	if err != nil || escaped == nil {
		s := strings.ReplaceAll(val, "\\", "\\\\")
		s = strings.ReplaceAll(s, "(", "\\(")
		s = strings.ReplaceAll(s, ")", "\\)")
		escaped = &s
	}
	content := fmt.Sprintf("BT /Helv %.2f Tf 2 %.2f Td (%s) Tj ET", fontSize, baseline, *escaped)

	fontDict := types.Dict(map[string]types.Object{
		"Type":     types.Name("Font"),
		"Subtype":  types.Name("Type1"),
		"BaseFont": types.Name("Helvetica"),
	})
	fontRef, err := xRef.IndRefForNewObject(fontDict)
	if err != nil {
		return fmt.Errorf("font object: %w", err)
	}
	res := types.Dict(map[string]types.Object{
		"Font": types.Dict(map[string]types.Object{"Helv": *fontRef}),
	})

	sd := types.StreamDict{
		Dict: types.Dict(map[string]types.Object{
			"Type":      types.Name("XObject"),
			"Subtype":   types.Name("Form"),
			"BBox":      types.NewNumberArray(0, 0, w, h),
			"Resources": res,
		}),
		Content:        []byte(content),
		FilterPipeline: []types.PDFFilter{{Name: filter.Flate, DecodeParms: nil}},
	}
	sd.InsertName("Filter", filter.Flate)
	if err := sd.Encode(); err != nil {
		return fmt.Errorf("encode appearance: %w", err)
	}
	apRef, err := xRef.IndRefForNewObject(sd)
	if err != nil {
		return fmt.Errorf("appearance object: %w", err)
	}
	d.Update("AP", types.Dict(map[string]types.Object{"N": *apRef}))
	return nil
}

func fieldSize(d types.Dict) (float64, float64) {
	o, ok := d.Find("Rect")
	if !ok {
		return 0, 0
	}
	arr, ok := o.(types.Array)
	if !ok || len(arr) < 4 {
		// May be an indirect array; best-effort skip.
		return 0, 0
	}
	rect := types.RectForArray(arr)
	if rect == nil {
		return 0, 0
	}
	return rect.Width(), rect.Height()
}

func parseDAFontSize(d types.Dict) float64 {
	da, err := d.StringOrHexLiteralEntry("DA")
	if err != nil || da == nil {
		return 0
	}
	// Typical: "0 0 0 rg /F3 11 Tf"
	parts := strings.Fields(*da)
	for i := 0; i+1 < len(parts); i++ {
		if parts[i+1] == "Tf" {
			f, err := strconv.ParseFloat(parts[i], 64)
			if err == nil {
				return f
			}
		}
	}
	return 0
}

// pdfDocApprox maps runes to bytes Helvetica can show (Latin-1-ish).
func pdfDocApprox(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0x20AC: // euro — Helvetica has no euro in standard encoding; use placeholder
			b.WriteByte('E')
		case r >= 0 && r <= 0xFF:
			b.WriteByte(byte(r))
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}
