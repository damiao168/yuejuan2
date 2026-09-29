package workerruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalResultJSON 返回用于计算结果 payload 哈希的确定性 JSON 封装；encoding/json 会排序 map 键，
// 因而语义相同的结果 map 会生成相同字节。
func CanonicalResultJSON(schema string, result map[string]any) ([]byte, error) {
	return json.Marshal(struct {
		Schema  string         `json:"schema"`
		Payload map[string]any `json:"payload"`
	}{
		Schema:  schema,
		Payload: result,
	})
}

// ResultPayloadHash 返回用于幂等完成检查和可信导入的规范 SHA-256 哈希。
func ResultPayloadHash(schema string, result map[string]any) (string, error) {
	raw, err := CanonicalResultJSON(schema, result)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func payloadHash(schema string, payload map[string]any) string {
	hash, _ := ResultPayloadHash(schema, payload)
	return hash
}
