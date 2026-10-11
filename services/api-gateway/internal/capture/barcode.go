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
	// 第 2 版声明在扫描前将印刷答题纸绑定到学生，使提交归属来自加密身份而非页数统计。
	// StudentID 是学生记录 UUID，不是学号，因此条码不含可直接识别身份的文本。SheetSerial 标识一张实体答题纸；
	// 不同提交出现同一序列号时，表示重复印刷或扫描混淆，必须转人工复核，不能静默分配。
	StudentID   string `json:"student_id,omitempty"`
	SheetSerial string `json:"sheet_serial,omitempty"`
}

// boundToStudent 判断声明是否包含学生身份（第 2 版）。
func (c BarcodeClaims) boundToStudent() bool {
	return c.StudentID != "" || c.SheetSerial != ""
}

type BarcodeKeyring struct {
	ActiveKeyID string
	Keys        map[string][]byte
}

func (k BarcodeKeyring) Sign(claims BarcodeClaims) (string, error) {
	// 声明载荷已印在实体答题纸上，无法收回，因此版本根据内容派生：模板级页面条码保持冻结的第 1 版布局，绑定学生的答题纸使用第 2 版。
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
		// 模板级条码不得在其版本的签名字段集合之外夹带学生身份。
		if claims.boundToStudent() {
			return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
		}
	case 2:
		if _, err := uuid.Parse(claims.StudentID); err != nil {
			return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
		}
		if _, err := uuid.Parse(claims.SheetSerial); err != nil {
			return fmt.Errorf("%w: claims_invalid", ErrBarcodeInvalid)
		}
	}
	return nil
}
