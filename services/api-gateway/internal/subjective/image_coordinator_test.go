package subjective

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func coordinatorModel() *ImageModelConfig {
	return &ImageModelConfig{ConfigID: "model-1", AdapterType: "openai_compatible", ProviderKey: "fixture", BaseURL: "https://fixture.invalid/v1", APIKey: "synthetic-private-key", ModelName: "vision-model", ModelVersion: "vision-v1", ConfigRevision: "config-v1"}
}

func coordinatorPlan(model *ImageModelConfig) *grading.GradingAIPlan {
	return &grading.GradingAIPlan{Version: 1, EffectiveMode: "single", SingleModel: &grading.GradingAIModel{ModelConfigID: model.ConfigID, AdapterType: model.AdapterType, ProviderKey: model.ProviderKey, ModelName: model.ModelName, ModelVersion: model.ModelVersion, ConfigRevision: model.ConfigRevision, BindingRevision: "school-selection-v1"}}
}

func coordinatorHandler(t *testing.T, db *sql.DB, url string, model *ImageModelConfig) *Handler {
	t.Helper()
	h := NewHandler(NewPostgresStore(db), NewHTTPAdapter(HTTPAdapterConfig{BaseURL: url, Token: strings.Repeat("t", 32), ModelVersion: model.ModelVersion}), nil)
	h.WithImageModelResolver(func(context.Context, string) (*ImageModelConfig, error) {
		return nil, errors.New("frozen exam must not resolve the current school default")
	})
	h.WithImageModelByIDResolver(func(_ context.Context, tenant, id, revision string) (*ImageModelConfig, error) {
		if tenant != "tenant-1" || id != model.ConfigID || revision != "school-selection-v1" {
			return nil, fmt.Errorf("model escaped frozen school binding: %s/%s/%s", tenant, id, revision)
		}
		return model, nil
	})
	return h
}

func TestImageCoordinatorObservesGroupOnceWithoutTeacherReferenceAndProjectsMemberEvidence(t *testing.T) {
	crop := newActiveCropResolverFixture(t)
	model := coordinatorModel()
	members := []grading.ObservationMember{{SegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "1", QuestionType: "calculation"}, {SegmentID: "segment-2", QuestionID: "question-2", QuestionNo: "2", QuestionType: "short_answer"}}
	input := grading.ScoringObservationInput{ScoringRunID: "scoring-run", RequestID: "observe-once", InputVersionID: "input-frozen", SegmentID: "segment-1", CropSHA256: crop.evidence.CropSHA256, Members: members}
	memberQuery := imageQuery("SELECT iv.members_json", imageJSON(t, members))
	memberQuery.check = func(_ string, args []driver.NamedValue) error {
		if !reflect.DeepEqual([]any{args[0].Value, args[1].Value, args[2].Value, args[3].Value}, []any{"tenant-1", input.InputVersionID, input.SegmentID, input.CropSHA256}) {
			return errors.New("observation lost tenant, input or crop binding")
		}
		return nil
	}
	db := imageTestDB(t, imageQuery("rule_snapshot->'ai_plan'", imageJSON(t, coordinatorPlan(model))), memberQuery,
		imageQuery("rule_snapshot->'questions'", "冻结题干一"), imageQuery("rule_snapshot->'questions'", "冻结题干二"))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request imageAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		png, _ := base64.StdEncoding.DecodeString(request.StudentImage.Data)
		if r.URL.Path != "/grading/observe-image-v3" || request.Rubric != nil || request.FrozenReference != nil || request.AnswerText != "" || string(png) != string(crop.data) || len(request.Members) != 2 || request.Members[1].QuestionText != "冻结题干二" {
			t.Error("observation mixed teacher reference, OCR or a different crop into the grouped request")
		}
		observation := imageTestObservation("question-1", input.CropSHA256)
		observation.ModelBinding = imageModelBinding(model)
		observation.Members = append(observation.Members, ObservedMember{QuestionID: "question-2", Presence: "uncertain", Association: "unresolved", Confidence: .2})
		_ = json.NewEncoder(w).Encode(imageAgentResponse{SchemaVersion: ImageProtocolVersion, RequestID: request.RequestID, Observation: observation,
			Telemetry: gradingAgentTelemetry{Adapter: model.AdapterType, Provider: model.ProviderKey, Deployment: model.ConfigRevision, Region: "managed", Attempts: 1}})
	}))
	defer server.Close()
	h := coordinatorHandler(t, db, server.URL, model).WithImageCropResolver(crop.resolver)
	raw, err := h.ObserveStudentAnswers(t.Context(), "tenant-1", input)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Members []struct {
			SegmentID   string          `json:"answer_segment_id"`
			QuestionID  string          `json:"question_id"`
			Answer      *ObservedAnswer `json:"answer"`
			EvidenceIDs []string        `json:"evidence_ids"`
		} `json:"members"`
		Observations []struct {
			QuestionID string `json:"question_id"`
			CropSHA256 string `json:"crop_sha256"`
		} `json:"observations"`
		ModelInvoked bool `json:"model_invoked"`
	}
	if json.Unmarshal(raw, &result) != nil || calls.Load() != 1 || !result.ModelInvoked || len(result.Members) != 2 || len(result.Observations) != 1 {
		t.Fatalf("group did not keep one observation: calls=%d result=%s", calls.Load(), raw)
	}
	if result.Members[0].SegmentID != "segment-1" || result.Members[0].EvidenceIDs[0] != "student-step" || result.Members[1].SegmentID != "segment-2" || result.Members[1].Answer != nil || len(result.Members[1].EvidenceIDs) != 0 || result.Observations[0].QuestionID != "question-1" || result.Observations[0].CropSHA256 != input.CropSHA256 {
		t.Fatal("projection copied one member's answer or lost evidence ownership")
	}
}

