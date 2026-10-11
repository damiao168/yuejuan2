package exam

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagementUpdateBatch9Validation(t *testing.T) {
	text := func(value string) *string { return &value }
	classes := func(values ...string) *[]string { return &values }
	cases := []struct {
		name  string
		input ManagementUpdateInput
		valid bool
	}{
		{name: "no_fields", input: ManagementUpdateInput{ExpectedRevision: 1}},
		{name: "zero_revision", input: ManagementUpdateInput{Name: text("考试")}},
		{name: "negative_revision", input: ManagementUpdateInput{ExpectedRevision: -1, ClassIDs: classes("class-a")}},
		{name: "name_only_omits_classes", input: ManagementUpdateInput{ExpectedRevision: 1, Name: text("考试")}, valid: true},
		{name: "blank_name", input: ManagementUpdateInput{ExpectedRevision: 1, Name: text(" \t ")}},
		{name: "name_max_bytes", input: ManagementUpdateInput{ExpectedRevision: 1, Name: text(strings.Repeat("x", 600))}, valid: true},
		{name: "name_over_bytes", input: ManagementUpdateInput{ExpectedRevision: 1, Name: text(strings.Repeat("x", 601))}},
		// 长度上限沿用 UTF-8 字节数，不能在重构时改为字符个数。
		{name: "chinese_name_max_bytes", input: ManagementUpdateInput{ExpectedRevision: 1, Name: text(strings.Repeat("考", 200))}, valid: true},
		{name: "chinese_name_over_bytes", input: ManagementUpdateInput{ExpectedRevision: 1, Name: text(strings.Repeat("考", 201))}},
		{name: "type_only", input: ManagementUpdateInput{ExpectedRevision: 1, ExamType: text("模拟")}, valid: true},
		{name: "blank_type", input: ManagementUpdateInput{ExpectedRevision: 1, ExamType: text(" ")}},
		{name: "type_max_bytes", input: ManagementUpdateInput{ExpectedRevision: 1, ExamType: text(strings.Repeat("x", 200))}, valid: true},
		{name: "type_over_bytes", input: ManagementUpdateInput{ExpectedRevision: 1, ExamType: text(strings.Repeat("x", 201))}},
		{name: "empty_class_selection", input: ManagementUpdateInput{ExpectedRevision: 1, ClassIDs: classes()}},
		{name: "empty_class_id", input: ManagementUpdateInput{ExpectedRevision: 1, ClassIDs: classes("")}},
		{name: "blank_class_id", input: ManagementUpdateInput{ExpectedRevision: 1, ClassIDs: classes("class-a", " \t ", "class-b")}},
		{name: "class_selection_only", input: ManagementUpdateInput{ExpectedRevision: 1, ClassIDs: classes("class-a", "class-b")}, valid: true},
		{name: "padded_class_id_not_rewritten", input: ManagementUpdateInput{ExpectedRevision: 1, ClassIDs: classes(" class-a ")}, valid: true},
		{name: "invalid_classes_with_valid_name", input: ManagementUpdateInput{ExpectedRevision: 1, Name: text("考试"), ClassIDs: classes()}},
		{name: "invalid_type_with_valid_classes", input: ManagementUpdateInput{ExpectedRevision: 1, ExamType: text(""), ClassIDs: classes("class-a")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			got := validateManagementUpdate(tc.input)
			if tc.valid && got != nil || !tc.valid && got != ErrInvalidInput {
				t.Fatalf("valid=%v, error=%v", tc.valid, got)
			}
			after, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("input changed: before=%s, after=%s", before, after)
			}
		})
	}
}
