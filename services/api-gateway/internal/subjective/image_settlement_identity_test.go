package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func settlementIdentityModel() *ImageModelConfig {
	return &ImageModelConfig{ConfigID: "11111111-1111-4111-8111-111111111111", AdapterType: "openai_compatible",
		ProviderKey: "custom", BaseURL: "https://example.invalid/v1", APIKey: "synthetic-secret",
		ModelName: "deepseek-test", ModelVersion: "deepseek-test-version", ConfigRevision: "frozen-config-revision"}
}

func TestImageSettlementCarriesTrustedManagedIdentityWithoutChangingRequestOrBinding(t *testing.T) {
	model := settlementIdentityModel()
	value := serviceTestContext()
	value.InputVersionID = "frozen-image-input"
	value.Observation = &StudentObservation{ModelBinding: imageModelBinding(model)}
	policy := ModelPolicy{ModelVersion: model.ModelVersion}
	requestBefore, _ := json.Marshal(model)
	oldBinding := imageModelBinding(model)
	withoutID := *model
	withoutID.ConfigID = ""
	requestWithoutID, _ := json.Marshal(&withoutID)
	if !bytes.Equal(requestBefore, requestWithoutID) || imageModelBinding(&withoutID) != oldBinding {
		t.Fatal("internal config ID changed the Python request/cache identity or existing observation binding")
	}
	if err := bindImageGradeModel(&value, policy, model); err != nil {
		t.Fatal(err)
	}
	grade := successfulGrade(value, policy, "run-image", validServiceOutput())
	if grade.ModelConfigID != model.ConfigID || grade.ModelName != model.ModelName {
		t.Fatalf("managed model_call_fact identity was lost before persistence: %#v", grade)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), model.ConfigID) || strings.Contains(string(raw), model.APIKey) {
		t.Fatal("settlement-only identity or credential leaked into persisted context")
	}
}

func TestImageSettlementRejectsMissingOrChangedManagedIdentity(t *testing.T) {
	for _, scenario := range []string{"missing model", "missing config ID", "missing model name", "changed revision", "changed version"} {
		t.Run(scenario, func(t *testing.T) {
			model := settlementIdentityModel()
			value := serviceTestContext()
			value.InputVersionID = "frozen-image-input"
			value.Observation = &StudentObservation{ModelBinding: imageModelBinding(model)}
			policy := ModelPolicy{ModelVersion: model.ModelVersion}
			switch scenario {
			case "missing model":
				model = nil
			case "missing config ID":
				model.ConfigID = ""
			case "missing model name":
				model.ModelName = ""
			case "changed revision":
				model.ConfigRevision = "new-selection"
			case "changed version":
				model.ModelVersion = "other-version"
			}
			if err := bindImageGradeModel(&value, policy, model); !errors.Is(err, ErrWorkerResultVersionConflict) {
				t.Fatalf("changed or incomplete identity was accepted: %v", err)
			}
			if value.modelConfigID != "" || value.modelName != "" {
				t.Fatal("rejected model identity must not reach grade persistence")
			}
		})
	}
}

func TestWorkerImageSettlementResolvesOnlyTheLeasedTenantAndPreservesLegacyPath(t *testing.T) {
	model := settlementIdentityModel()
	var tenants []string
	handler := &Handler{imageModelResolver: func(_ context.Context, tenant string) (*ImageModelConfig, error) {
		tenants = append(tenants, tenant)
		return model, nil
	}}
	legacy := serviceTestContext()
	if err := handler.bindWorkerImageGradeModel(t.Context(), "school-tenant", &legacy, ModelPolicy{}); err != nil || len(tenants) != 0 {
		t.Fatal("legacy text settlement unexpectedly resolved an image model")
	}
	value := legacy
	value.InputVersionID = "frozen-image-input"
	value.Observation = &StudentObservation{ModelBinding: imageModelBinding(model)}
	if err := handler.bindWorkerImageGradeModel(t.Context(), "school-tenant", &value, ModelPolicy{ModelVersion: model.ModelVersion}); err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 1 || tenants[0] != "school-tenant" || value.modelConfigID != model.ConfigID {
		t.Fatalf("settlement resolved the wrong tenant or dropped identity: %v %#v", tenants, value)
	}
}

func TestCompleteWorkerImageSettlementUsesTrustedIdentityAndRejectsChangedBinding(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "current binding", true: "changed binding"}[changed], func(t *testing.T) {
			fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
			value, _ := fixture.store.LoadContext(t.Context(), workerServiceTenant, fixture.run.AnswerSegmentID)
			model := settlementIdentityModel()
			model.ModelVersion = fixture.run.ModelVersion
			observation := imageTestObservation(value.Question.ID, "synthetic-crop")
			observation.ModelBinding = imageModelBinding(model)
			value.InputVersionID, value.CropSHA256, value.Observation = observation.InputVersionID, observation.CropSHA256, &observation
			fixture.store.AddContext(workerServiceTenant, value.SegmentID, value)
			if changed {
				model.ConfigRevision = "changed-selection"
			}
			handler := NewHandler(fixture.store, nil, nil).WithWorkerRuntimeStore(fixture.runtime).
				WithImageModelResolver(func(_ context.Context, tenantID string) (*ImageModelConfig, error) {
					if tenantID != workerServiceTenant {
						t.Fatalf("resolved another tenant: %s", tenantID)
					}
					return model, nil
				})
			output := validServiceOutput()
			output.SchemaVersion, output.RequestID = ImageProtocolVersion, fixture.run.RequestID
			output.ModelVersion, output.PromptVersion, output.RubricVersion = fixture.run.ModelVersion, fixture.run.PromptVersion, fixture.run.RubricVersion
			output.ImageJudgments = []ImageCriterionJudgment{{CriterionID: "p1", Status: "supported", EvidenceIDs: []string{"student-step"}, Reason: "合成证据支持采分点"}}
			output.RawOutput = map[string]any{"model_config_id": "worker-forged-config", "model_name": "worker-forged-model"}
			raw, _ := json.Marshal(WorkerResultInput{TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken, ResultSchemaVersion: "subjective-grade-v1", Output: output})
			complete := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/internal/subjective/result", bytes.NewReader(raw))
				req.SetPathValue("runId", fixture.run.ID)
				req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "teacher-1", TenantID: workerServiceTenant}))
				response := httptest.NewRecorder()
				handler.CompleteWorker(response, req)
				return response
			}
			response := complete()
			if changed {
				if response.Code != http.StatusConflict {
					t.Fatalf("changed binding accepted: %d %s", response.Code, response.Body.String())
				}
				if _, err := fixture.store.GetGradeByAdapterRequestID(t.Context(), workerServiceTenant, fixture.run.RequestID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("changed binding persisted a grade: %v", err)
				}
				return
			}
			grade, err := fixture.store.GetGradeByAdapterRequestID(t.Context(), workerServiceTenant, fixture.run.RequestID)
			if response.Code != http.StatusOK || err != nil || grade.ModelConfigID != model.ConfigID || grade.ModelName != model.ModelName {
				t.Fatalf("callback lost trusted identity: status=%d grade=%s/%s err=%v body=%s", response.Code, grade.ModelConfigID, grade.ModelName, err, response.Body.String())
			}
			// 已完成回调沿用首次结果；后来换选不能让相同租约重新读取配置或重复结算。
			handler.WithImageModelResolver(func(context.Context, string) (*ImageModelConfig, error) {
				t.Fatal("completed callback resolved the model again")
				return nil, nil
			})
			if response = complete(); response.Code != http.StatusOK {
				t.Fatalf("identical callback did not replay: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
