package capture

import (
	"errors"
	"strings"
	"testing"
)

func TestPrintLayoutRequiresEveryMissingPageAndFrozenSource(t *testing.T) {
	pages := map[int]printPackagePageLayout{
		1: {width: 420, height: 594},
		2: {width: 380, height: 340, identity: []PrintPackageRegion{{ID: "qr", X: .7, Y: .05, Width: .25, Height: .3}}},
	}
	hash := strings.Repeat("a", 64)
	if _, err := normalizedStudentPrintLayout(pages, hash, nil); !errors.Is(err, ErrPrintLayoutConfirmationRequired) {
		t.Fatal(err)
	}
	valid := StudentPrintLayout{SourceSHA256: strings.ToUpper(hash), Pages: []StudentPrintQRPage{{PageNo: 1, X: .03, Y: .70, Width: .24, Height: .27}}}
	got, err := normalizedStudentPrintLayout(pages, hash, &valid)
	if err != nil || got.SourceSHA256 != hash {
		t.Fatalf("valid source and missing page rejected: %+v %v", got, err)
	}
	for _, name := range []string{"changed_source", "missing_page", "dedicated_override", "duplicate_page", "out_of_page"} {
		t.Run(name, func(t *testing.T) {
			input := valid
			input.Pages = append([]StudentPrintQRPage{}, valid.Pages...)
			switch name {
			case "changed_source":
				input.SourceSHA256 = strings.Repeat("b", 64)
			case "missing_page":
				input.Pages = nil
			case "dedicated_override":
				input.Pages[0].PageNo = 2
			case "duplicate_page":
				input.Pages = append(input.Pages, input.Pages[0])
			case "out_of_page":
				input.Pages[0].X = .9
			}
			if _, err := normalizedStudentPrintLayout(pages, hash, &input); err == nil {
				t.Fatal("invalid print confirmation accepted")
			}
		})
	}
}

func TestUnlabelledSourceHeaderRequiresConfirmationBeforePreflight(t *testing.T) {
	background := samplePrintBackground(t)
	pages := map[int]printPackagePageLayout{1: {width: 420, height: 594}}
	if err := validatePrintPackageBackgroundAndGeometry(pages, background); !errors.Is(err, ErrPrintLayoutConfirmationRequired) {
		t.Fatalf("unlabelled EXAM NOTICE must never be assumed blank: %v", err)
	}
	confirmed := &StudentPrintLayout{Pages: []StudentPrintQRPage{{PageNo: 1, X: .03, Y: .70, Width: .24, Height: .27}}}
	if err := validatePrintPackageBackgroundAndGeometry(pages, background, confirmed); err != nil {
		t.Fatal(err)
	}
	confirmed.Pages[0].Width, confirmed.Pages[0].Height = .01, .01
	if err := validatePrintPackageBackgroundAndGeometry(pages, background, confirmed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("small confirmed QR accepted: %v", err)
	}
}

func TestPrintLayoutReplayNormalizesOrderWithoutMovingRegions(t *testing.T) {
	a := &StudentPrintLayout{SourceSHA256: strings.Repeat("A", 64), Pages: []StudentPrintQRPage{
		{PageNo: 2, X: .1, Y: .7, Width: .2, Height: .2},
		{PageNo: 1, X: .2, Y: .7, Width: .2, Height: .2},
	}}
	b := &StudentPrintLayout{SourceSHA256: strings.Repeat("a", 64), Pages: []StudentPrintQRPage{a.Pages[1], a.Pages[0]}}
	if !sameStudentPrintLayout(a, b) || studentBarcodePrintRequestHash([]string{"student-b", "student-a"}, a) != studentBarcodePrintRequestHash([]string{"student-a", "student-b"}, b) {
		t.Fatal("same confirmed regions must replay across page order and hexadecimal case")
	}
	b.Pages[0].X += .01
	if sameStudentPrintLayout(a, b) {
		t.Fatal("a moved QR must be a different layout")
	}
	if a.Pages[0].PageNo != 2 {
		t.Fatal("canonical comparison mutated frozen input")
	}
}

func TestPrintLayoutHashRetainsLegacyStudentSetValidation(t *testing.T) {
	studentID := "aabbccdd-1111-4111-8111-111111111111"
	if _, err := barcodeStudentIDs([]string{studentID, strings.ToUpper(studentID)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate UUID spellings must still be rejected before hashing: %v", err)
	}
	students := []string{"student-b", "student-a"}
	if studentBarcodePrintRequestHash(students, nil) != studentBarcodeRequestHash(students) {
		t.Fatal("requests without a layout must retain the existing ledger hash")
	}
	if students[0] != "student-b" {
		t.Fatal("hashing changed the requested print order")
	}
}
