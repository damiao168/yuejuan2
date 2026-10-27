package ocr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
)

type completionIdentity struct {
	ModelVersion string
	ConfigHash   string
	InputHash    string
	DurationMS   int
	WorkerID     string
	Results      []string
}

func completionFingerprint(input CompleteTaskInput, storedPrecision bool) (string, error) {
	if len(input.Results) == 0 || input.DurationMS < 0 {
		return "", ErrInvalidInput
	}
	identity := completionIdentity{ModelVersion: input.ModelVersion, ConfigHash: input.ConfigHash, InputHash: input.InputHash, DurationMS: input.DurationMS, WorkerID: input.WorkerID, Results: make([]string, 0, len(input.Results))}
	for _, result := range input.Results {
		if err := ValidateResultInput(result); err != nil {
			return "", err
		}
		if storedPrecision {
			result.Confidence = math.Round(result.Confidence*10000) / 10000
		}
		encoded, err := json.Marshal(struct {
			Result            ResultInput
			PreprocessProfile string
		}{result, input.PreprocessProfile})
		if err != nil {
			return "", ErrInvalidInput
		}
		identity.Results = append(identity.Results, string(encoded))
	}
	// 数据库同事务时间戳相同，随机UUID不能表达输入顺序；排序保留重复项数量，不把集合去重。
	sort.Strings(identity.Results)
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", ErrInvalidInput
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func sameCompletion(task Task, input CompleteTaskInput) bool {
	fingerprint, err := completionFingerprint(input, task.CompletionPayloadHash == "")
	if err != nil {
		return false
	}
	if task.CompletionPayloadHash != "" {
		return fingerprint == task.CompletionPayloadHash
	}
	// 升级前没有原始指纹，只能比较历史实际保存的精度；不能恢复亚精度差异。
	previous := CompleteTaskInput{ModelVersion: task.ModelVersion, ConfigHash: task.ConfigHash, InputHash: task.InputHash, DurationMS: task.DurationMS, WorkerID: task.WorkerID}
	for _, result := range task.Results {
		if len(previous.Results) > 0 && previous.PreprocessProfile != result.PreprocessProfile {
			return false
		}
		previous.PreprocessProfile = result.PreprocessProfile
		previous.Results = append(previous.Results, ResultInput{SubmissionPageID: result.SubmissionPageID, Text: result.Text, BBox: result.BBox, Confidence: result.Confidence, SourceImageFileID: result.SourceImageFileID})
	}
	previousFingerprint, err := completionFingerprint(previous, true)
	return err == nil && fingerprint == previousFingerprint
}
