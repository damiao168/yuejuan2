package grading

import (
	"encoding/json"
	"fmt"
)

// decodeJSONB decodes a jsonb column value scanned from Postgres into dst.
// Empty input is treated as "no value" and leaves dst untouched, preserving
// the semantics of the previous len(raw) > 0 guards. These jsonb columns are
// written exclusively by this application, so a decode failure means the
// stored structure has drifted from the Go types; it must surface as an
// explicit error instead of silently zeroing the destination.
func decodeJSONB(raw []byte, dst any, field string) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}

// decodeJSONBLenient 用于没有写入侧 schema 保证的展示字段。
// 它忽略解码错误且不清空目标；类型不匹配时可能留下部分字段，调用方不能把结果当作完整几何数据。
// 单条记录的异常只应降级该行展示，不能让整场成绩列表查询失败。
func decodeJSONBLenient(raw []byte, dst any) {
	if len(raw) == 0 {
		return
	}
	_ = json.Unmarshal(raw, dst)
}
