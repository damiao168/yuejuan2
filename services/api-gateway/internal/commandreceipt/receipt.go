// Package commandreceipt persists command identity inside the caller's business
// transaction. It is independent of the expiring HTTP response cache.
package commandreceipt

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrConflict = errors.New("business command request conflict")

// TransportProcessingTimeout is the maximum quiet period before a synchronous
// command reservation is considered abandoned. The recovery GET remains
// read-only; a client must POST the frozen request again and let the
// idempotency store acquire the reservation with a conditional update.
const TransportProcessingTimeout = 2 * time.Minute

type contextKey struct{}

func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}
func ID(ctx context.Context) string { id, _ := ctx.Value(contextKey{}).(string); return id }

type Receipt struct {
	CommandID  string          `json:"command_id"`
	Status     string          `json:"status"`
	HTTPStatus int             `json:"http_status,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	Operation  string          `json:"operation,omitempty"`
	TargetID   string          `json:"target_id,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

func fingerprint(operation, target string, input any) (string, error) {
	raw, err := json.Marshal(struct {
		Operation, Target string
		Input             any
	}{operation, target, input})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Load 在业务事务内锁定命令身份并读取原结果；同一命令键改变输入时返回 ErrConflict。
// 调用方必须在同一事务内完成业务写入和 Save，确保结果回执与业务事实一起提交。
func Load(ctx context.Context, tx *sql.Tx, tenant, actor, operation, target string, input any, out any) (bool, error) {
	id := ID(ctx)
	if id == "" {
		return false, nil
	}
	if len(id) > 128 {
		return false, ErrConflict
	}
	hash, err := fingerprint(operation, target, commandInput(ctx, input))
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenant+":"+actor+":"+id+":business-command"); err != nil {
		return false, err
	}
	var existing string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM business_command_receipt WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND command_id=$3`, tenant, actor, id).Scan(&existing, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hash != existing {
		return false, ErrConflict
	}
	return true, json.Unmarshal(raw, out)
}
func Save(ctx context.Context, tx *sql.Tx, tenant, actor, operation, target string, input, result any) error {
	id := ID(ctx)
	if id == "" {
		return nil
	}
	hash, err := fingerprint(operation, target, commandInput(ctx, input))
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO business_command_receipt(tenant_id,actor_id,command_id,operation,target_id,request_hash,result) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7::jsonb)`, tenant, actor, id, operation, target, hash, raw)
	return err
}
func Recover(ctx context.Context, db *sql.DB, tenant, actor, id, prefix string) (Receipt, error) {
	result := Receipt{CommandID: id, Status: "not_accepted"}
	err := db.QueryRowContext(ctx, `SELECT operation,target_id,result FROM business_command_receipt WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND command_id=$3`, tenant, actor, id).Scan(&result.Operation, &result.TargetID, &result.Result)
	if errors.Is(err, sql.ErrNoRows) {
		return recoverTransportReceipt(ctx, db, result, tenant, actor, prefix)
	}
	if err != nil {
		return result, err
	}
	if !strings.HasPrefix(result.Operation, prefix) {
		return Receipt{CommandID: id, Status: "not_accepted"}, nil
	}
	result.Status = "succeeded"
	return result, nil
}

