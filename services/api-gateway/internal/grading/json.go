package grading

import (
	"encoding/json"
	"fmt"
)

// decodeJSONB 将从 Postgres 读取的 jsonb 列解码到 dst。
// 空输入视为无值，不修改 dst，保留原有 len(raw) > 0 检查的语义。这些 jsonb 列仅由本应用写入，解码失败表示存储结构与 Go 类型发生偏离；
// 必须显式返回错误，不能静默清零目标。
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
