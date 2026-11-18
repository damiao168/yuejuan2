package schoolassistant

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

const (
	webSearchActionMaxAnswerRunes = 8_000
	webSearchActionMaxClaims      = 8
	webSearchActionMaxClaimRunes  = 300
	webSearchActionMaxClaimTotal  = 2_400
	webSearchActionMaxQuoteTotal  = 4_000
	webSearchMaxPrefetchReads     = 2
)

type webSearchCoveredClaim struct {
	Claim     string   `json:"claim"`
	SourceIDs []string `json:"source_ids"`
}

// webSearchAction 将下一步与本轮证据放在同一响应，避免为选页、覆盖判断另开固定模型轮次。
// 覆盖清单只能关联本轮已核实的原文来源；程序校验归属，不把模型的语义判断当成事实保证。
type webSearchAction struct {
	Action            string                  `json:"action"`
	Question          string                  `json:"question,omitempty"`
	Query             string                  `json:"query,omitempty"`
	AdditionalQueries []string                `json:"additional_queries,omitempty"`
	ReadTopResults    int                     `json:"read_top_results,omitempty"`
	SourceIDs         []string                `json:"source_ids,omitempty"`
	InformationNeeded string                  `json:"information_needed,omitempty"`
	Answer            string                  `json:"answer,omitempty"`
	Covered           []webSearchCoveredClaim `json:"covered,omitempty"`
	Missing           []string                `json:"missing,omitempty"`
	Evidence          []webSearchEvidence     `json:"evidence,omitempty"`
}

// decodeWebSearchAction 只解码受限公开动作，不执行搜索、读取或校内授权。
// 循环位置、重复来源和整轮预算仍由编排层检查；remainingReads 必须来自服务端已发生的读取计数。
func decodeWebSearchAction(response modelgovernance.AgentResponse, candidates []Source, remainingReads int) (webSearchAction, error) {
	invalid := func() (webSearchAction, error) {
		return webSearchAction{}, reject(502, "invalid_web_search_action", "模型处理联网查询时返回异常，请重试或切换模型。")
	}
	var action webSearchAction
	if len(response.Message.ToolCalls) != 0 || decodeStrictWebSearchObject(response.Message.Content, &action) != nil {
		return invalid()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(response.Message.Content), &fields) != nil || !validWebSearchActionFields(action.Action, fields) {
		return invalid()
	}
	action.Question = strings.TrimSpace(action.Question)
	action.Query = strings.TrimSpace(action.Query)
	action.InformationNeeded = strings.TrimSpace(action.InformationNeeded)
	var valid bool
	switch action.Action {
	case "search":
		valid = validateWebSearchQueries(&action, fields["question"] != nil, remainingReads)
	case "read":
		valid = validWebSearchRead(action, webSearchCandidatesByID(candidates), remainingReads)
	case "answer":
		valid = validateWebSearchAnswer(&action, fields, webSearchCandidatesByID(candidates))
	case "clarify":
		valid = strings.TrimSpace(action.Answer) != "" && utf8.RuneCountInString(action.Answer) <= 1000
	case "business":
		valid = true
	}
	if !valid {
		return invalid()
	}
	return action, nil
}

func webSearchActionFields(action string) (required, optional []string, ok bool) {
	switch action {
	case "search":
		return []string{"query"}, []string{"information_needed", "question", "additional_queries", "read_top_results"}, true
	case "read":
		return []string{"source_ids", "information_needed"}, nil, true
	case "answer":
		return []string{"answer", "covered", "missing", "evidence"}, nil, true
	case "clarify":
		return []string{"answer"}, nil, true
	case "business":
		return nil, nil, true
	default:
		return nil, nil, false
	}
}

func validWebSearchActionFields(action string, fields map[string]json.RawMessage) bool {
	required, optional, ok := webSearchActionFields(action)
	if !ok || fields["action"] == nil {
		return false
	}
	// encoding/json 对字段大小写兼容；这里按协议拒绝其他键名和显式 null。
	for field, value := range fields {
		if strings.TrimSpace(string(value)) == "null" {
			return false
		}
		if field != "action" && !slices.Contains(required, field) && !slices.Contains(optional, field) {
			return false
		}
	}
	for _, field := range required {
		if fields[field] == nil {
			return false
		}
	}
	return true
}

func validateWebSearchQueries(action *webSearchAction, hasQuestion bool, remainingReads int) bool {
	// 模型可把选页合并到搜索动作；仍按本轮剩余读取额度校验，不能额外增加网络请求。
	if action.ReadTopResults < 0 || action.ReadTopResults > webSearchMaxPrefetchReads || action.ReadTopResults > 0 && action.ReadTopResults > remainingReads {
		return false
	}
	if action.Query == "" || utf8.RuneCountInString(action.Query) > webSearchMaxQueryRunes || utf8.RuneCountInString(action.InformationNeeded) > 1000 {
		return false
	}
	if hasQuestion && (action.Question == "" || utf8.RuneCountInString(action.Question) > 1000) {
		return false
	}
	// 空数组和省略都表示没有补充查询；单次动作最多补一条。
	if len(action.AdditionalQueries) == 0 {
		return true
	}
	if len(action.AdditionalQueries) > 1 {
		return false
	}
	query := strings.TrimSpace(action.AdditionalQueries[0])
	if query == "" || utf8.RuneCountInString(query) > webSearchMaxQueryRunes || publicSearchQueryKey(query) == publicSearchQueryKey(action.Query) {
		return false
	}
	action.AdditionalQueries[0] = query
	return true
}