func TestImageCoordinatorRejectsStaleCropOrFrozenMembersBeforeModelCall(t *testing.T) {
	for _, changed := range []string{"crop", "members"} {
		t.Run(changed, func(t *testing.T) {
			crop, model := newActiveCropResolverFixture(t), coordinatorModel()
			members := []grading.ObservationMember{{SegmentID: "segment-1", QuestionID: "question-1"}}
			input := grading.ScoringObservationInput{ScoringRunID: "run", RequestID: "observe-stale", InputVersionID: "input-frozen", SegmentID: "segment-1", CropSHA256: crop.evidence.CropSHA256, Members: members}
			steps := []imageSQLStep{imageQuery("rule_snapshot->'ai_plan'", imageJSON(t, coordinatorPlan(model))), imageQuery("SELECT iv.members_json", imageJSON(t, members))}
			want := ErrInvalidInput
			if changed == "crop" {
				input.CropSHA256 = strings.Repeat("b", 64)
				steps = append(steps, imageQuery("rule_snapshot->'questions'", "冻结题干"))
				want = ErrWorkerResultVersionConflict
			} else {
				input.Members = []grading.ObservationMember{{SegmentID: "another-segment", QuestionID: "question-1"}}
			}
			h := coordinatorHandler(t, imageTestDB(t, steps...), "http://invalid.invalid", model).WithImageCropResolver(crop.resolver)
			if _, err := h.ObserveStudentAnswers(t.Context(), "tenant-1", input); !errors.Is(err, want) {
				t.Fatalf("stale %s was accepted: %v", changed, err)
			}
		})
	}
}

func coordinatorFrozen(t *testing.T) (grading.ScoringAIInput, Context, map[string]any) {
	t.Helper()
	value := serviceTestContext()
	value.SegmentID, value.InputVersionID, value.AnswerVersion, value.CropSHA256 = "segment-1", "input-frozen", "input-frozen", "crop-hash"
	value.AssessmentSnapshot = assessment.ExamQuestionSnapshot{ID: "snapshot-1", ScoringPolicySnapshot: assessment.ScoringPolicy{Mode: assessment.ScoringAIAssist}}
	value.AIPlan = coordinatorPlan(coordinatorModel())
	observation := imageTestObservation(value.Question.ID, value.CropSHA256)
	observation.ModelBinding = imageModelBinding(coordinatorModel())
	value.Observation = &observation
	input := grading.ScoringAIInput{ScoringRunID: "scoring-run", SegmentID: value.SegmentID, InputVersionID: value.InputVersionID, CropSHA256: value.CropSHA256, AssessmentSnapshotID: value.AssessmentSnapshot.ID, IdempotencyKey: "image-command", Observation: imageJSON(t, map[string]any{"student_observation": observation})}
	frozen := map[string]any{"question": value.Question, "rubric": value.Rubric, "assessment_snapshot": value.AssessmentSnapshot, "answer_key": paper.AnswerKey{StandardAnswer: "冻结标准答案", EquivalentAnswers: []any{"等价结果"}}, "hash": "snapshot-hash"}
	return input, value, frozen
}

