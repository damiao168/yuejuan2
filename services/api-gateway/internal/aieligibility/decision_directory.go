package aieligibility

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

// 目录仅列冻结决策的维度和结果；详细输入与原因仍通过原来的单条查询读取。
type DecisionSummary struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenant_id"`
	RunItemID         string    `json:"run_item_id"`
	SubjectCode       string    `json:"subject_code"`
	EducationStage    string    `json:"education_stage"`
	ArchetypeCode     string    `json:"archetype_code"`
	RiskTier          string    `json:"risk_tier"`
	Decision          string    `json:"decision"`
	ExternalAIAllowed bool      `json:"external_ai_allowed"`
	CreatedAt         time.Time `json:"created_at"`
}

type DecisionFilter struct {
	Query string
	Limit int
	After pagination.Cursor
}

type DecisionPage struct {
	Decisions  []DecisionSummary `json:"decisions"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

func (h *Handler) ListDecisions(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authenticationRequired)
		return
	}
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 30, 100)
	if err != nil {
		writeError(w, r, ErrInvalidInput)
		return
	}
	after, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, r, ErrInvalidInput)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(query) > 128 || strings.ContainsRune(query, '\x00') {
		writeError(w, r, ErrInvalidInput)
		return
	}
	// 沿用路由层已核实的学校上下文，不能以查询参数另行扩展租户范围。
	items, err := h.service.store.ListDecisions(r.Context(), user.TenantID, DecisionFilter{Query: query, Limit: limit + 1, After: after})
	if err != nil {
		writeError(w, r, err)
		return
	}
	page := DecisionPage{Decisions: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Decisions = items[:limit]
		last := page.Decisions[limit-1]
		page.NextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, page)
}

func decisionSummary(item Decision) DecisionSummary {
	field := func(key string) string {
		value := item.InputSnapshot[key]
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
	return DecisionSummary{ID: item.ID, TenantID: item.TenantID, RunItemID: item.RunItemID, SubjectCode: field("subject_code"), EducationStage: field("education_stage"), ArchetypeCode: field("archetype_code"), RiskTier: field("risk_tier"), Decision: string(item.Decision), ExternalAIAllowed: item.ExternalAIAllowed, CreatedAt: item.CreatedAt}
}

func decisionSearchTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	terms := []string{query}
	// 目录显示中文名称；中文查询须映射到冻结快照里的规范代码，仍由服务端在分页前筛选。
	aliases := map[string]string{"chinese": "语文", "mathematics": "数学", "english": "英语", "physics": "物理", "chemistry": "化学", "biology": "生物", "history": "历史", "geography": "地理", "ethics_politics": "道德与法治思想政治", "junior": "初中", "senior": "高中", "selected_response": "选择题", "exact_text": "精确文本作答", "numeric_expression": "数值作答", "structured_steps": "步骤题", "short_constructed": "简答题", "extended_response": "长作答", "diagram_graph": "图表题", "table_experiment": "表格实验题", "rule_auto": "规则自动", "ai_assist": "AI 建议", "ai_fast_confirm": "AI 快速确认", "human_primary": "人工主评", "dual_human": "双人阅卷", "manual_only": "仅人工"}
	if query != "" {
		for code, label := range aliases {
			if strings.Contains(strings.ToLower(label), query) {
				terms = append(terms, code)
			}
		}
	}
	return terms
}

func (s *MemoryStore) ListDecisions(_ context.Context, tenantID string, filter DecisionFilter) ([]DecisionSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []DecisionSummary{}
	for _, decision := range s.decisions {
		if decision.TenantID != tenantID {
			continue
		}
		item := decisionSummary(decision)
		text := strings.Join([]string{item.RunItemID, item.SubjectCode, item.EducationStage, item.ArchetypeCode, item.RiskTier, item.Decision}, " ")
		matches := false
		for _, term := range decisionSearchTerms(filter.Query) {
			if strings.Contains(strings.ToLower(text), term) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if !filter.After.CreatedAt.IsZero() && (item.CreatedAt.After(filter.After.CreatedAt) || item.CreatedAt.Equal(filter.After.CreatedAt) && item.ID >= filter.After.ID) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *PostgresStore) ListDecisions(ctx context.Context, tenantID string, filter DecisionFilter) ([]DecisionSummary, error) {
	var after any
	if !filter.After.CreatedAt.IsZero() {
		after = filter.After.CreatedAt
	}
	// 先按租户、关键词和游标筛选，再取一页；关键词不检索隐藏输入，也不把%或_解释为通配符。
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,tenant_id::text,run_item_id,
COALESCE(input_snapshot_json->>'subject_code',''),COALESCE(input_snapshot_json->>'education_stage',''),COALESCE(input_snapshot_json->>'archetype_code',''),COALESCE(input_snapshot_json->>'risk_tier',''),decision,external_ai_allowed,created_at
FROM ai_eligibility_decision WHERE tenant_id=$1
AND EXISTS(SELECT 1 FROM unnest($2::text[]) term WHERE strpos(lower(concat_ws(' ',run_item_id,input_snapshot_json->>'subject_code',input_snapshot_json->>'education_stage',input_snapshot_json->>'archetype_code',input_snapshot_json->>'risk_tier',decision)),term)>0)
AND ($3::timestamptz IS NULL OR (created_at,id::text)<($3::timestamptz,$4))
ORDER BY created_at DESC,id::text DESC LIMIT $5`, tenantID, decisionSearchTerms(filter.Query), after, filter.After.ID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DecisionSummary{}
	for rows.Next() {
		var item DecisionSummary
		if err := rows.Scan(&item.ID, &item.TenantID, &item.RunItemID, &item.SubjectCode, &item.EducationStage, &item.ArchetypeCode, &item.RiskTier, &item.Decision, &item.ExternalAIAllowed, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
