package review

import (
	"encoding/json"
	"fmt"
)

// decodeJSONB 将从 Postgres 扫描出的 jsonb 列值解码到 dst。
// 空输入视为“无值”并保持 dst 不变，保留此前 len(raw) > 0 检查的语义。
// 这些 jsonb 列仅由本应用写入，因此解码失败表示存储结构已与 Go 类型发生偏移；
// 必须显式返回错误，不能静默把目标清零。
func decodeJSONB(raw []byte, dst any, field string) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}