func TestImageCoordinatorFrozenMemberValidationAndDatabaseFailures(t *testing.T) {
	valid := []grading.ObservationMember{{SegmentID: "segment-1", QuestionID: "question-1"}}
	input := grading.ScoringObservationInput{ScoringRunID: "run", InputVersionID: "input-frozen", SegmentID: "segment-1", CropSHA256: "crop-hash", Members: valid}
	for _, tc := range []struct {
		name              string
		raw               []byte
		members           []grading.ObservationMember
		databaseErr, want error
	}{
		{"stale row", nil, valid, nil, ErrWorkerResultVersionConflict},
		{"database offline", nil, valid, errors.New("database offline"), nil},
		{"broken JSON", []byte("{"), valid, nil, ErrInvalidInput},
		{"empty snapshot", []byte("[]"), valid, nil, ErrInvalidInput},
		{"member count changed", imageJSON(t, valid), append(append([]grading.ObservationMember{}, valid...), valid[0]), nil, ErrInvalidInput},
		{"duplicate requested question", imageJSON(t, append(append([]grading.ObservationMember{}, valid...), valid[0])), append(append([]grading.ObservationMember{}, valid...), valid[0]), nil, ErrInvalidInput},
		{"member replaced", imageJSON(t, valid), []grading.ObservationMember{{SegmentID: "foreign", QuestionID: "question-1"}}, nil, ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := imageQuery("SELECT iv.members_json")
			if tc.raw != nil {
				step.values = []driver.Value{tc.raw}
			}
			step.err = tc.databaseErr
			store := NewPostgresStore(imageTestDB(t, step))
			candidate := input
			candidate.Members = tc.members
			_, err := store.imageObservationMembers(t.Context(), "tenant-1", candidate)
			want := tc.want
			if tc.databaseErr != nil {
				want = tc.databaseErr
			}
			if !errors.Is(err, want) {
				t.Fatalf("boundary accepted changed membership: %v", err)
			}
		})
	}
}

func TestImageCoordinatorRunContextRejectsChangedBindingOrMissingObservation(t *testing.T) {
	_, value, _ := coordinatorFrozen(t)
	run := GradingRun{ID: "ai-run", AnswerVersion: value.InputVersionID, ExamQuestionSnapshotID: value.AssessmentSnapshot.ID, QuestionID: value.Question.ID}
	for _, tc := range []struct {
		name    string
		current bool
		mutate  func(*Context)
		want    error
	}{
		{"current frozen context", true, func(*Context) {}, nil},
		{"cancelled or superseded", false, func(*Context) {}, ErrWorkerResultVersionConflict},
		{"input replaced", true, func(v *Context) { v.InputVersionID = "new-input" }, ErrWorkerResultVersionConflict},
		{"snapshot replaced", true, func(v *Context) { v.AssessmentSnapshot.ID = "new-snapshot" }, ErrWorkerResultVersionConflict},
		{"observation missing", true, func(v *Context) { v.Observation = nil }, ErrWorkerResultVersionConflict},
		{"evidence input changed", true, func(v *Context) { copy := *v.Observation; copy.InputVersionID = "new-input"; v.Observation = &copy }, ErrInvalidModelOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := value
			tc.mutate(&candidate)
			db := imageTestDB(t, imageQuery("SELECT r.image_context_json", imageJSON(t, candidate), true), imageQuery("SELECT EXISTS", tc.current))
			got, err := NewPostgresStore(db).LoadRunContext(t.Context(), "tenant-1", run)
			if !errors.Is(err, tc.want) {
				t.Fatalf("run context boundary: %v", err)
			}
			if err == nil && (got.InputVersionID != value.InputVersionID || got.Observation.Members[0].Answer.Value != value.Observation.Members[0].Answer.Value) {
				t.Fatal("worker context did not retain frozen observation")
			}
		})
	}
}
