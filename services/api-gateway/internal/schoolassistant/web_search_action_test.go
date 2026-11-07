package schoolassistant

import (
	"encoding/json"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func actionResponse(content string) modelgovernance.AgentResponse {
	return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Content: content}}
}

func actionTestSources() []Source {
	return []Source{
		{ID: "web-1", Excerpt: "2026年11月2日，北京天气晴。最高温17℃，最低温4℃。"},
		{ID: "web-2", Excerpt: "北京预报于11月2日08时发布。"},
	}
}

func validAnswerAction() string {
	return `{"action":"answer","answer":"北京今天晴，最高17℃、最低4℃。[1]","covered":[{"claim":"北京今天的天气和预报温度","source_ids":["web-1"]}],"missing":[],"evidence":[{"source_id":"web-1","quote":"2026年11月2日，北京天气晴。最高温17℃，最低温4℃。"}]}`
}

func TestDecodeWebSearchActionAcceptsBoundedActions(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"search", `{"action":"search","query":" 北京 2026年11月2日 天气 "}`, "search"},
		{"search_with_goal", `{"action":"search","query":"北京天气","information_needed":"指定日期的天气与预报温度"}`, "search"},
		{"search_with_full_question", `{"action":"search","question":" 比较北京和上海今天的预报温度 ","query":"北京 2026年11月2日 预报温度"}`, "search"},
		{"search_batch", `{"action":"search","question":"比较北京与上海的天气","query":" 北京天气 ","additional_queries":[" 上海天气 "]}`, "search"},
		{"search_prefetch", `{"action":"search","question":"查询公开信息","query":"公开信息","read_top_results":2}`, "search"},
		{"search_without_prefetch", `{"action":"search","query":"公开信息","read_top_results":0}`, "search"},
		{"search_empty_batch", `{"action":"search","question":"北京今天的天气","query":"北京天气","additional_queries":[]}`, "search"},
		{"read", `{"action":"read","source_ids":["web-1","web-2"],"information_needed":"日期与预报温度"}`, "read"},
		{"answer", validAnswerAction(), "answer"},
		{"direct_answer", `{"action":"answer","answer":"你好。","covered":[],"missing":[],"evidence":[]}`, "answer"},
		{"answer_preserves_whitespace", `{"action":"answer","answer":"\n你好。\n","covered":[],"missing":[],"evidence":[]}`, "answer"},
		{"cannot_verify", `{"action":"answer","answer":"本次未能核实实时天气。","covered":[],"missing":["实时天气"],"evidence":[]}`, "answer"},
		{"clarify", `{"action":"clarify","answer":"请问你要查询哪个城市？"}`, "clarify"},
		{"business", `{"action":"business"}`, "business"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action, err := decodeWebSearchAction(actionResponse(test.body), actionTestSources(), 3)
			if err != nil || action.Action != test.want {
				t.Fatalf("action rejected: %+v %v", action, err)
			}
			if test.name == "search" && (strings.HasPrefix(action.Query, " ") || strings.HasSuffix(action.Query, " ")) {
				t.Fatal("query must be normalized before search")
			}
			if test.name == "answer_preserves_whitespace" && action.Answer != "\n你好。\n" {
				t.Fatal("final validation changed already streamed answer text")
			}
			if test.name == "search_with_full_question" && action.Question != "比较北京和上海今天的预报温度" {
				t.Fatal("full question was replaced by its first sub-query")
			}
			if test.name == "search_batch" && (action.Query != "北京天气" || len(action.AdditionalQueries) != 1 || action.AdditionalQueries[0] != "上海天气") {
				t.Fatalf("batch query was not normalized: %+v", action)
			}
			if test.name == "search_empty_batch" && (action.Query != "北京天气" || len(action.AdditionalQueries) != 0) {
				t.Fatalf("empty batch changed primary search: %+v", action)
			}
		})
	}
}

