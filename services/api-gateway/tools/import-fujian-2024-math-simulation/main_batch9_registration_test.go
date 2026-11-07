package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

func openBatch9DB(t *testing.T, script *batch9SQLScript) *sql.DB {
	t.Helper()
	db := sql.OpenDB(batch9Connector{script: script})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBatch9RegistrationTransactions(t *testing.T) {
	tests := []struct {
		name, wantError string
		failAt          int
		commitErr       error
	}{
		{name: "success"},
		{name: "capture_file_failure", failAt: 1, wantError: "create capture file C001 page 2: injected"},
		{name: "capture_page_failure", failAt: 2, wantError: "create capture page C001 page 2: injected"},
		{name: "registration_failure", failAt: 3, wantError: "create registration C001 page 2: injected"},
		{name: "commit_failure", commitErr: errors.New("commit failed"), wantError: "commit failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &batch9SQLScript{commitErr: test.commitErr}
			if test.failAt > 0 {
				script.queryErr, script.queryFailAt = errors.New("injected"), test.failAt
			}
			db := openBatch9DB(t, script)
			id, err := createBatch9Registration(context.Background(), db, pageRecord{
				ID: "page", PageNo: 2, Path: "nested/page.png",
				FileAsset: map[string]any{"id": "source", "hash_sha256": "source-hash", "size_bytes": float64(321)},
			})
			assertBatch9Error(t, err, test.wantError)
			if test.wantError == "" && id != "id-3" || test.wantError != "" && id != "" {
				t.Fatalf("registration id = %q", id)
			}
			assertBatch9RegistrationTransaction(t, script, test.failAt)
			logBatch9Proof(t, test.name, map[string]any{"id": id, "error": batch9ErrorText(err), "sql": script.events})
		})
	}
}

func assertBatch9RegistrationTransaction(t *testing.T, script *batch9SQLScript, failAt int) {
	t.Helper()
	// 查询失败必须回滚；提交失败仍应留下已经尝试提交的调用事实。
	wantEnd := "commit"
	if failAt > 0 {
		wantEnd = "rollback"
	}
	if script.events[0].Kind != "begin" || script.events[len(script.events)-1].Kind != wantEnd {
		t.Fatalf("transaction = %#v", script.events)
	}
	wantArgs := []any{"tenant", "batch", "source", "page.png", "source-hash", int64(321), datasetID + ":C001:page:2", "admin"}
	if !reflect.DeepEqual(script.events[1].Args, wantArgs) {
		t.Fatalf("capture file bindings = %#v", script.events[1].Args)
	}
}

func TestBatch9RegistrationCompletion(t *testing.T) {
	for _, affected := range []int64{0, 1, 2} {
		t.Run(strconv.FormatInt(affected, 10), func(t *testing.T) {
			script := &batch9SQLScript{affected: affected}
			err := completeBatch9Registration(context.Background(), openBatch9DB(t, script), map[string]any{"id": "registered"})
			wantError, wantCount, wantEnd := "", 2, "commit"
			if affected != 1 {
				wantError, wantCount, wantEnd = fmt.Sprintf("complete registration affected %d rows", affected), 1, "rollback"
			}
			assertBatch9Error(t, err, wantError)
			if script.execCount != wantCount || script.events[len(script.events)-1].Kind != wantEnd || !reflect.DeepEqual(script.events[1].Args, []any{"tenant", "registration", "registered"}) {
				t.Fatalf("completion = %#v", script.events)
			}
			if affected == 1 && !reflect.DeepEqual(script.events[2].Args, []any{"tenant", "submission", "page", "template", "template-hash", "registration", int64(imageWidth), int64(imageHeight)}) {
				t.Fatalf("segment evidence bindings = %#v", script.events[2].Args)
			}
			logBatch9Proof(t, "completion", map[string]any{"error": batch9ErrorText(err), "sql": script.events})
		})
	}
}

func TestBatch9AnswerCandidateDecisions(t *testing.T) {
	tests := []struct {
		name, route, extractor, decision string
		text                             any
	}{
		{name: "automatic_blank_still_confirmed", route: "auto_confirm", extractor: "omr", decision: "confirmed", text: ""},
		{name: "blank", extractor: "ocr", decision: "blank", text: " \t"},
		{name: "multiple", extractor: "omr", decision: "multiple", text: []any{"A", "C"}},
		{name: "one_selection", extractor: "omr", decision: "ambiguous", text: []any{"A"}},
		{name: "typed_strings_remain_ambiguous", extractor: "ocr", decision: "ambiguous", text: []string{"A", "C"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &batch9SQLScript{}
			id, err := createBatch9AnswerCandidate(context.Background(), openBatch9DB(t, script), candidateAnswer{Text: test.text, Confidence: .88, Route: test.route, Synthetic: true}, test.extractor)
			if err != nil || id != "id-1" || len(script.events) != 1 {
				t.Fatalf("candidate = %q %v %#v", id, err, script.events)
			}
			args := script.events[0].Args
			if args[0] != "tenant" || args[1] != "segment" || args[2] != test.extractor || args[4] != displayText(test.text) || args[5] != .88 || args[6] != test.decision || args[11] != "admin" {
				t.Fatalf("candidate bindings = %#v", args)
			}
			logBatch9Proof(t, test.name, map[string]any{"id": id, "sql": script.events})
		})
	}
}

func TestBatch9ObjectiveGradeBindings(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(strconv.FormatBool(failed), func(t *testing.T) {
			script := &batch9SQLScript{affected: 1}
			wantError := ""
			if failed {
				script.execErr, wantError = errors.New("injected"), "confirm automatic objective grade: injected"
			}
			grade := map[string]any{"id": "grade", "grader_type": "rule", "rule_version": "v1", "confidence": .99, "risk_flags": []any{}, "suggested_score": 3.0, "max_score": 4.0}
			err := confirmBatch9ObjectiveGrade(context.Background(), openBatch9DB(t, script), grade)
			assertBatch9Error(t, err, wantError)
			args := script.events[0].Args
			if !reflect.DeepEqual(args[:9], []any{"tenant", "exam", "submission", "question", "segment", "candidate", "rule", 3.0, 4.0}) || args[10] != "admin" {
				t.Fatalf("grade bindings = %#v", args)
			}
			logBatch9Proof(t, "grade", map[string]any{"error": batch9ErrorText(err), "sql": script.events})
		})
	}
}
