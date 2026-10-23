package subjective

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func imageLedgerTx(t *testing.T, steps ...imageSQLStep) *sql.Tx {
	t.Helper()
	steps = append(append([]imageSQLStep{{kind: "begin"}}, steps...), imageSQLStep{kind: "rollback"})
	// t.Context 会先于 Cleanup 取消并触发异步回滚；夹具由下面的显式回滚结束事务，再核对全部 SQL 步骤。
	tx, err := imageTestDB(t, steps...).BeginTx(context.WithoutCancel(t.Context()), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func imageLedgerGrade(execution *ImagePanelExecution) Grade {
	return Grade{QuestionID: "q1", AnswerSegmentID: "segment", RubricVersion: "rubric-v1", AdapterRequestID: "parent-request", RawOutput: map[string]any{"panel_execution": execution}}
}

func imageLedgerSteps(t *testing.T, grade Grade, role ImageRoleResult) []imageSQLStep {
	t.Helper()
	output := role.Output
	telemetry, usage := output.Telemetry, output.Telemetry.Usage
	status := "succeeded"
	if role.Status != "succeeded" {
		status = "failed"
	}
	check := func(want []any) func(string, []driver.NamedValue) error {
		return func(_ string, args []driver.NamedValue) error {
			got := make([]any, len(args))
			for i, arg := range args {
				got[i] = arg.Value
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("role %s ledger identity or usage changed: got=%v want=%v", role.Role, got, want)
			}
			return nil
		}
	}
	return []imageSQLStep{
		{kind: "exec", contains: "INSERT INTO model_usage_event", check: check([]any{"tenant-1", output.RequestID, role.Role, role.Model.ProviderKey, role.Model.ModelName, int64(telemetry.Attempts), usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens, usage.ReasoningTokens, usage.TotalTokens, status, telemetry.Deployment, grade.QuestionID, grade.AnswerSegmentID, grade.AdapterRequestID})},
		{kind: "exec", contains: "INSERT INTO model_call_fact", check: check([]any{"tenant-1", output.RequestID, grade.AnswerSegmentID, grade.QuestionID, role.Model.ProviderKey, telemetry.Deployment, role.Model.AdapterType, role.Model.ModelVersion, role.Model.ModelConfigID, role.Model.ModelName, output.PromptVersion, grade.RubricVersion, output.CapabilityProfile, telemetry.Region, status, int64(telemetry.Attempts), telemetry.ElapsedMS, usage.InputTokens, usage.OutputTokens, role.ErrorCode})},
	}
}

func TestImagePanelUsageRecordsEachAttemptedModelAndFailureSeparately(t *testing.T) {
	_, _, output := imagePanelFixture(t, "dual_conditional_arbiter", map[string]float64{"primary_a": 6, "primary_b": 12, "arbiter": 12})
	execution := output.PanelExecution
	grade := imageLedgerGrade(execution)
	steps := []imageSQLStep{}
	for i := range execution.RoleResults {
		role := &execution.RoleResults[i]
		role.Output.Telemetry.Attempts = i%2 + 1
		role.Output.Telemetry.ElapsedMS = int64(100 + i)
		role.Output.Telemetry.Usage = ModelTokenUsage{InputTokens: int64(10 + i), OutputTokens: int64(3 + i), CachedInputTokens: int64(i), ReasoningTokens: 2, TotalTokens: int64(13 + i*2)}
		if role.Role == "arbiter" {
			role.Status, role.ErrorCode = "failed", "model_output_invalid"
		}
		steps = append(steps, imageLedgerSteps(t, grade, *role)...)
	}
	// 父请求只串联账本；每笔收费身份和失败状态仍属于实际发起请求的角色。
	if err := recordImagePanelCallsInTx(t.Context(), imageLedgerTx(t, steps...), "tenant-1", grade); err != nil {
		t.Fatal(err)
	}
}

func TestImagePanelUsageSkipsUnsentAndUnknownUsageWithoutInventingZero(t *testing.T) {
	for _, scenario := range []string{"no output", "not dispatched", "usage unavailable", "reported zero"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, output := imagePanelFixture(t, "single", map[string]float64{"single": 6})
			role := &output.PanelExecution.RoleResults[0]
			var steps []imageSQLStep
			switch scenario {
			case "no output":
				role.Output = nil
			case "not dispatched":
				role.Output.Telemetry.Attempts = 0
			case "usage unavailable":
				role.Output.Telemetry.ExecutionDiagnostics = &ExecutionDiagnostics{FinishOutcome: "unavailable", Attempts: 1, ProviderRequestCount: 1, UsageStatus: "unavailable"}
			case "reported zero":
				zero := int64(0)
				role.Output.Telemetry.ExecutionDiagnostics = &ExecutionDiagnostics{FinishOutcome: "completed", Attempts: 1, ProviderRequestCount: 1, UsageStatus: "reported", Usage: &DiagnosticTokenUsage{InputTokens: &zero, OutputTokens: &zero, TotalTokens: &zero}}
				steps = imageLedgerSteps(t, imageLedgerGrade(output.PanelExecution), *role)
			}
			if err := recordImagePanelCallsInTx(t.Context(), imageLedgerTx(t, steps...), "tenant-1", imageLedgerGrade(output.PanelExecution)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImagePanelUsagePropagatesLedgerAndMalformedPublicOutputFailures(t *testing.T) {
	for _, scenario := range []string{"usage write", "fact write", "marshal", "decode"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, output := imagePanelFixture(t, "single", map[string]float64{"single": 6})
			grade := imageLedgerGrade(output.PanelExecution)
			steps := imageLedgerSteps(t, grade, output.PanelExecution.RoleResults[0])
			injected := errors.New("ledger unavailable")
			switch scenario {
			case "usage write":
				steps[0].err, steps = injected, steps[:1]
			case "fact write":
				steps[1].err = injected
			case "marshal":
				grade.RawOutput["panel_execution"], steps = func() {}, nil
			case "decode":
				grade.RawOutput["panel_execution"], steps = "invalid execution", nil
			}
			err := recordImagePanelCallsInTx(t.Context(), imageLedgerTx(t, steps...), "tenant-1", grade)
			if err == nil || (scenario == "usage write" || scenario == "fact write") && !errors.Is(err, injected) {
				t.Fatalf("ledger failure was ignored: %v", err)
			}
		})
	}
}

func TestFailedImagePanelUsageAcceptsOnlyFrozenFailedRoleIdentity(t *testing.T) {
	for _, boundary := range []string{"valid", "read failure", "context JSON", "missing plan", "public marshal", "public decode", "wrong mode", "unknown role", "changed model", "success status", "request", "provider", "adapter", "deployment", "no output"} {
		t.Run(boundary, func(t *testing.T) {
			value, _, output := imagePanelFixture(t, "single", map[string]float64{"single": 6})
			execution := output.PanelExecution
			role := &execution.RoleResults[0]
			role.Status, role.ErrorCode = "failed", "model_output_invalid"
			run := GradingRun{ID: "ai-run", RequestID: "parent-request", QuestionID: "q1", AnswerSegmentID: "segment", RubricVersion: "rubric-v1"}
			var public any = execution
			query := imageQuery("SELECT image_context_json")
			switch boundary {
			case "missing plan":
				value.AIPlan = nil
			case "public marshal":
				public = func() {}
			case "public decode":
				public = "invalid execution"
			case "wrong mode":
				execution.ConfiguredMode = "dual"
			case "unknown role":
				role.Role = "unfrozen"
			case "changed model":
				role.Model.ModelConfigID = "another-connection"
			case "success status":
				role.Status = "succeeded"
			case "request":
				role.Output.RequestID = "another-parent:single"
			case "provider":
				role.Output.Telemetry.Provider = "another-provider"
			case "adapter":
				role.Output.Telemetry.Adapter = "another-adapter"
			case "deployment":
				role.Output.Telemetry.Deployment = "another-revision"
			case "no output":
				role.Output = nil
			}
			query.values = []driver.Value{imageJSON(t, value)}
			if boundary == "read failure" {
				query.err = errors.New("context unavailable")
			}
			if boundary == "context JSON" {
				query.values = []driver.Value{[]byte("{")}
			}
			steps := []imageSQLStep{query}
			if boundary == "valid" {
				steps = append(steps, imageLedgerSteps(t, imageLedgerGrade(execution), *role)...)
			}
			err := recordFailedImagePanelCallsInTx(t.Context(), imageLedgerTx(t, steps...), "tenant-1", run, public)
			if boundary == "valid" || boundary == "no output" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("untrusted failed role reached the usage ledger")
			} else if boundary == "read failure" && !errors.Is(err, query.err) {
				t.Fatalf("context read failure was lost: %v", err)
			} else if boundary != "read failure" && boundary != "public marshal" && !errors.Is(err, ErrInvalidModelOutput) {
				t.Fatalf("untrusted role did not fail identity validation: %v", err)
			}
		})
	}
}
