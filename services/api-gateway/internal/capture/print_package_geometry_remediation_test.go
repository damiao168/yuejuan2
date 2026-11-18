package capture

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPrintPackagePreservesWholePageGeometryInRenderedPDF(t *testing.T) {
	// 从正式布局解析器一直走到压缩后的 PDF 向量；期望坐标独立按整页毫米计算，防止只测辅助映射而漏掉渲染内框。
	for _, dimensions := range []struct {
		name          string
		width, height int
		mmW, mmH      float64
	}{
		{"portrait_non_A4", 1000, 1400, 210, 294},
		{"landscape", 2160, 1440, 315, 210},
	} {
		t.Run(dimensions.name, func(t *testing.T) {
			layout := fmt.Sprintf(`{"pages":[{"page_no":1,"width":%d,"height":%d,
				"registration_marks":[{"id":"top-left","x":0.03,"y":0.03,"width":0.02,"height":0.02}],
				"identity_regions":[{"id":"student","x":0.1,"y":0.08,"width":0.4,"height":0.04},
				{"id":"controlled_qr","x":0.7,"y":0.05,"width":0.2,"height":0.18}],
				"question_regions":[{"id":"q1","question_id":"question1","x":0.1,"y":0.25,"width":0.3,"height":0.4,
				"option_coordinate_space":"question_relative","option_regions":[{"id":"A","x":0.1,"y":0.1,"width":0.1,"height":0.1}]}],
				"grading_groups":[{"group_id":"g1","question_ids":["question1"],"bbox":{"x":0.06,"y":0.2,"width":0.5,"height":0.5}}]}]}`, dimensions.width, dimensions.height)
			pages, err := printPackagePages([]byte(layout), 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			parsed := pages[1]
			page := PrintPackagePage{PageNo: 1, TemplateWidth: parsed.width, TemplateHeight: parsed.height,
				BarcodeValue: "EG1.synthetic-controlled-print.signature", QuestionRegions: parsed.regions,
				RegistrationMarks: parsed.marks, IdentityRegions: parsed.identity, GradingGroups: parsed.groups}
			pdf, _, err := RenderStudentPrintPackage(printGeometryPackage(page))
			if err != nil {
				t.Fatal(err)
			}
			ptPerMM := 72.0 / 25.4
			mediaBox := fmt.Sprintf("/MediaBox [0 0 %.2f %.2f]", dimensions.mmW*ptPerMM, dimensions.mmH*ptPerMM)
			if !bytes.Contains(pdf, []byte(mediaBox)) {
				t.Fatalf("PDF page size must preserve template aspect: expected %s", mediaBox)
			}
			content := printPDFContent(t, pdf)
			for _, shape := range []struct {
				name       string
				x, y, w, h float64
				operator   string
			}{
				{"registration mark", .03, .03, .02, .02, "f"},
				{"question", .1, .25, .3, .4, "S"},
				{"group", .06, .2, .5, .5, "S"},
				{"identity", .1, .08, .4, .04, "S"},
				{"relative option", .13, .29, .03, .04, "S"},
			} {
				command := fmt.Sprintf("%.2f %.2f %.2f %.2f re %s", shape.x*dimensions.mmW*ptPerMM,
					(1-shape.y)*dimensions.mmH*ptPerMM, shape.w*dimensions.mmW*ptPerMM, -shape.h*dimensions.mmH*ptPerMM, shape.operator)
				if !strings.Contains(content, command) {
					t.Errorf("%s did not retain template whole-page coordinates: missing %s", shape.name, command)
				}
			}
		})
	}
}

func TestPrintQRUsesDeclaredRegionOrRequiresExplicitConfirmation(t *testing.T) {
	base := PrintPackagePage{PageNo: 1, TemplateWidth: 1000, TemplateHeight: 1400, BarcodeValue: "EG1.synthetic.signature",
		RegistrationMarks: []PrintPackageRegion{{X: .03, Y: .03, Width: .02, Height: .02}},
		QuestionRegions:   []PrintPackageRegion{{X: .1, Y: .25, Width: .8, Height: .65}},
		IdentityRegions: []PrintPackageRegion{{ID: "student", X: .1, Y: .05, Width: .4, Height: .1},
			{ID: "二维码", X: .7, Y: .05, Width: .2, Height: .18}}}
	qr, err := printQRRegion(base, 210, 294)
	if err != nil || math.Abs(qr.X-.7) > 1e-9 || math.Abs(qr.Width*210-qr.Height*294) > 1e-9 {
		t.Fatalf("declared QR must remain inside its original physical square: %#v, %v", qr, err)
	}
	base.IdentityRegions = base.IdentityRegions[:1]
	if _, err = printQRRegion(base, 210, 294); !errors.Is(err, ErrPrintLayoutConfirmationRequired) {
		t.Fatalf("unlabelled page content cannot be treated as blank: %v", err)
	}
	base.confirmedQR = &PrintPackageRegion{X: .7, Y: .05, Width: .2, Height: .18}
	qr, err = printQRRegion(base, 210, 294)
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range append(append([]PrintPackageRegion{}, base.QuestionRegions...), base.IdentityRegions...) {
		if overlapsPrintRegion(qr, region) {
			t.Fatalf("confirmed QR overlaps declared region: %#v, %#v", qr, region)
		}
	}
	for _, name := range []string{"no_space", "declared_overlaps_question", "declared_too_small"} {
		t.Run(name, func(t *testing.T) {
			page := base
			switch name {
			case "no_space":
				page.QuestionRegions = []PrintPackageRegion{{Width: 1, Height: 1}}
			case "declared_overlaps_question":
				page.IdentityRegions = []PrintPackageRegion{{ID: "qr", X: .2, Y: .3, Width: .2, Height: .2}}
			case "declared_too_small":
				page.IdentityRegions = []PrintPackageRegion{{ID: "qr", X: .7, Y: .05, Width: .05, Height: .05}}
			}
			if _, _, err := RenderStudentPrintPackage(printGeometryPackage(page)); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("unprintable QR layout must fail visibly instead of moving question regions: %v", err)
			}
		})
	}
}

func TestPrintQuestionOptionCoordinateCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, space string
		option      PrintPackageRegion
		wantX       float64
	}{
		{"explicit_relative", "question_relative", PrintPackageRegion{X: .3, Y: .3, Width: .1, Height: .1}, .35},
		{"legacy_absolute", "", PrintPackageRegion{X: .3, Y: .3, Width: .1, Height: .1}, .3},
		{"legacy_relative", "", PrintPackageRegion{X: .1, Y: .1, Width: .1, Height: .1}, .25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			question := PrintPackageRegion{X: .2, Y: .2, Width: .5, Height: .5,
				OptionCoordinateSpace: tc.space, OptionRegions: []PrintPackageRegion{tc.option}}
			options := printQuestionOptions(question)
			if math.Abs(options[0].X-tc.wantX) > 1e-9 || question.OptionRegions[0].X != tc.option.X {
				t.Fatalf("option coordinates must preserve the scoring reader convention without mutating source: %#v", options)
			}
		})
	}
}

func printGeometryPackage(page PrintPackagePage) StudentPrintPackage {
	return StudentPrintPackage{PrintBatchID: "batch", ExamID: "exam", TemplateID: "unchanged-template", TemplateContentHash: "sha256:unchanged",
		IssuedAt: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC), Sheets: []PrintPackageSheet{{StudentID: "synthetic-student", SheetSerial: "serial", Ordinal: 1, Pages: []PrintPackagePage{page}}}}
}

func printPDFContent(t *testing.T, pdf []byte) string {
	t.Helper()
	var content strings.Builder
	streams := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindAllSubmatch(pdf, -1)
	if len(streams) == 0 {
		t.Fatal("PDF has no content streams")
	}
	for _, stream := range streams {
		reader, err := zlib.NewReader(bytes.NewReader(stream[1]))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		content.Write(decoded)
	}
	return content.String()
}
