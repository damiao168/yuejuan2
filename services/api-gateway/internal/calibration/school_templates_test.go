package calibration

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func schoolTemplateFixture(t *testing.T) (*Service, *MemoryStore, SchoolTemplateTarget, Policy) {
	t.Helper()
	s := NewMemoryStore()
	service := NewService(s, nil)
	target := SchoolTemplateTarget{SchoolID: "school-a", Subject: "数学", ArchetypeCode: "structured_steps", MaxScore: 10}
	s.SetSchoolTemplateTarget("tenant", "exam", "source", target)
	s.SetSchoolTemplateTarget("tenant", "exam", "target", target)
	p, err := service.PutPolicy(t.Context(), "tenant", "exam", "source", PutPolicyInput{ArchetypeCode: target.ArchetypeCode, MaxScore: 10, MinimumSamples: 3, MaximumMAE: 1, MinimumExactAgreement: 0.6, MinimumWithinOneAgreement: 0.9, MaximumSevereRate: 0, SevereErrorThreshold: 3, QualificationValidityDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	return service, s, target, p
}

func TestSchoolTemplateApprovalFreezesValuesAndRecoversSameSource(t *testing.T) {
	service, store, target, p := schoolTemplateFixture(t)
	input := ApproveSchoolTemplateInput{Name: "同类题通过标准", SourcePolicyRevision: p.Revision}
	item, err := store.ApproveSchoolTemplate(t.Context(), "tenant", "manager", "exam", "source", input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.ApproveSchoolTemplate(t.Context(), "tenant", "manager", "exam", "source", input)
	if err != nil || retry.ID != item.ID {
		t.Fatal("duplicate approval", retry, err)
	}
	changed := templatePolicyInput(item, p.Revision)
	changed.SchoolTemplateID = ""
	changed.SchoolTemplateRevision = 0
	changed.MinimumSamples = 8
	if _, err = service.PutPolicy(t.Context(), "tenant", "exam", "source", changed); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListSchoolTemplates(t.Context(), "tenant", target)
	if err != nil || len(list) != 1 || list[0].Policy.MinimumSamples != 3 {
		t.Fatal("source change altered snapshot", list, err)
	}
	if _, err = store.ApproveSchoolTemplate(t.Context(), "tenant", "manager", "exam", "source", input); !errors.Is(err, ErrConflict) {
		t.Fatal("stale approval accepted", err)
	}
	for _, other := range []SchoolTemplateTarget{{SchoolID: "foreign", Subject: target.Subject, ArchetypeCode: target.ArchetypeCode, MaxScore: 10}, {SchoolID: target.SchoolID, Subject: "语文", ArchetypeCode: target.ArchetypeCode, MaxScore: 10}, {SchoolID: target.SchoolID, Subject: target.Subject, ArchetypeCode: "extended_response", MaxScore: 10}, {SchoolID: target.SchoolID, Subject: target.Subject, ArchetypeCode: target.ArchetypeCode, MaxScore: 20}} {
		items, _ := store.ListSchoolTemplates(t.Context(), "tenant", other)
		if len(items) != 0 {
			t.Fatal("incompatible template visible")
		}
	}
	items, _ := store.ListSchoolTemplates(t.Context(), "other-tenant", target)
	if len(items) != 0 {
		t.Fatal("cross tenant template visible")
	}
}

func TestSchoolTemplateApplyUsesApprovedSnapshotAndTargetCAS(t *testing.T) {
	service, store, _, p := schoolTemplateFixture(t)
	item, err := store.ApproveSchoolTemplate(t.Context(), "tenant", "manager", "exam", "source", ApproveSchoolTemplateInput{Name: "标准", SourcePolicyRevision: p.Revision})
	if err != nil {
		t.Fatal(err)
	}
	// 请求故意伪造其他阈值；模板路径只从批准快照取得值，目标修订仍由本题提供。
	saved, err := service.PutPolicy(t.Context(), "tenant", "exam", "target", PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1, MinimumSamples: 999})
	if err != nil || saved.MinimumSamples != 3 || saved.SchoolTemplateID != item.ID || saved.SchoolTemplateRevision != 1 || saved.QuestionID != "target" {
		t.Fatal(saved, err)
	}
	if _, err = service.PutPolicy(t.Context(), "tenant", "exam", "target", PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale target overwrote policy", err)
	}
	manual := templatePolicyInput(item, saved.Revision)
	manual.SchoolTemplateID = ""
	manual.SchoolTemplateRevision = 0
	manual.MinimumSamples = 5
	saved, err = service.PutPolicy(t.Context(), "tenant", "exam", "target", manual)
	if err != nil || saved.SchoolTemplateID != "" || saved.SchoolTemplateRevision != 0 {
		t.Fatal("manual save retained false provenance", saved, err)
	}
}

func TestWithdrawSchoolTemplateKeepsHistoryAndBlocksFutureApply(t *testing.T) {
	service, store, target, p := schoolTemplateFixture(t)
	input := ApproveSchoolTemplateInput{Name: "标准", SourcePolicyRevision: p.Revision}
	item, err := store.ApproveSchoolTemplate(t.Context(), "tenant", "manager", "exam", "source", input)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.PutPolicy(t.Context(), "tenant", "exam", "target", PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	withdrawn, err := store.WithdrawSchoolTemplate(t.Context(), "tenant", "manager", target, item.ID, WithdrawSchoolTemplateInput{ExpectedRevision: 1, Reason: "更换学校标准"})
	if err != nil || withdrawn.Status != "withdrawn" || withdrawn.Revision != 2 {
		t.Fatal(withdrawn, err)
	}
	items, _ := store.ListSchoolTemplates(t.Context(), "tenant", target)
	if len(items) != 0 {
		t.Fatal("withdrawn template remained available")
	}
	if _, err = service.PutPolicy(t.Context(), "tenant", "exam", "target", PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1, ExpectedRevision: saved.Revision}); !errors.Is(err, ErrTemplateUnavailable) {
		t.Fatal("withdrawn template applied", err)
	}
	still, _ := service.GetPolicy(t.Context(), "tenant", "exam", "target")
	if still.Revision != saved.Revision || still.SchoolTemplateID != item.ID {
		t.Fatal("withdrawal changed saved fact")
	}
	retry, err := store.ApproveSchoolTemplate(t.Context(), "tenant", "manager", "exam", "source", input)
	if err != nil || retry.ID != item.ID || retry.Status != "withdrawn" {
		t.Fatal("old approval retry resurrected template", retry, err)
	}
}

func TestSchoolTemplateApprovalRequiresSchoolManagementScope(t *testing.T) {
	_, store, target, p := schoolTemplateFixture(t)
	const examID = "11111111-1111-4111-8111-111111111111"
	const questionID = "22222222-2222-4222-8222-222222222222"
	store.SetSchoolTemplateTarget("tenant", examID, questionID, target)
	store.mu.Lock()
	copy := p
	copy.ExamID = examID
	copy.QuestionID = questionID
	store.policies[policyKey("tenant", examID, questionID)] = copy
	store.mu.Unlock()
	h := NewHandler(NewService(store, nil), nil)
	request := func(user auth.User, method string) *httptest.ResponseRecorder {
		t.Helper()
		scope, err := auth.ResolveDeclaredAccessScope(user)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/", strings.NewReader(`{"name":"学校标准","source_policy_revision":1}`))
		req.SetPathValue("examId", examID)
		req.SetPathValue("questionId", questionID)
		req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), user), scope))
		rec := httptest.NewRecorder()
		if method == http.MethodGet {
			h.ListSchoolTemplates(rec, req)
		} else {
			h.ApproveSchoolTemplate(rec, req)
		}
		return rec
	}
	teacher := auth.User{ID: "teacher", TenantID: "tenant", Roles: []string{"teacher"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"teacher": map[string]any{"scope": "class", "class_id": "class", "exam_id": examID}}}
	list := request(teacher, http.MethodGet)
	var directory struct {
		Capabilities templateCapabilities `json:"capabilities"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &directory)
	if list.Code != 200 || directory.Capabilities.CanApprove || directory.Capabilities.CanWithdraw {
		t.Fatal("question manager acquired approval", list.Code, list.Body)
	}
	denied := request(teacher, http.MethodPost)
	if denied.Code != 403 {
		t.Fatal(denied.Code, denied.Body)
	}
	manager := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
	approved := request(manager, http.MethodPost)
	if approved.Code != 200 {
		t.Fatal(approved.Code, approved.Body)
	}
	manager.DataScope = map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-other", "exam_id": examID}}
	denied = request(manager, http.MethodPost)
	if denied.Code != 403 {
		t.Fatal("projected external exam became school management", denied.Code, denied.Body)
	}
}

func TestSchoolTemplateWritesRejectTargetChangesAfterAuthorization(t *testing.T) {
	_, store, target, p := schoolTemplateFixture(t)
	item, err := store.ApproveSchoolTemplate(t.Context(), "tenant", "manager", "exam", "source", ApproveSchoolTemplateInput{Name: "标准", SourcePolicyRevision: p.Revision})
	if err != nil {
		t.Fatal(err)
	}
	user := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	base := auth.WithAccessScope(auth.WithUser(t.Context(), user), scope)
	beforeSource := withExpectedTemplateTarget(base, "exam", "source", target)
	beforeTarget := withExpectedTemplateTarget(base, "exam", "target", target)
	changed := target
	changed.SchoolID = "school-b"
	store.SetSchoolTemplateTarget("tenant", "exam", "source", changed)
	store.SetSchoolTemplateTarget("tenant", "exam", "target", changed)
	if _, err = store.ApproveSchoolTemplate(beforeSource, "tenant", "manager", "exam", "source", ApproveSchoolTemplateInput{Name: "新标准", SourcePolicyRevision: p.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatal("approval crossed target change", err)
	}
	if _, err = store.ApplySchoolTemplate(beforeTarget, "tenant", "exam", "target", PutPolicyInput{SchoolTemplateID: item.ID, SchoolTemplateRevision: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("apply crossed target change", err)
	}
	if _, err = store.WithdrawSchoolTemplate(beforeSource, "tenant", "manager", target, item.ID, WithdrawSchoolTemplateInput{ExpectedRevision: 1, Reason: "撤回"}); !errors.Is(err, ErrConflict) {
		t.Fatal("withdraw crossed target change", err)
	}
	if _, err = store.ApproveSchoolTemplate(base, "tenant", "manager", "exam", "source", ApproveSchoolTemplateInput{Name: "缺上下文", SourcePolicyRevision: p.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatal("HTTP identity bypassed target binding", err)
	}
}
