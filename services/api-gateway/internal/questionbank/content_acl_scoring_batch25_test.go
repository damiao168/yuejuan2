package questionbank

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

func TestBatch25ContentStopsAtFailingNormalizationStage(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*Content)
		grade      string
		difficulty string
		points     []string
		copied     bool
	}{
		{"identity", func(c *Content) { c.QuestionType = "unknown" }, " grade_8 ", "", []string{" integers "}, false},
		{"grade", func(c *Content) { c.Metadata.GradeScope = " " }, "", "", []string{" integers "}, false},
		{"metadata", func(c *Content) { c.Metadata.Copyright = "public" }, "grade_8", "unclassified", []string{" integers "}, false},
		{"options", func(c *Content) { c.Options[0] = " " }, "grade_8", "unclassified", []string{" integers "}, true},
		{"knowledge", func(c *Content) { c.KnowledgePoints = []string{" x ", " x "} }, "grade_8", "unclassified", []string{"x", " x "}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fixtureContent()
			input.Stem, input.Metadata.GradeScope = " question ", " grade_8 "
			input.KnowledgePoints = []string{" integers "}
			input.CustomMetadata = map[string]any{"note": "original"}
			tc.mutate(&input)
			got, err := normalizeContent(input)
			if err != ErrInvalidInput || got.Stem != "question" || got.Metadata.GradeScope != tc.grade || got.Metadata.DifficultyBand != tc.difficulty || !reflect.DeepEqual(got.KnowledgePoints, tc.points) {
				t.Fatalf("partial result=%+v, error=%v", got, err)
			}
			// 数组仅在列表校验开始时复制；失败前不提前处理后续字段。
			originalOption := input.Options[0]
			got.Options[0] = "changed"
			if (input.Options[0] == originalOption) != tc.copied {
				t.Fatal("option copy occurred at a different normalization stage")
			}
			got.CustomMetadata["note"] = "changed"
			if input.CustomMetadata["note"] != "changed" {
				t.Fatal("failed normalization copied later custom metadata")
			}
		})
	}
	input := fixtureContent()
	input.Options, input.KnowledgePoints = []string{" A "}, []string{" integers "}
	input.CustomMetadata = map[string]any{"note": "original"}
	got, err := normalizeContent(input)
	if err != nil || got.Options[0] != " A " || got.KnowledgePoints[0] != "integers" || got.Metadata.IntendedUse != "practice" {
		t.Fatalf("successful normalization=%+v, error=%v", got, err)
	}
	got.Options[0], got.KnowledgePoints[0], got.CustomMetadata["note"] = "changed", "changed", "changed"
	if input.Options[0] != " A " || input.KnowledgePoints[0] != " integers " || input.CustomMetadata["note"] != "original" {
		t.Fatal("successful normalization aliases caller content")
	}
}

func TestBatch25ACLValidationKeepsRawNamesAndInput(t *testing.T) {
	user := "00000000-0000-0000-0000-000000000105"
	cases := []struct {
		name    string
		input   UpdateACLInput
		allowed bool
	}{
		{"raw names differ after trimming", UpdateACLInput{ExpectedRevision: 1, Groups: []ACLGroup{{Name: "reviewers", Preset: "Reviewer"}, {Name: " reviewers ", Preset: "Reviewer"}}}, true},
		{"duplicate raw name", UpdateACLInput{ExpectedRevision: 1, Groups: []ACLGroup{{Name: "reviewers", Preset: "Reviewer"}, {Name: "reviewers", Preset: "Reviewer"}}}, false},
		{"duplicate direct user", UpdateACLInput{ExpectedRevision: 1, Bindings: []ACLBinding{{UserID: user, Preset: "Viewer"}, {UserID: user, Preset: "Author"}}}, false},
		{"duplicate group member", UpdateACLInput{ExpectedRevision: 1, Groups: []ACLGroup{{Name: "reviewers", Preset: "Reviewer", MemberIDs: []string{user, user}}}}, false},
		{"unknown preset", UpdateACLInput{ExpectedRevision: 1, Bindings: []ACLBinding{{UserID: user, Preset: "Unknown"}}}, false},
		{"empty group name", UpdateACLInput{ExpectedRevision: 1, Groups: []ACLGroup{{Name: " ", Preset: "Viewer"}}}, false},
		{"invalid group id", UpdateACLInput{ExpectedRevision: 1, Groups: []ACLGroup{{ID: "bad", Name: "viewers", Preset: "Viewer"}}}, false},
		{"invalid revision", UpdateACLInput{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.input)
			err := validateACLInput(tc.input)
			after, _ := json.Marshal(tc.input)
			if (err == nil) != tc.allowed || !bytes.Equal(before, after) {
				t.Fatalf("validation changed raw input or acceptance: %s -> %s, error=%v", before, after, err)
			}
		})
	}
}