func webSearchCandidatesByID(candidates []Source) map[string]Source {
	byID := make(map[string]Source, len(candidates))
	for _, source := range candidates {
		byID[source.ID] = source
	}
	return byID
}

func validWebSearchRead(action webSearchAction, byID map[string]Source, remainingReads int) bool {
	if action.InformationNeeded == "" || utf8.RuneCountInString(action.InformationNeeded) > 1000 || len(action.SourceIDs) == 0 || len(action.SourceIDs) > min(remainingReads, webSearchMaxPageReads) {
		return false
	}
	seen := make(map[string]bool)
	for _, id := range action.SourceIDs {
		if _, exists := byID[id]; id == "" || !exists || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func validateWebSearchAnswer(action *webSearchAction, fields map[string]json.RawMessage, byID map[string]Source) bool {
	// Answer 可在整对象验证前增量显示；只判断空白，不能修剪已显示正文而改变最终前缀。
	if strings.TrimSpace(action.Answer) == "" || utf8.RuneCountInString(action.Answer) > webSearchActionMaxAnswerRunes || action.Covered == nil || action.Missing == nil || action.Evidence == nil {
		return false
	}
	if len(action.Evidence) > webSearchMaxResults || len(action.Covered)+len(action.Missing) > webSearchActionMaxClaims || (len(action.Evidence) > 0 && len(action.Covered) == 0) {
		return false
	}
	quotedIDs, ok := validateWebSearchActionEvidence(action.Evidence, fields["evidence"], byID)
	return ok && validateWebSearchActionClaims(action, fields["covered"], quotedIDs)
}

func validateWebSearchActionEvidence(evidenceList []webSearchEvidence, raw json.RawMessage, byID map[string]Source) (map[string]bool, bool) {
	var evidenceFields []map[string]json.RawMessage
	if json.Unmarshal(raw, &evidenceFields) != nil || len(evidenceFields) != len(evidenceList) {
		return nil, false
	}
	quotedIDs, quoteTotal := make(map[string]bool), 0
	for index, evidence := range evidenceList {
		entry := evidenceFields[index]
		if len(entry) != 2 || entry["source_id"] == nil || (entry["quote"] == nil) == (entry["quotes"] == nil) {
			return nil, false
		}
		source, exists := byID[evidence.SourceID]
		if evidence.SourceID == "" || !exists || quotedIDs[evidence.SourceID] || !validWebSearchQuotes(evidence, source.Excerpt) {
			return nil, false
		}
		quoteTotal += utf8.RuneCountInString(evidence.Quote)
		for _, quote := range evidence.Quotes {
			quoteTotal += utf8.RuneCountInString(quote)
		}
		if quoteTotal > webSearchActionMaxQuoteTotal {
			return nil, false
		}
		quotedIDs[evidence.SourceID] = true
	}
	return quotedIDs, true
}

func validateWebSearchActionClaims(action *webSearchAction, raw json.RawMessage, quotedIDs map[string]bool) bool {
	var coveredFields []map[string]json.RawMessage
	if json.Unmarshal(raw, &coveredFields) != nil || len(coveredFields) != len(action.Covered) {
		return false
	}
	seenClaims, coveredIDs, claimTotal := make(map[string]bool), make(map[string]bool), 0
	for index := range action.Covered {
		claim := &action.Covered[index]
		entry := coveredFields[index]
		claim.Claim = strings.TrimSpace(claim.Claim)
		length := utf8.RuneCountInString(claim.Claim)
		if len(entry) != 2 || entry["claim"] == nil || entry["source_ids"] == nil || length == 0 || length > webSearchActionMaxClaimRunes || seenClaims[claim.Claim] {
			return false
		}
		claimTotal += length
		seenClaims[claim.Claim] = true
		if !markWebSearchClaimSources(claim.SourceIDs, quotedIDs, coveredIDs) {
			return false
		}
	}
	// 已关联的来源都经过引文验证；数量相等表示每条引文都关联到至少一项声明。
	if len(coveredIDs) != len(quotedIDs) {
		return false
	}
	for index, missing := range action.Missing {
		missing = strings.TrimSpace(missing)
		length := utf8.RuneCountInString(missing)
		if length == 0 || length > webSearchActionMaxClaimRunes || seenClaims[missing] {
			return false
		}
		claimTotal += length
		seenClaims[missing], action.Missing[index] = true, missing
	}
	return claimTotal <= webSearchActionMaxClaimTotal
}

func markWebSearchClaimSources(ids []string, quotedIDs, coveredIDs map[string]bool) bool {
	if len(ids) == 0 || len(ids) > webSearchMaxResults {
		return false
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		if !quotedIDs[id] || seen[id] {
			return false
		}
		seen[id], coveredIDs[id] = true, true
	}
	return true
}