func TestDecodeWebSearchActionRejectsConflictingOrForeignInstructions(t *testing.T) {
	tests := map[string]string{
		"unknown_action":         `{"action":"fetch_url","url":"https://example.com"}`,
		"prefetch_negative":      `{"action":"search","query":"公开信息","read_top_results":-1}`,
		"prefetch_excess":        `{"action":"search","query":"公开信息","read_top_results":3}`,
		"prefetch_null":          `{"action":"search","query":"公开信息","read_top_results":null}`,
		"prefetch_wrong_type":    `{"action":"search","query":"公开信息","read_top_results":"1"}`,
		"prefetch_fraction":      `{"action":"search","query":"公开信息","read_top_results":1.5}`,
		"prefetch_other_action":  `{"action":"clarify","answer":"哪个城市？","read_top_results":1}`,
		"unknown_field":          `{"action":"search","query":"天气","url":"https://example.com"}`,
		"duplicate_field":        `{"action":"search","query":"天气","query":"成绩"}`,
		"uppercase_field":        `{"Action":"business"}`,
		"uppercase_nested":       strings.Replace(validAnswerAction(), `"claim":`, `"Claim":`, 1),
		"uppercase_evidence":     strings.Replace(validAnswerAction(), `"source_id":`, `"Source_ID":`, 1),
		"duplicate_nested":       strings.Replace(validAnswerAction(), `"claim":`, `"claim":"前一个值","claim":`, 1),
		"trailing_object":        `{"action":"business"}{"action":"search","query":"隐私"}`,
		"markdown":               "```json\n{\"action\":\"business\"}\n```",
		"query_null":             `{"action":"search","query":null}`,
		"query_empty":            `{"action":"search","query":" "}`,
		"query_wrong_type":       `{"action":"search","query":12}`,
		"question_null":          `{"action":"search","query":"北京天气","question":null}`,
		"question_empty":         `{"action":"search","query":"北京天气","question":" "}`,
		"question_wrong_type":    `{"action":"search","query":"北京天气","question":[]}`,
		"question_other_action":  `{"action":"business","question":"学校成绩"}`,
		"batch_null":             `{"action":"search","query":"北京天气","additional_queries":null}`,
		"batch_wrong_type":       `{"action":"search","query":"北京天气","additional_queries":"上海天气"}`,
		"batch_null_item":        `{"action":"search","query":"北京天气","additional_queries":[null]}`,
		"batch_blank_item":       `{"action":"search","query":"北京天气","additional_queries":[" "]}`,
		"batch_oversized":        `{"action":"search","query":"北京天气","additional_queries":["上海天气","南京天气"]}`,
		"batch_duplicate":        `{"action":"search","query":"北京 天气","additional_queries":[" 北京  天气 "]}`,
		"batch_case_duplicate":   `{"action":"search","query":"Beijing Weather","additional_queries":[" beijing  WEATHER "]}`,
		"batch_uppercase_field":  `{"action":"search","query":"北京天气","Additional_Queries":["上海天气"]}`,
		"batch_repeated_field":   `{"action":"search","query":"北京天气","additional_queries":["上海天气"],"additional_queries":["南京天气"]}`,
		"batch_foreign_action":   `{"action":"clarify","answer":"哪个城市？","additional_queries":["上海天气"]}`,
		"read_missing_goal":      `{"action":"read","source_ids":["web-1"]}`,
		"read_goal_null":         `{"action":"read","source_ids":["web-1"],"information_needed":null}`,
		"read_empty":             `{"action":"read","source_ids":[],"information_needed":"天气"}`,
		"read_foreign_id":        `{"action":"read","source_ids":["invented"],"information_needed":"天气"}`,
		"read_duplicate_id":      `{"action":"read","source_ids":["web-1","web-1"],"information_needed":"天气"}`,
		"business_extra":         `{"action":"business","query":"成绩"}`,
		"clarify_extra":          `{"action":"clarify","answer":"哪个城市？","query":"北京天气"}`,
		"answer_missing_arrays":  `{"action":"answer","answer":"你好"}`,
		"answer_null_arrays":     `{"action":"answer","answer":"你好","covered":null,"missing":[],"evidence":[]}`,
		"answer_mixed_action":    strings.Replace(validAnswerAction(), `"answer",`, `"answer","source_ids":["web-1"],`, 1),
		"forged_quote":           strings.Replace(validAnswerAction(), `"quote":"2026年11月2日，北京天气晴。最高温17℃，最低温4℃。"`, `"quote":"北京在下暴雨。"`, 1),
		"title_only_quote":       strings.Replace(validAnswerAction(), `"quote":"2026年11月2日，北京天气晴。最高温17℃，最低温4℃。"`, `"quote":"北京市官方天气标题"`, 1),
		"foreign_evidence_id":    strings.Replace(validAnswerAction(), `"source_id":"web-1"`, `"source_id":"invented"`, 1),
		"quote_and_quotes":       strings.Replace(validAnswerAction(), `"quote":`, `"quotes":["北京"],"quote":`, 1),
		"unquoted_claim_source":  strings.Replace(validAnswerAction(), `"source_ids":["web-1"]`, `"source_ids":["web-2"]`, 1),
		"duplicate_claim_source": strings.Replace(validAnswerAction(), `"source_ids":["web-1"]`, `"source_ids":["web-1","web-1"]`, 1),
		"evidence_not_covered":   strings.Replace(validAnswerAction(), `"covered":[{"claim":"北京今天的天气和预报温度","source_ids":["web-1"]}]`, `"covered":[]`, 1),
		"claim_without_evidence": `{"action":"answer","answer":"晴。","covered":[{"claim":"天气","source_ids":["web-1"]}],"missing":[],"evidence":[]}`,
		"empty_missing":          `{"action":"answer","answer":"没有信息。","covered":[],"missing":[" "],"evidence":[]}`,
		"duplicate_missing":      `{"action":"answer","answer":"没有信息。","covered":[],"missing":["温度","温度"],"evidence":[]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWebSearchAction(actionResponse(body), actionTestSources(), 3); err == nil {
				t.Fatalf("unsafe action was accepted: %s", body)
			} else if code, _ := conversationError(err); code != "invalid_web_search_action" {
				t.Fatalf("unexpected public error: %s", code)
			}
		})
	}
	response := actionResponse(`{"action":"business"}`)
	response.Message.ToolCalls = []modelgovernance.AgentToolCall{{Name: "other"}}
	if _, err := decodeWebSearchAction(response, nil, 3); err == nil {
		t.Fatal("native tool calls cannot bypass the action object contract")
	}
}

func TestDecodeWebSearchActionEnforcesReadAndOutputBudgets(t *testing.T) {
	for _, remaining := range []int{-1, 0, 1, 2, 3} {
		_, err := decodeWebSearchAction(actionResponse(`{"action":"search","query":"公开信息","read_top_results":2}`), nil, remaining)
		if (err == nil) != (remaining >= 2) {
			t.Fatalf("prefetch exceeded remaining read budget %d: %v", remaining, err)
		}
	}
	read := actionResponse(`{"action":"read","source_ids":["web-1","web-2"],"information_needed":"天气"}`)
	for _, remaining := range []int{-1, 0, 1} {
		if _, err := decodeWebSearchAction(read, actionTestSources(), remaining); err == nil {
			t.Fatalf("read exceeded remaining budget %d", remaining)
		}
	}
	query := strings.Repeat("天", webSearchMaxQueryRunes)
	for _, extra := range []int{0, 1} {
		body, _ := json.Marshal(map[string]any{"action": "search", "query": query + strings.Repeat("气", extra)})
		_, err := decodeWebSearchAction(actionResponse(string(body)), nil, 3)
		if (err != nil) != (extra == 1) {
			t.Fatalf("query rune bound incorrect: extra=%d err=%v", extra, err)
		}
	}
	for _, extra := range []int{0, 1} {
		body, _ := json.Marshal(map[string]any{"action": "search", "query": "北京天气", "question": strings.Repeat("问", 1000+extra)})
		_, err := decodeWebSearchAction(actionResponse(string(body)), nil, 3)
		if (err != nil) != (extra == 1) {
			t.Fatalf("question rune bound incorrect: extra=%d err=%v", extra, err)
		}
	}
	for _, extra := range []int{0, 1} {
		body, _ := json.Marshal(map[string]any{"action": "search", "query": "北京天气", "additional_queries": []string{strings.Repeat("问", webSearchMaxQueryRunes+extra)}})
		_, err := decodeWebSearchAction(actionResponse(string(body)), nil, 3)
		if (err != nil) != (extra == 1) {
			t.Fatalf("additional query rune bound incorrect: extra=%d err=%v", extra, err)
		}
	}
	var wire map[string]any
	_ = json.Unmarshal([]byte(validAnswerAction()), &wire)
	wire["answer"] = strings.Repeat("字", webSearchActionMaxAnswerRunes+1)
	body, _ := json.Marshal(wire)
	if _, err := decodeWebSearchAction(actionResponse(string(body)), actionTestSources(), 3); err == nil {
		t.Fatal("oversized answer accepted")
	}
	wire["answer"] = "未能核实。"
	wire["covered"], wire["evidence"] = []any{}, []any{}
	wire["missing"] = []string{"一", "二", "三", "四", "五", "六", "七", "八", "九"}
	body, _ = json.Marshal(wire)
	if _, err := decodeWebSearchAction(actionResponse(string(body)), nil, 3); err == nil {
		t.Fatal("oversized coverage list accepted")
	}
	wire["missing"] = []string{strings.Repeat("字", webSearchActionMaxClaimRunes+1)}
	body, _ = json.Marshal(wire)
	if _, err := decodeWebSearchAction(actionResponse(string(body)), nil, 3); err == nil {
		t.Fatal("oversized claim accepted")
	}
}

func TestDecodeWebSearchActionRequiresEveryEvidenceSourceToBeCovered(t *testing.T) {
	var wire map[string]any
	_ = json.Unmarshal([]byte(validAnswerAction()), &wire)
	wire["evidence"] = []webSearchEvidence{{SourceID: "web-1", Quote: "北京天气晴。"}, {SourceID: "web-2", Quote: "北京预报于11月2日08时发布。"}}
	body, _ := json.Marshal(wire)
	if _, err := decodeWebSearchAction(actionResponse(string(body)), actionTestSources(), 3); err == nil {
		t.Fatal("unassociated evidence source accepted")
	}
	wire["covered"] = []webSearchCoveredClaim{{Claim: "天气与发布时间", SourceIDs: []string{"web-1", "web-2"}}}
	body, _ = json.Marshal(wire)
	if _, err := decodeWebSearchAction(actionResponse(string(body)), actionTestSources(), 3); err != nil {
		t.Fatalf("supported coverage rejected: %v", err)
	}
}

func TestDecodeWebSearchActionBoundsAllQuotesTogether(t *testing.T) {
	quote := strings.Repeat("原", 2000)
	sources := []Source{{ID: "web-1", Excerpt: quote + "补"}, {ID: "web-2", Excerpt: quote}}
	answer := map[string]any{
		"action": "answer", "answer": "资料已找到。[1][2]",
		"covered":  []webSearchCoveredClaim{{Claim: "两份材料", SourceIDs: []string{"web-1", "web-2"}}},
		"missing":  []string{},
		"evidence": []webSearchEvidence{{SourceID: "web-1", Quote: quote}, {SourceID: "web-2", Quote: quote}},
	}
	body, _ := json.Marshal(answer)
	if _, err := decodeWebSearchAction(actionResponse(string(body)), sources, 3); err != nil {
		t.Fatalf("exact total quote limit rejected: %v", err)
	}
	answer["evidence"] = []webSearchEvidence{{SourceID: "web-1", Quotes: []string{quote, "补"}}, {SourceID: "web-2", Quote: quote}}
	body, _ = json.Marshal(answer)
	if _, err := decodeWebSearchAction(actionResponse(string(body)), sources, 3); err == nil {
		t.Fatal("combined quotes exceeded output budget")
	}
}
