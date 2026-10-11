package capture

import (
	"errors"
	"strings"
	"testing"
)

func TestControlledBarcodeRoundTripAndTamperRejection(t *testing.T) {
	keyring := BarcodeKeyring{ActiveKeyID: "2026-01", Keys: map[string][]byte{"2026-01": []byte(strings.Repeat("k", 32))}}
	input := BarcodeClaims{TenantID: "11111111-1111-4111-8111-111111111111", ExamID: "22222222-2222-4222-8222-222222222222", TemplateID: "33333333-3333-4333-8333-333333333333", TemplateContentHash: "sha256:template", PageNo: 2, Nonce: "random-128-bit"}
	value, err := keyring.Sign(input)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := keyring.Verify(value)
	if err != nil || claims.PageNo != 2 || claims.KeyID != "2026-01" {
		t.Fatalf("verify returned %#v, %v", claims, err)
	}
	tampered := strings.Replace(value, "EG1.", "EG1.A", 1)
	if _, err = keyring.Verify(tampered); !errors.Is(err, ErrBarcodeInvalid) {
		t.Fatalf("tampered value should be rejected, got %v", err)
	}
}

func TestControlledBarcodeRejectsUnknownKeyAndInvalidClaims(t *testing.T) {
	keyring := BarcodeKeyring{ActiveKeyID: "active", Keys: map[string][]byte{"active": []byte(strings.Repeat("a", 32))}}
	_, err := keyring.Sign(BarcodeClaims{TenantID: "bad", ExamID: "bad", TemplateID: "bad", PageNo: 0})
	if !errors.Is(err, ErrBarcodeInvalid) {
		t.Fatalf("invalid claims should be rejected, got %v", err)
	}
}

func TestStudentBoundBarcodeRoundTrip(t *testing.T) {
	keyring := BarcodeKeyring{ActiveKeyID: "2026-01", Keys: map[string][]byte{"2026-01": []byte(strings.Repeat("k", 32))}}
	input := BarcodeClaims{
		TenantID: "11111111-1111-4111-8111-111111111111", ExamID: "22222222-2222-4222-8222-222222222222",
		TemplateID: "33333333-3333-4333-8333-333333333333", TemplateContentHash: "sha256:template",
		PageNo: 1, Nonce: "random-128-bit",
		StudentID: "44444444-4444-4444-8444-444444444444", SheetSerial: "55555555-5555-4555-8555-555555555555",
	}
	value, err := keyring.Sign(input)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := keyring.Verify(value)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Version != 2 || claims.StudentID != input.StudentID || claims.SheetSerial != input.SheetSerial {
		t.Fatalf("student claims lost in round trip: %#v", claims)
	}
}

func TestTemplateBarcodeStaysVersionOneAndStillVerifies(t *testing.T) {
	// 向后兼容是关键约束：第 1 版条码已印在实体答题纸上，第 2 版上线后仍须能通过验证。
	keyring := BarcodeKeyring{ActiveKeyID: "2026-01", Keys: map[string][]byte{"2026-01": []byte(strings.Repeat("k", 32))}}
	input := BarcodeClaims{TenantID: "11111111-1111-4111-8111-111111111111", ExamID: "22222222-2222-4222-8222-222222222222", TemplateID: "33333333-3333-4333-8333-333333333333", TemplateContentHash: "sha256:template", PageNo: 2, Nonce: "random-128-bit"}
	value, err := keyring.Sign(input)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := keyring.Verify(value)
	if err != nil || claims.Version != 1 || claims.StudentID != "" || claims.SheetSerial != "" {
		t.Fatalf("template barcode must stay version 1 without student claims: %#v, %v", claims, err)
	}
}

func TestStudentClaimsRejectInvalidIdentity(t *testing.T) {
	keyring := BarcodeKeyring{ActiveKeyID: "active", Keys: map[string][]byte{"active": []byte(strings.Repeat("a", 32))}}
	base := BarcodeClaims{
		TenantID: "11111111-1111-4111-8111-111111111111", ExamID: "22222222-2222-4222-8222-222222222222",
		TemplateID: "33333333-3333-4333-8333-333333333333", TemplateContentHash: "sha256:template",
		PageNo: 1, Nonce: "n",
	}
	notUUID := base
	notUUID.StudentID = "student-42"
	notUUID.SheetSerial = "serial"
	if _, err := keyring.Sign(notUUID); !errors.Is(err, ErrBarcodeInvalid) {
		t.Fatalf("non-UUID student_id must be rejected, got %v", err)
	}
	noSerial := base
	noSerial.StudentID = "44444444-4444-4444-8444-444444444444"
	if _, err := keyring.Sign(noSerial); !errors.Is(err, ErrBarcodeInvalid) {
		t.Fatalf("student claims without sheet_serial must be rejected, got %v", err)
	}
	invalidSerial := base
	invalidSerial.StudentID = "44444444-4444-4444-8444-444444444444"
	invalidSerial.SheetSerial = "serial-not-issued-by-edugrade"
	if _, err := keyring.Sign(invalidSerial); !errors.Is(err, ErrBarcodeInvalid) {
		t.Fatalf("non-UUID sheet_serial must be rejected, got %v", err)
	}
}

func TestVersionOnePayloadCannotSmuggleStudentIdentity(t *testing.T) {
	// 向第 1 版载荷添加学生字段的伪造内容必须校验失败；学生身份仅在第 2 版声明集合中受信任。
	forged := BarcodeClaims{
		Version: 1, KeyID: "2026-01",
		TenantID: "11111111-1111-4111-8111-111111111111", ExamID: "22222222-2222-4222-8222-222222222222",
		TemplateID: "33333333-3333-4333-8333-333333333333", TemplateContentHash: "sha256:template",
		PageNo: 1, Nonce: "n",
		StudentID: "44444444-4444-4444-8444-444444444444", SheetSerial: "serial",
	}
	if err := validateBarcodeClaims(forged); !errors.Is(err, ErrBarcodeInvalid) {
		t.Fatalf("version-1 claims with student identity must be rejected, got %v", err)
	}
}

func TestStudentBarcodeTamperRejection(t *testing.T) {
	keyring := BarcodeKeyring{ActiveKeyID: "2026-01", Keys: map[string][]byte{"2026-01": []byte(strings.Repeat("k", 32))}}
	value, err := keyring.Sign(BarcodeClaims{
		TenantID: "11111111-1111-4111-8111-111111111111", ExamID: "22222222-2222-4222-8222-222222222222",
		TemplateID: "33333333-3333-4333-8333-333333333333", TemplateContentHash: "sha256:template",
		PageNo: 1, Nonce: "n",
		StudentID: "44444444-4444-4444-8444-444444444444", SheetSerial: "55555555-5555-4555-8555-555555555555",
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(value, ".", 3)
	// 使用另一名学生重新编码载荷，模拟身份替换。
	swapped := strings.Replace(value, parts[1], parts[1][:len(parts[1])-4]+"AAAA", 1)
	if swapped == value {
		t.Fatal("test setup failed to alter payload")
	}
	if _, err := keyring.Verify(swapped); !errors.Is(err, ErrBarcodeInvalid) {
		t.Fatalf("altered student payload must fail signature check, got %v", err)
	}
}
