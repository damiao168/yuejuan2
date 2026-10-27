package ocr

import (
	"context"
	"errors"
	"testing"
)

func completionRemediationInput() CompleteTaskInput {
	return CompleteTaskInput{ModelVersion: "fixture-model", ConfigHash: "cfg", InputHash: "input", PreprocessProfile: "unchanged", WorkerID: "synthetic", DurationMS: 10,
		Results: []ResultInput{{SubmissionPageID: "page", Text: "first", BBox: []float64{1, 2, 3, 4}, Confidence: .912345}, {SubmissionPageID: "page", Text: "second", BBox: []float64{5, 6, 7, 8}, Confidence: .923456}}}
}

func TestCompletionIdentityIgnoresOrderButPreservesEverySourceFact(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	task, err := store.CreateTask(ctx, "tenant", "submission", "actor", CreateTaskInput{Engine: "fixture", EngineVersion: "v1", MinConfidence: .8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.StartTask(ctx, "tenant", task.ID); err != nil {
		t.Fatal(err)
	}
	input := completionRemediationInput()
	completed, err := store.CompleteTask(ctx, "tenant", task.ID, input)
	if err != nil || completed.CompletionPayloadHash == "" {
		t.Fatalf("complete: %#v %v", completed, err)
	}
	input.Results[0], input.Results[1] = input.Results[1], input.Results[0]
	if _, err = store.CompleteTask(ctx, "tenant", task.ID, input); err != nil {
		t.Fatalf("same reordered input rejected: %v", err)
	}
	for _, change := range []struct {
		name   string
		mutate func(*CompleteTaskInput)
	}{
		{"subprecision confidence", func(i *CompleteTaskInput) { i.Results[0].Confidence += .000001 }},
		{"text", func(i *CompleteTaskInput) { i.Results[0].Text += " changed" }},
		{"bbox", func(i *CompleteTaskInput) { i.Results[0].BBox[0] += .001 }},
		{"source image", func(i *CompleteTaskInput) { i.Results[0].SourceImageFileID = "different" }},
		{"profile", func(i *CompleteTaskInput) { i.PreprocessProfile = "different" }},
		{"model", func(i *CompleteTaskInput) { i.ModelVersion = "different" }},
		{"duration", func(i *CompleteTaskInput) { i.DurationMS++ }},
		{"duplicate count", func(i *CompleteTaskInput) { i.Results = append(i.Results, i.Results[0]) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := completionRemediationInput()
			change.mutate(&changed)
			if _, err := store.CompleteTask(ctx, "tenant", task.ID, changed); !errors.Is(err, ErrResultConflict) {
				t.Fatalf("changed input accepted: %v", err)
			}
		})
	}
}

func TestHistoricalCompletionUsesStoredPrecisionAndMultiset(t *testing.T) {
	input := completionRemediationInput()
	task := Task{ModelVersion: input.ModelVersion, ConfigHash: input.ConfigHash, InputHash: input.InputHash, WorkerID: input.WorkerID, DurationMS: input.DurationMS}
	for _, result := range input.Results {
		task.Results = append(task.Results, Result{SubmissionPageID: result.SubmissionPageID, Text: result.Text, BBox: result.BBox, Confidence: .9123, PreprocessProfile: input.PreprocessProfile})
	}
	task.Results[1].Confidence = .9235
	task.Results[0], task.Results[1] = task.Results[1], task.Results[0]
	if !sameCompletion(task, input) {
		t.Fatal("historical reordered four-decimal record rejected")
	}
	input.Results[0].Text = "changed"
	if sameCompletion(task, input) {
		t.Fatal("history accepted changed text")
	}
}