func batch25ScoringStore() (*MemoryStore, auth.AccessScope, Version, Version) {
	s, scope := NewMemoryStore(), batch22Scope()
	target := Item{ID: batch22ItemID, TenantID: scope.TenantID, BankID: batch22BankID, Kind: "question", Status: "active"}
	template := Item{ID: "00000000-0000-0000-0000-000000000105", TenantID: scope.TenantID, BankID: batch22ExamID, Kind: "rubric_template", Status: "active"}
	for _, item := range []Item{target, template} {
		s.items[item.ID] = item
		s.banks[item.BankID] = Bank{ID: item.BankID, TenantID: scope.TenantID, SchoolID: "school", Status: "active"}
		s.acl[aclKey(scope.TenantID, item.BankID, scope.ActorID, "read")] = true
	}
	s.acl[aclKey(scope.TenantID, target.BankID, scope.ActorID, "edit")] = true
	v := Version{ID: batch22VersionID, TenantID: scope.TenantID, ItemID: target.ID, Revision: 1, WorkflowStatus: "draft", Content: fixtureContent(), Scoring: emptyScoring()}
	source := Version{ID: "00000000-0000-0000-0000-000000000106", TenantID: scope.TenantID, ItemID: template.ID, Revision: 1, WorkflowStatus: "published", Content: fixtureContent(), Scoring: scoringFixture()}
	source.Scoring.Rubric.Status = "approved"
	s.versions[v.ID], s.versions[source.ID] = v, source
	return s, scope, v, source
}

func TestBatch25MemoryScoringTemplateCopyAndReplay(t *testing.T) {
	s, scope, target, source := batch25ScoringStore()
	input := UpdateScoringInput{ExpectedRevision: target.Revision, Scoring: scoringFixture()}
	input.Scoring.TemplateVersionID = &source.ID
	input.Scoring.Rubric.Points[0].Description = "request rubric"
	ctx := commandreceipt.WithID(context.Background(), "batch25-scoring")
	got, err := s.UpdateScoring(ctx, scope, target.ID, input)
	if err != nil || got.Revision != 2 || got.Scoring.Rubric.Points[0].Description != "point" || got.Scoring.Answer.StandardAnswer != "A" || got.Scoring.Solution.RawText != "solution" || got.AnswerVersionID == nil || got.RubricVersionID == nil || got.BundleHash != bundleHash(got.Content, got.Scoring) {
		t.Fatalf("template update=%+v, error=%v", got, err)
	}
	replay, err := s.UpdateScoring(ctx, scope, target.ID, input)
	if err != nil {
		t.Fatalf("scoring replay=%+v, error=%v", replay, err)
	}
	firstJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	replayJSON, err := json.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}
	// 收据 JSON 会省略空 custom_metadata；按完整对外 JSON 核对重放的版本、评分事实和 ID。
	if !bytes.Equal(firstJSON, replayJSON) || s.versions[target.ID].Revision != got.Revision {
		t.Fatalf("scoring replay changed version: first=%s replay=%s", firstJSON, replayJSON)
	}
	got.Scoring.Rubric.Points[0].Description = "changed"
	if s.versions[target.ID].Scoring.Rubric.Points[0].Description != "point" || s.versions[source.ID].Scoring.Rubric.Points[0].Description != "point" || input.Scoring.Rubric.Points[0].Description != "request rubric" {
		t.Fatal("template update aliases source, stored version or caller")
	}
	delete(s.acl, aclKey(scope.TenantID, batch22BankID, scope.ActorID, "read"))
	if _, err := s.UpdateScoring(ctx, scope, target.ID, input); err != ErrNotFound {
		t.Fatalf("revoked scoring replay error=%v", err)
	}
}

