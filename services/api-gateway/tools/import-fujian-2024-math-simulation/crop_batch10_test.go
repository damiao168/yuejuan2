package main

import (
	"context"
	"database/sql/driver"
	"net/http"
	"testing"
)

func TestBatch10CropRepairRejectsInvalidEvidenceBeforeUpload(t *testing.T) {
	// 所有页面仍先解码；每条分段的配准、考生、区域和页号前提必须在哈希跳过前核对。
	tests := []struct {
		name, candidate, question, registration, wantError string
	}{
		{name: "missing_registration", candidate: "C001", question: "Q1", wantError: "segment segment has no completed registration"},
		{name: "unknown_candidate", candidate: "unknown", question: "Q1", registration: "registration", wantError: "segment references unknown candidate unknown"},
		{name: "missing_region", candidate: "C001", question: "Q1", registration: "registration", wantError: "missing region for C001/Q1"},
		{name: "missing_second_page", candidate: "C001", question: "Q17", registration: "registration", wantError: "missing page 2 for C001/Q17"},
		{name: "invalid_crop", candidate: "C001", question: "Q1", registration: "registration", wantError: "crop C001/Q1: crop region must have positive width and height"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, data := batch9DatasetFixture(t)
			data.Candidates, data.Questions = data.Candidates[:1], data.Questions[:1]
			if test.name == "missing_region" {
				delete(data.Regions["C001"], "Q1")
			}
			if test.name == "missing_second_page" {
				data.Candidates[0].PageFiles = data.Candidates[0].PageFiles[:1]
			}
			if test.name == "invalid_crop" {
				data.Regions["C001"]["Q1"] = region{Width: 0, Height: .2}
			}
			script := &batch9SQLScript{queryRows: [][]driver.Value{{"segment", test.candidate, test.question, test.registration, "old-hash", "answer_segment_crop", test.registration}}}
			calls := 0
			client := batch9Client(func(request *http.Request) (*http.Response, error) {
				calls++
				t.Fatalf("invalid evidence reached upload: %s", request.URL.Path)
				return nil, nil
			})
			result, err := repairExistingCrops(context.Background(), cfg, data, client, openBatch9DB(t, script), "tenant", "exam")
			assertBatch9Error(t, err, test.wantError)
			if result.Found != 1 || result.Skipped != 0 || result.Updated != 0 || calls != 0 || script.execCount != 0 {
				t.Fatalf("invalid evidence mutated output: %#v, calls=%d, SQL=%#v", result, calls, script.events)
			}
			logBatch9Proof(t, test.name, map[string]any{"result": result, "error": batch9ErrorText(err), "calls": calls, "sql": script.events})
		})
	}
}
