package grading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
)

var ErrCommandConflict = errors.New("scoring command input conflict")

type ScoringCommandRecovery struct {
	CommandID string      `json:"command_id"`
	Status    string      `json:"status"`
	Run       *ScoringRun `json:"scoring_run,omitempty"`
}

func scoringCommandHash(examID, segmentID string) string {
	sum := sha256.Sum256([]byte("scoring-run\x00" + examID + "\x00" + segmentID))
	return hex.EncodeToString(sum[:])
}

func (s *PostgresStore) RecoverScoringCommand(ctx context.Context, tenantID, examID, actorID, commandID string) (ScoringCommandRecovery, error) {
	commandID = strings.TrimSpace(commandID)
	result := ScoringCommandRecovery{CommandID: commandID, Status: "not_accepted"}
	if commandID == "" || len(commandID) > 160 {
		return result, ErrInvalidInput
	}
	run, err := scanScoringRun(s.db.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND started_by=$3::uuid AND idempotency_key=$4`, tenantID, examID, actorID, commandID))
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	// succeeded 表示启动命令已被接受；实际评分进度仍看返回 run 的状态，不能据此显示阅卷完成。
	result.Status = "succeeded"
	result.Run = &run
	return result, nil
}