func TestBatch25MemoryScoringPreservesFailureOrder(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*MemoryStore, auth.AccessScope, *UpdateScoringInput, Version, Version)
		want   error
	}{
		{"locked before revision", func(s *MemoryStore, _ auth.AccessScope, in *UpdateScoringInput, v, _ Version) {
			v.WorkflowStatus = "published"
			s.versions[v.ID] = v
			in.ExpectedRevision = 2
		}, ErrLocked},
		{"revision before assets", func(_ *MemoryStore, _ auth.AccessScope, in *UpdateScoringInput, _, _ Version) {
			in.ExpectedRevision = 2
			in.Scoring.Assets = []Asset{{FileAssetID: batch22ItemID, SHA256: strings.Repeat("a", 64)}}
		}, ErrConflict},
		{"assets before unreadable template", func(s *MemoryStore, scope auth.AccessScope, in *UpdateScoringInput, _, _ Version) {
			in.Scoring.Assets = []Asset{{FileAssetID: batch22ItemID, SHA256: strings.Repeat("a", 64)}}
			delete(s.acl, aclKey(scope.TenantID, batch22ExamID, scope.ActorID, "read"))
		}, ErrUnavailable},
		{"template read before target kind", func(s *MemoryStore, scope auth.AccessScope, _ *UpdateScoringInput, v, _ Version) {
			i := s.items[v.ItemID]
			i.Kind = "rubric_template"
			s.items[i.ID] = i
			delete(s.acl, aclKey(scope.TenantID, batch22ExamID, scope.ActorID, "read"))
		}, ErrNotFound},
		{"template tenant isolation", func(s *MemoryStore, _ auth.AccessScope, _ *UpdateScoringInput, _, v Version) {
			v.TenantID = "other"
			s.versions[v.ID] = v
		}, ErrNotFound},
		{"unpublished template", func(s *MemoryStore, _ auth.AccessScope, _ *UpdateScoringInput, _, v Version) {
			v.WorkflowStatus = "draft"
			s.versions[v.ID] = v
		}, ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, scope, target, source := batch25ScoringStore()
			input := UpdateScoringInput{ExpectedRevision: 1, Scoring: Scoring{TemplateVersionID: &source.ID}}
			tc.mutate(s, scope, &input, target, source)
			before, _ := json.Marshal(s.versions[target.ID])
			got, err := s.UpdateScoring(context.Background(), scope, target.ID, input)
			after, _ := json.Marshal(s.versions[target.ID])
			if !errors.Is(err, tc.want) || got.ID != target.ID || !bytes.Equal(before, after) {
				t.Fatalf("failed scoring result=%+v, error=%v, want=%v", got, err, tc.want)
			}
		})
	}
}

func TestBatch25PostgresACLTransactionAndRawReceipt(t *testing.T) {
	failure := errors.New("group member write failed")
	for _, stage := range []string{"success", "inactive_user", "member_write", "stale_archived"} {
		t.Run(stage, func(t *testing.T) {
			assertPostgresACLTransactionAndRawReceipt(t, stage, failure)
		})
	}
}

func assertPostgresACLTransactionAndRawReceipt(t *testing.T, stage string, failure error) {
	t.Helper()
	fixture := questionBankACLTransactionFixture(stage, failure)
	bankID, scope, input := fixture.bankID, fixture.scope, fixture.input
	original, steps, want, end := fixture.original, fixture.steps, fixture.want, fixture.end
	store, script := openBatch22Store(t, steps)
	ctx := commandreceipt.WithID(context.Background(), "batch25-acl")
	got, err := store.UpdateACL(ctx, scope, bankID, input)
	if !errors.Is(err, want) || (want == nil && (got.BankID != bankID || got.Revision != 5)) || (want != nil && !reflect.DeepEqual(got, ACLDocument{})) {
		t.Fatalf("ACL result=%+v, error=%v, want=%v", got, err, want)
	}
	if stage != "stale_archived" {
		after, _ := json.Marshal(input)
		if !bytes.Equal(original, after) {
			t.Fatalf("ACL changed raw request: %s -> %s", original, after)
		}
	}
	// 使用既有脚本验证事务调用顺序与失败回滚；不宣称验证了真实 PostgreSQL 锁竞争。
	assertBatch22Script(t, script, end)
}

type questionBankACLPostgresFixture struct {
	bankID   string
	scope    auth.AccessScope
	input    UpdateACLInput
	original []byte
	steps    []batch22Step
	want     error
	end      string
}

