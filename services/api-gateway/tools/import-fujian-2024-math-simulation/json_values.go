package main

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func nestedMap(value map[string]any, key string) map[string]any {
	item, _ := value[key].(map[string]any)
	return item
}

func nestedStringField(value map[string]any, nested, key string) string {
	return stringField(nestedMap(value, nested), key)
}

func stringField(value map[string]any, key string) string {
	item, _ := value[key].(string)
	return item
}

func boolField(value map[string]any, key string) bool {
	item, _ := value[key].(bool)
	return item
}

func intField(value map[string]any, key string) int {
	switch item := value[key].(type) {
	case float64:
		return int(item)
	case int:
		return item
	default:
		return 0
	}
}

func numberField(value map[string]any, key string) float64 {
	switch item := value[key].(type) {
	case float64:
		return item
	case int:
		return float64(item)
	default:
		return 0
	}
}

func sliceOfMaps(value any) []map[string]any {
	raw, _ := value.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
}
