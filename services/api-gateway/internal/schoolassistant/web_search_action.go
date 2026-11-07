package schoolassistant

import (
	"encoding/json"
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
	if json.Unmarshal([]byte(response.Message.Content), &fields) != nil || fields["action"] == nil {
		return invalid()
	}
	// encoding/json 对字段大小写兼容；动作协议固定小写键，不能靠兼容规则接受另一套结构。
	allowed := map[string]bool{"action": true}
	var required []string
	switch action.Action {
	case "search":
		allowed["query"], allowed["information_needed"], allowed["question"], allowed["additional_queries"] = true, true, true, true
		allowed["read_top_results"] = true
		required = []string{"query"}
	case "read":
		allowed["source_ids"], allowed["information_needed"] = true, true
		required = []string{"source_ids", "information_needed"}
	case "answer":
		allowed["answer"], allowed["covered"], allowed["missing"], allowed["evidence"] = true, true, true, true
		required = []string{"answer", "covered", "missing", "evidence"}
	case "clarify":
		allowed["answer"] = true
		required = []string{"answer"}
	case "business":
	default:
		return invalid()
	}
	for field, value := range fields {
		if !allowed[field] || strings.TrimSpace(string(value)) == "null" {
			return invalid()
		}
	}
	for _, field := range required {
		if fields[field] == nil {
			return invalid()
		}
	}
	action.Question = strings.TrimSpace(action.Question)
	action.Query = strings.TrimSpace(action.Query)
	action.InformationNeeded = strings.TrimSpace(action.InformationNeeded)
	if action.Action == "search" {
		// 模型可把选页合并到搜索动作；仍按本轮剩余读取额度校验，不能额外增加网络请求。
		if action.ReadTopResults < 0 || action.ReadTopResults > webSearchMaxPrefetchReads || action.ReadTopResults > 0 && action.ReadTopResults > remainingReads {
			return invalid()
		}
		if action.Query == "" || utf8.RuneCountInString(action.Query) > webSearchMaxQueryRunes || utf8.RuneCountInString(action.InformationNeeded) > 1000 || (fields["question"] != nil && (action.Question == "" || utf8.RuneCountInString(action.Question) > 1000)) {
			return invalid()
		}
		// 空数组和省略都表示没有补充查询；null已在字段检查中拒绝，不能借数组扩充整轮额度。
		if len(action.AdditionalQueries) > 1 {
			return invalid()
		}
		if len(action.AdditionalQueries) == 1 {
			primaryKey := strings.ToLower(strings.Join(strings.Fields(action.Query), " "))
			query := strings.TrimSpace(action.AdditionalQueries[0])
			if query == "" || utf8.RuneCountInString(query) > webSearchMaxQueryRunes || strings.ToLower(strings.Join(strings.Fields(query), " ")) == primaryKey {
				return invalid()
			}
			action.AdditionalQueries[0] = query
		}
	}
	if action.Action == "clarify" && (strings.TrimSpace(action.Answer) == "" || utf8.RuneCountInString(action.Answer) > 1000) {
		return invalid()
	}
	// Answer 可在整对象验证前增量显示；只判断空白，不能修剪已显示正文而改变最终前缀。
	if action.Action == "answer" && (strings.TrimSpace(action.Answer) == "" || utf8.RuneCountInString(action.Answer) > webSearchActionMaxAnswerRunes || action.Covered == nil || action.Missing == nil || action.Evidence == nil) {
		return invalid()
	}
	byID := make(map[string]Source, len(candidates))
	for _, source := range candidates {
		byID[source.ID] = source
	}
	if action.Action == "read" {
		if remainingReads > webSearchMaxPageReads {
			remainingReads = webSearchMaxPageReads
		}
		if action.InformationNeeded == "" || utf8.RuneCountInString(action.InformationNeeded) > 1000 || len(action.SourceIDs) == 0 || len(action.SourceIDs) > remainingReads {
			return invalid()
		}
		seen := make(map[string]bool)
		for _, id := range action.SourceIDs {
			if _, exists := byID[id]; id == "" || !exists || seen[id] {
				return invalid()
			}
			seen[id] = true
		}
	}
	if action.Action != "answer" {
		return action, nil
	}
	if len(action.Evidence) > webSearchMaxResults || len(action.Covered)+len(action.Missing) > webSearchActionMaxClaims || (len(action.Evidence) > 0 && len(action.Covered) == 0) {
		return invalid()
	}
	var evidenceFields []map[string]json.RawMessage
	_ = json.Unmarshal(fields["evidence"], &evidenceFields)
	quotedIDs, quoteTotal := make(map[string]bool), 0
	for index, evidence := range action.Evidence {
		entry := evidenceFields[index]
		if len(entry) != 2 || entry["source_id"] == nil || (entry["quote"] == nil) == (entry["quotes"] == nil) {
			return invalid()
		}
		source, exists := byID[evidence.SourceID]
		if evidence.SourceID == "" || !exists || quotedIDs[evidence.SourceID] || !validWebSearchQuotes(evidence, source.Excerpt) {
			return invalid()
		}
		quoteTotal += utf8.RuneCountInString(evidence.Quote)
		for _, quote := range evidence.Quotes {
			quoteTotal += utf8.RuneCountInString(quote)
		}
		if quoteTotal > webSearchActionMaxQuoteTotal {
			return invalid()
		}
		quotedIDs[evidence.SourceID] = true
	}
	var coveredFields []map[string]json.RawMessage
	_ = json.Unmarshal(fields["covered"], &coveredFields)
	seenClaims, coveredIDs, claimTotal := make(map[string]bool), make(map[string]bool), 0
	for index := range action.Covered {
		claim := &action.Covered[index]
		entry := coveredFields[index]
		claim.Claim = strings.TrimSpace(claim.Claim)
		claimTotal += utf8.RuneCountInString(claim.Claim)
		if len(entry) != 2 || entry["claim"] == nil || entry["source_ids"] == nil || claim.Claim == "" || utf8.RuneCountInString(claim.Claim) > webSearchActionMaxClaimRunes || len(claim.SourceIDs) == 0 || len(claim.SourceIDs) > webSearchMaxResults || seenClaims[claim.Claim] {
			return invalid()
		}
		seenClaims[claim.Claim] = true
		seenSources := make(map[string]bool)
		for _, id := range claim.SourceIDs {
			if !quotedIDs[id] || seenSources[id] {
				return invalid()
			}
			seenSources[id] = true
			coveredIDs[id] = true
		}
	}
	for id := range quotedIDs {
		if !coveredIDs[id] {
			return invalid()
		}
	}
	for index, missing := range action.Missing {
		missing = strings.TrimSpace(missing)
		claimTotal += utf8.RuneCountInString(missing)
		if missing == "" || utf8.RuneCountInString(missing) > webSearchActionMaxClaimRunes || seenClaims[missing] {
			return invalid()
		}
		seenClaims[missing], action.Missing[index] = true, missing
	}
	if claimTotal > webSearchActionMaxClaimTotal {
		return invalid()
	}
	return action, nil
}