func questionBankACLTransactionFixture(stage string, failure error) questionBankACLPostgresFixture {
	bankID, userID, groupID := "A0000000-0000-0000-0000-000000000101", "00000000-0000-0000-0000-000000000105", "00000000-0000-0000-0000-000000000107"
	scope := batch22Scope()
	input := UpdateACLInput{ExpectedRevision: 4, Bindings: []ACLBinding{{UserID: userID, Preset: "Viewer", Actions: []string{"ignored"}}}, Groups: []ACLGroup{{ID: groupID, Name: " reviewers ", Preset: "Reviewer", MemberIDs: []string{userID}}}}
	original, _ := json.Marshal(input)
	lock := batch22BankStep(bankID, "active")
	lock.fragment = " FOR UPDATE OF b"
	lock.rows[0][0], lock.rows[0][8] = strings.ToLower(bankID), "creator"
	lock.args = map[int]any{0: scope.TenantID, 1: bankID, 4: scope.ActorID, 5: "manage"}
	steps := []batch22Step{{kind: "exec", fragment: "pg_advisory_xact_lock"}, {kind: "query", fragment: "FROM business_command_receipt", columns: 2}, lock}
	want, end := error(nil), "commit"
	if stage == "stale_archived" {
		input.ExpectedRevision, lock.rows[0][5] = 3, "archived"
		want, end = ErrConflict, "rollback"
	} else {
		count := int64(1)
		if stage == "inactive_user" {
			count, want, end = 0, ErrInvalidInput, "rollback"
		}
		// 同一用户同时出现在直接授权和分组中，活跃用户数仍按唯一 ID 比较。
		steps = append(steps, batch22Step{kind: "query", fragment: "SELECT count(DISTINCT id) FROM app_user WHERE tenant_id=$1", columns: 1, rows: [][]driver.Value{{count}}, args: map[int]any{0: scope.TenantID, 1: []string{userID, userID}}})
		if stage != "inactive_user" {
			steps = append(steps,
				batch22Step{kind: "query", fragment: "GROUP BY user_id ORDER BY user_id", columns: 2},
				batch22Step{kind: "query", fragment: "GROUP BY g.id,g.name ORDER BY g.name,g.id", columns: 4},
				batch22Step{kind: "exec", fragment: "VALUES($1,$2,$3,'manage')", args: map[int]any{0: scope.TenantID, 1: bankID, 2: scope.ActorID}},
				batch22Step{kind: "exec", fragment: "DELETE FROM question_bank_acl", args: map[int]any{0: scope.TenantID, 1: bankID, 2: "creator", 3: scope.ActorID}},
				batch22Step{kind: "exec", fragment: "DELETE FROM question_bank_group", args: map[int]any{0: scope.TenantID, 1: bankID}},
				batch22Step{kind: "exec", fragment: "INSERT INTO question_bank_acl", args: map[int]any{1: bankID, 2: userID, 3: "read"}},
				batch22Step{kind: "exec", fragment: "INSERT INTO question_bank_acl", args: map[int]any{3: "statistics"}},
				batch22Step{kind: "exec", fragment: "INSERT INTO question_bank_group(", args: map[int]any{0: groupID, 1: scope.TenantID, 2: bankID, 3: "reviewers", 4: scope.ActorID}},
				batch22Step{kind: "exec", fragment: "INSERT INTO question_bank_group_acl", args: map[int]any{0: scope.TenantID, 1: bankID, 2: groupID, 3: "read"}},
				batch22Step{kind: "exec", fragment: "INSERT INTO question_bank_group_acl", args: map[int]any{3: "review"}},
				batch22Step{kind: "exec", fragment: "INSERT INTO question_bank_group_member", args: map[int]any{0: scope.TenantID, 1: groupID, 2: userID}})
			if stage == "member_write" {
				steps[len(steps)-1].err, want, end = failure, failure, "rollback"
			} else {
				read := batch22BankStep(bankID, "active")
				read.rows[0][6] = int64(5)
				raw, _ := json.Marshal(struct {
					Operation, Target string
					Input             any
				}{"question_bank.acl.update", bankID, input})
				hash := sha256.Sum256(raw)
				steps = append(steps,
					batch22Step{kind: "exec", fragment: "UPDATE question_bank SET revision=revision+1", args: map[int]any{0: scope.TenantID, 1: bankID}}, read,
					batch22Step{kind: "query", fragment: "GROUP BY user_id ORDER BY user_id", columns: 2},
					batch22Step{kind: "query", fragment: "GROUP BY g.id,g.name ORDER BY g.name,g.id", columns: 4},
					batch22Step{kind: "exec", fragment: "INSERT INTO audit_log", args: map[int]any{0: scope.TenantID, 1: scope.ActorID, 2: "question_bank.acl.update", 3: bankID}},
					batch22Step{kind: "exec", fragment: "INSERT INTO event_outbox"},
					batch22Step{kind: "exec", fragment: "INSERT INTO business_command_receipt", args: map[int]any{4: bankID, 5: hex.EncodeToString(hash[:])}})
			}
		}
	}
	return questionBankACLPostgresFixture{bankID, scope, input, original, steps, want, end}
}
