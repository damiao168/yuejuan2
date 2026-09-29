package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// 命令键只标识一次操作；哈希绑定实际创建内容，防止同键被复用于不同考试或批次参数。
func batchCommandHash(examID string, input CreateBatchInput) string {
	data, _ := json.Marshal(struct{ ExamID, Name, SourceType, ScannerDevice string }{examID, input.Name, input.SourceType, input.ScannerDevice})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

type BatchCommandRecovery struct {
	CommandID string `json:"command_id"`
	Status    string `json:"status"`
	Batch     *Batch `json:"batch,omitempty"`
}