func recoverTransportReceipt(ctx context.Context, db *sql.DB, result Receipt, tenant, actor, prefix string) (Receipt, error) {
	allowed := transportRoutes(prefix)
	if len(allowed) == 0 {
		return result, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT route,state,COALESCE(request_body,''::bytea),COALESCE(response_status,0),COALESCE(response_body,''::bytea),updated_at
FROM idempotency_record
WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND method='POST' AND idempotency_key=$3
ORDER BY updated_at DESC`, tenant, actor, result.CommandID)
	if err != nil {
		return Receipt{}, err
	}
	defer rows.Close()
	type transportRecord struct {
		state       string
		requestBody []byte
		httpStatus  int
		body        []byte
		updatedAt   time.Time
	}
	var matches []transportRecord
	for rows.Next() {
		var route string
		var record transportRecord
		if err := rows.Scan(&route, &record.state, &record.requestBody, &record.httpStatus, &record.body, &record.updatedAt); err != nil {
			return Receipt{}, err
		}
		// net/http ServeMux exposes patterns as "POST /path" while older
		// fixtures and records stored only "/path". Method is already filtered
		// by the query, so accept both exact representations.
		if allowed[strings.TrimPrefix(route, "POST ")] {
			matches = append(matches, record)
		}
	}
	if err := rows.Err(); err != nil {
		return Receipt{}, err
	}
	if len(matches) == 0 {
		return result, nil
	}
	if len(matches) > 1 {
		result.Status = "unknown"
		result.ErrorCode = "ambiguous_command_identity"
		return result, nil
	}
	record := matches[0]
	result.HTTPStatus = record.httpStatus
	switch {
	case record.state == "processing" && record.updatedAt.Before(time.Now().UTC().Add(-TransportProcessingTimeout)):
		if len(record.requestBody) == 0 || !json.Valid(record.requestBody) {
			result.Status = "unknown"
			result.ErrorCode = "business_command_payload_missing"
			break
		}
		result.Status = "takeover_ready"
		result.Payload = append(json.RawMessage(nil), record.requestBody...)
	case record.state == "processing":
		result.Status = "processing"
	case record.state == "completed" && record.httpStatus >= 400:
		result.Status = "rejected"
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(record.body, &envelope) == nil {
			result.ErrorCode = envelope.Error.Code
		}
	default:
		// A completed success transport receipt without the transactional
		// business receipt is an integrity anomaly. It is never permission
		// to submit the operation again.
		result.Status = "unknown"
		result.ErrorCode = "business_receipt_missing"
	}
	return result, nil
}

func transportRoutes(prefix string) map[string]bool {
	switch prefix {
	case "review.":
		return map[string]bool{
			"/api/v1/review-tasks/{id}/submit":      true,
			"/api/v1/arbitration-tasks/{id}/submit": true,
		}
	case "score.":
		return map[string]bool{
			"/api/v1/exams/{examId}/confirm-grades": true,
			"/api/v1/exams/{examId}/publish":        true,
		}
	case "report.":
		return map[string]bool{
			"/api/v1/exams/{examId}/reports/export": true,
		}
	default:
		return nil
	}
}

type memoryEntry struct {
	hash    string
	receipt Receipt
}
type Memory struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
}

func (m *Memory) Load(ctx context.Context, tenant, actor, operation, target string, input, out any) (bool, error) {
	if ID(ctx) == "" {
		return false, nil
	}
	hash, err := fingerprint(operation, target, commandInput(ctx, input))
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[tenant+":"+actor+":"+ID(ctx)]
	if !ok {
		return false, nil
	}
	if hash != entry.hash {
		return false, ErrConflict
	}
	return true, json.Unmarshal(entry.receipt.Result, out)
}
func (m *Memory) Save(ctx context.Context, tenant, actor, operation, target string, input, result any) error {
	if ID(ctx) == "" {
		return nil
	}
	hash, err := fingerprint(operation, target, commandInput(ctx, input))
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]memoryEntry{}
	}
	m.entries[tenant+":"+actor+":"+ID(ctx)] = memoryEntry{hash, Receipt{CommandID: ID(ctx), Status: "succeeded", Operation: operation, TargetID: target, Result: raw}}
	return nil
}
func (m *Memory) Recover(_ context.Context, tenant, actor, id string) (Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[tenant+":"+actor+":"+id]
	if !ok {
		return Receipt{CommandID: id, Status: "not_accepted"}, nil
	}
	result := entry.receipt
	result.Result = append(json.RawMessage(nil), result.Result...)
	return result, nil
}

type inputContextKey struct{}

// WithInput 保存请求的原始输入，后续补全字段不应改变用于重试核对的命令指纹。
func WithInput(ctx context.Context, input any) context.Context {
	return context.WithValue(ctx, inputContextKey{}, input)
}
func commandInput(ctx context.Context, input any) any {
	if original := ctx.Value(inputContextKey{}); original != nil {
		return original
	}
	return input
}
