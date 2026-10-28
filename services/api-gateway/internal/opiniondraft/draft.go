// Package opiniondraft 保存教职工未提交的敏感意见；内容只按当前租户、本人和业务记录读取，不提供列表或导出。
package opiniondraft

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrConflict = errors.New("private opinion draft revision conflict")

type Draft struct {
	Source         string         `json:"source"`
	SourceRevision int64          `json:"source_revision"`
	Values         map[string]any `json:"values"`
	UpdatedAt      time.Time      `json:"updated_at"`
	ExpiresAt      time.Time      `json:"expires_at"`
}
type Record struct {
	Revision       int64  `json:"revision"`
	Draft          *Draft `json:"draft"`
	Source         string `json:"source"`
	SourceRevision int64  `json:"source_revision"`
}
type Input struct {
	Source           string         `json:"source"`
	SourceRevision   int64          `json:"source_revision"`
	ExpectedRevision int64          `json:"expected_revision"`
	Values           map[string]any `json:"values"`
}
type Store interface {
	Get(context.Context, string, string, string, string) (Record, error)
	Put(context.Context, string, string, string, string, int64, *Draft) (Record, error)
	Commit(context.Context, string, string, string, string, int64) error
}

// Endpoint 由业务处理器在核实当前资源权限后调用；source和sourceRevision必须来自服务端资源，不能相信请求。
func Endpoint(w http.ResponseWriter, r *http.Request, store Store, kind, source string, sourceRevision int64, editable bool) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, 401, "unauthenticated", "authentication required")
		return
	}
	if !editable {
		httpx.Error(w, r, 403, "private_draft_unavailable", "current record no longer accepts an opinion draft")
		return
	}
	if store == nil {
		httpx.Error(w, r, 503, "private_draft_unavailable", "private draft storage is unavailable")
		return
	}
	id := r.PathValue("id")
	var result Record
	var err error
	if r.Method == http.MethodGet {
		result, err = store.Get(r.Context(), user.TenantID, user.ID, kind, id)
	} else {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 40<<10))
		decoder.DisallowUnknownFields()
		if r.Method == http.MethodDelete {
			var body struct {
				ExpectedRevision int64 `json:"expected_revision"`
			}
			if decoder.Decode(&body) != nil || body.ExpectedRevision < 0 {
				httpx.Error(w, r, 400, "invalid_private_draft", "invalid private draft input")
				return
			}
			result, err = store.Put(r.Context(), user.TenantID, user.ID, kind, id, body.ExpectedRevision, nil)
		} else {
			var body Input
			if decoder.Decode(&body) != nil || body.ExpectedRevision < 0 || body.SourceRevision < 1 || body.Values == nil {
				httpx.Error(w, r, 400, "invalid_private_draft", "invalid private draft input")
				return
			}
			if body.Source != source || body.SourceRevision != sourceRevision {
				httpx.Error(w, r, 409, "private_draft_source_conflict", "record source or revision changed")
				return
			}
			if !validValues(kind, body.Values) {
				httpx.Error(w, r, 400, "invalid_private_draft", "private draft contains unsupported fields")
				return
			}
			now := time.Now().UTC()
			result, err = store.Put(r.Context(), user.TenantID, user.ID, kind, id, body.ExpectedRevision, &Draft{Source: source, SourceRevision: sourceRevision, Values: body.Values, UpdatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)})
		}
	}
	if errors.Is(err, ErrConflict) {
		httpx.Error(w, r, 409, "private_draft_conflict", "private draft changed in another page; reload before saving")
		return
	}
	if err != nil {
		httpx.Error(w, r, 503, "private_draft_unavailable", "private draft storage is unavailable")
		return
	}
	// 来源变化后不返回旧内容；同来源的新资源修订可由前端显式核对恢复，不能自动作为正式结论。
	if result.Draft != nil && (result.Draft.Source != source || !time.Now().Before(result.Draft.ExpiresAt)) {
		result.Draft = nil
	}
	result.Source = source
	result.SourceRevision = sourceRevision
	httpx.JSON(w, http.StatusOK, result)
}

