package capture

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const controlledBarcodePrefix = "EG1"

var ErrBarcodeInvalid = errors.New("controlled barcode invalid")

type BarcodeClaims struct {
	Version             int    `json:"v"`
	KeyID               string `json:"kid"`
	TenantID            string `json:"tenant_id"`
	ExamID              string `json:"exam_id"`
	TemplateID          string `json:"template_id"`
	TemplateContentHash string `json:"template_content_hash"`
	PageNo              int    `json:"page_no"`
	Nonce               string `json:"nonce"`
	// Version 2 claims bind a printed sheet to a student before scanning, so
	// submission ownership comes from cryptographic identity instead of page
	// counting. StudentID is the student row UUID, never a school number, so
	// the barcode carries no directly identifying text. SheetSerial identifies
	// one physical printed sheet; the same serial observed across different
	// submissions signals a duplicate print or a scan mix-up and must go to
	// manual review instead of silent assignment.
	StudentID   string `json:"student_id,omitempty"`
	SheetSerial string `json:"sheet_serial,omitempty"`
}

// boundToStudent reports whether the claims carry student identity (version 2).
func (c BarcodeClaims) boundToStudent() bool {
	return c.StudentID != "" || c.SheetSerial != ""
}

type BarcodeKeyring struct {
	ActiveKeyID string
	Keys        map[string][]byte
}

func (k BarcodeKeyring) Sign(claims BarcodeClaims) (string, error) {
	// The claims payload is printed on physical sheets and cannot be recalled,
	// so the version is derived from content: template-level page barcodes stay
	// on the frozen version-1 layout, student-bound sheets use version 2.
	if claims.boundToStudent() {
		claims.Version = 2
	} else {
		claims.Version = 1
	}
	claims.KeyID = k.ActiveKeyID
	if err := validateBarcodeClaims(claims); err != nil {
		return "", err
	}
	key := k.Keys[claims.KeyID]
	if len(key) < 32 {
		return "", fmt.Errorf("%w: signing key unavailable", ErrBarcodeInvalid)
	}
	fields := map[string]any{
		"exam_id": claims.ExamID, "kid": claims.KeyID, "nonce": claims.Nonce,
		"page_no": claims.PageNo, "template_content_hash": claims.TemplateContentHash,
		"template_id": claims.TemplateID, "tenant_id": claims.TenantID, "v": claims.Version,
	}
	if claims.Version >= 2 {
		fields["student_id"] = claims.StudentID
		fields["sheet_serial"] = claims.SheetSerial
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signed := controlledBarcodePrefix + "." + encoded
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(signed))
	return signed + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (k BarcodeKeyring) Verify(value string) (BarcodeClaims, error) {
	var claims BarcodeClaims
	if len(value) > 2048 {
		return claims, fmt.Errorf("%w: value_too_long", ErrBarcodeInvalid)
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != controlledBarcodePrefix {
		return claims, fmt.Errorf("%w: format_invalid", ErrBarcodeInvalid)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return claims, fmt.Errorf("%w: payload_invalid", ErrBarcodeInvalid)
	}
	key := k.Keys[claims.KeyID]
	if len(key) < 32 {
		return claims, fmt.Errorf("%w: key_unknown", ErrBarcodeInvalid)
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return claims, fmt.Errorf("%w: signature_invalid", ErrBarcodeInvalid)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return claims, fmt.Errorf("%w: signature_invalid", ErrBarcodeInvalid)
	}
	if err = validateBarcodeClaims(claims); err != nil {
		return claims, err
	}
	return claims, nil
}

func validateBarcodeClaims(claims BarcodeClaims) error {
	if claims.Version != 1 && claims.Version != 2 {
		return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
	}
	if claims.KeyID == "" || claims.PageNo <= 0 || claims.Nonce == "" || claims.TemplateContentHash == "" {
		return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
	}
	for _, value := range []string{claims.TenantID, claims.ExamID, claims.TemplateID} {
		if _, err := uuid.Parse(value); err != nil {
			return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
		}
	}
	switch claims.Version {
	case 1:
		// Template-level barcodes must not smuggle student identity outside the
		// signed field set of their version.
		if claims.boundToStudent() {
			return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
		}
	case 2:
		if _, err := uuid.Parse(claims.StudentID); err != nil {
			return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
		}
		if claims.SheetSerial == "" || len(claims.SheetSerial) > 64 {
			return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
		}
	}
	return nil
}
