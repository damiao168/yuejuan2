package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// 草稿候选必须可通过严格解码往返保存；可选审核字段也不能迫使旧客户端伪造确认事实。
func TestTemplateReviewOpenAPITracksGoDraftAndAuditMetadata(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	schemas := object(t, object(t, document, "components"), "schemas")
	for _, binding := range []struct {
		name string
		kind reflect.Type
	}{
		{"AnswerSheetLayoutRegion", reflect.TypeFor[paper.LayoutRegion]()},
		{"AnswerSheetTemplateEditorState", reflect.TypeFor[paper.TemplateEditorState]()},
		{"AnswerSheetTemplateEditorCandidate", reflect.TypeFor[paper.TemplateEditorCandidate]()},
		{"AnswerSheetTemplateLayout", reflect.TypeFor[paper.TemplateLayout]()},
		{"AnalyzeAnswerSheetPageRequest", reflect.TypeFor[paper.TemplatePageAnalysisInput]()},
		{"TemplateAnalyzedRegion", reflect.TypeFor[paper.TemplateAnalyzedRegion]()},
		{"TemplateAnalyzedObjectiveItem", reflect.TypeFor[paper.TemplateAnalyzedObjectiveItem]()},
		{"TemplateAnalyzedObjectiveOption", reflect.TypeFor[paper.TemplateAnalyzedObjectiveOption]()},
	} {
		t.Run(binding.name, func(t *testing.T) {
			schema := object(t, schemas, binding.name)
			if schema["additionalProperties"] != false {
				t.Fatal("draft transport must retain strict fields")
			}
			properties := object(t, schema, "properties")
			for index := range binding.kind.NumField() {
				name := strings.Split(binding.kind.Field(index).Tag.Get("json"), ",")[0]
				if _, ok := properties[name]; !ok {
					t.Errorf("missing transport property: %s", name)
				}
			}
			if len(properties) != binding.kind.NumField() {
				t.Fatal("contract includes fields unavailable to Go transport")
			}
		})
	}
	region := object(t, schemas, "AnswerSheetLayoutRegion")
	for _, field := range []string{"origin", "review_state", "semantic_check", "reason_codes", "analysis_run_id"} {
		required, _ := region["required"].([]any)
		if slices.Contains(required, any(field)) {
			t.Fatalf("legacy region now requires %s", field)
		}
	}
	for field, want := range map[string][]string{
		"origin":         {"model", "local_detector", "library", "human"},
		"review_state":   {"suggested", "edited", "confirmed", "discarded"},
		"semantic_check": {"supported", "needs_review", "not_evaluated"},
	} {
		got := object(t, object(t, region, "properties"), field)["enum"].([]any)
		if len(got) != len(want) {
			t.Fatalf("%s enum drift: %v", field, got)
		}
		for _, value := range want {
			if !slices.Contains(got, any(value)) {
				t.Fatalf("%s missing %s", field, value)
			}
		}
	}
	state := object(t, schemas, "AnswerSheetTemplateEditorState")
	assertRequired(t, state, []string{"version", "source_sha256", "candidates"})
	assertPropertyRef(t, object(t, schemas, "AnswerSheetTemplateLayout"), "editor_state", "#/components/schemas/AnswerSheetTemplateEditorState")
	analyzed := object(t, schemas, "TemplateAnalyzedRegion")
	if required, _ := analyzed["required"].([]any); slices.Contains(required, any("origin")) {
		t.Fatal("legacy worker candidates must not require an invented origin")
	}
	if required, _ := analyzed["required"].([]any); slices.Contains(required, any("objective_items")) {
		t.Fatal("legacy worker candidates must not require objective subregions")
	}
	origins := object(t, object(t, analyzed, "properties"), "origin")["enum"].([]any)
	if !reflect.DeepEqual(origins, []any{"model", "local_detector"}) {
		t.Fatalf("analysis origin must distinguish model and local suggestions: %v", origins)
	}
	// 浏览器只提交图片与几何；正式填空题白名单由后端快照构造，不属于公开输入契约。
	if _, exposed := object(t, object(t, schemas, "AnalyzeAnswerSheetPageRequest"), "properties")["fill_blank_question_labels"]; exposed {
		t.Fatal("browser input exposed the official fill-blank whitelist")
	}
}