func validValues(kind string, values map[string]any) bool {
	fields := map[string]map[string]bool{
		"question-appeal": {"publicResponse": true, "reviewOpinion": true, "suggestedScore": true, "reviewExplanation": true, "regradeJobID": true, "resolutionID": true},
		"legacy-appeal":   {"recommendation": true, "decision": true, "score": true, "reason": true},
		"arbitration":     {"finalScore": true, "reason": true, "studentFeedback": true},
	}[kind]
	if fields == nil || len(values) > len(fields) {
		return false
	}
	for key, value := range values {
		if !fields[key] {
			return false
		}
		if key == "score" || key == "suggestedScore" {
			if value == nil {
				continue
			}
			number, ok := value.(float64)
			if !ok || number < 0 {
				return false
			}
			continue
		}
		if key == "finalScore" {
			switch score := value.(type) {
			case nil:
			case float64:
				if score < 0 {
					return false
				}
			case string:
				if utf8.RuneCountInString(score) > 32 {
					return false
				}
			default:
				return false
			}
			continue
		}
		text, ok := value.(string)
		if !ok || utf8.RuneCountInString(text) > 4000 || strings.ContainsRune(text, '\x00') {
			return false
		}
	}
	raw, err := json.Marshal(values)
	return err == nil && len(raw) <= 32<<10
}

type MemoryStore struct {
	mu    sync.Mutex
	items map[string]Record
}

func NewMemoryStore() *MemoryStore  { return &MemoryStore{items: map[string]Record{}} }
func key(t, a, k, id string) string { return t + "\x00" + a + "\x00" + k + "\x00" + id }
func clone(item Record) Record {
	raw, _ := json.Marshal(item)
	var out Record
	_ = json.Unmarshal(raw, &out)
	return out
}
func (s *MemoryStore) Get(_ context.Context, t, a, k, id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.items[key(t, a, k, id)]), nil
}
func (s *MemoryStore) Put(_ context.Context, t, a, k, id string, expected int64, draft *Draft) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := key(t, a, k, id)
	old := s.items[identity]
	if old.Revision != expected {
		return Record{}, ErrConflict
	}
	item := Record{Revision: old.Revision + 1, Draft: draft}
	s.items[identity] = clone(item)
	return clone(item), nil
}
func (s *MemoryStore) Commit(_ context.Context, t, a, k, id string, sourceRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity := key(t, a, k, id)
	item := s.items[identity]
	if item.Draft != nil && item.Draft.SourceRevision <= sourceRevision {
		item.Revision++
		item.Draft = nil
		s.items[identity] = item
	}
	return nil
}

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }
func (s *PostgresStore) Get(ctx context.Context, t, a, k, id string) (Record, error) {
	var item Record
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision,draft_json FROM private_opinion_draft WHERE tenant_id=$1 AND actor_id=$2 AND kind=$3 AND record_id=$4`, t, a, k, id).Scan(&item.Revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return item, nil
	}
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &item.Draft)
	}
	return item, err
}
func (s *PostgresStore) Put(ctx context.Context, t, a, k, id string, expected int64, draft *Draft) (Record, error) {
	var raw any
	if draft != nil {
		encoded, err := json.Marshal(draft)
		if err != nil {
			return Record{}, err
		}
		raw = encoded
	}
	var revision int64
	// 删除保留修订墓碑：另一页面拿旧修订保存时必须冲突，不能把已正式提交或主动删除的意见重新写回。
	err := s.db.QueryRowContext(ctx, `INSERT INTO private_opinion_draft(tenant_id,actor_id,kind,record_id,revision,draft_json) SELECT $1,$2,$3,$4,1,$6::jsonb WHERE $5=0 OR EXISTS(SELECT 1 FROM private_opinion_draft WHERE tenant_id=$1 AND actor_id=$2 AND kind=$3 AND record_id=$4)
ON CONFLICT(tenant_id,actor_id,kind,record_id) DO UPDATE SET revision=private_opinion_draft.revision+1,draft_json=EXCLUDED.draft_json,updated_at=now() WHERE private_opinion_draft.revision=$5
RETURNING revision`, t, a, k, id, expected, raw).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrConflict
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23514" {
		return Record{}, ErrConflict
	}
	return Record{Revision: revision, Draft: draft}, err
}
func (s *PostgresStore) Commit(ctx context.Context, t, a, k, id string, sourceRevision int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE private_opinion_draft SET revision=revision+1,draft_json=NULL,updated_at=now() WHERE tenant_id=$1 AND actor_id=$2 AND kind=$3 AND record_id=$4 AND (draft_json->>'source_revision')::bigint<=$5`, t, a, k, id, sourceRevision)
	return err
}
