package mathunderstanding

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestCorrectionsAreVersionBoundAndTenantIsolated(t *testing.T) {
	artifacts := NewMemoryStore()
	artifact, err := artifacts.CreateArtifact(context.Background(), "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryCorrectionStore(artifacts)
	input := CreateCorrectionInput{ExpectedArtifactVersion: artifact.Version, Reason: "formula recognition correction", Operations: []CorrectionOperation{{Type: "correct_formula", TargetID: "formula-1", Payload: map[string]any{"canonical_latex": "x=3"}}}, CorrectedContract: validInput()}
	item, err := store.CreateCorrection(context.Background(), "tenant-a", artifact.ID, "teacher-1", input)
	if err != nil || item.Revision != 1 {
		t.Fatalf("create correction: %#v %v", item, err)
	}
	if items, _ := store.ListCorrections(context.Background(), "tenant-b", artifact.ID); len(items) != 0 {
		t.Fatal("cross tenant correction leaked")
	}
	input.ExpectedArtifactVersion++
	if _, err = store.CreateCorrection(context.Background(), "tenant-a", artifact.ID, "teacher-1", input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale correction accepted: %v", err)
	}
}

func TestResolveEffectiveArtifactProjectsLatestCorrection(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	corrections := NewMemoryCorrectionStore(artifacts)

	effective, err := ResolveEffectiveArtifact(ctx, artifacts, corrections, "tenant-a", base.AnswerSegmentID)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Corrected || effective.CorrectionRevision != 0 || effective.EffectiveContract.Formulas[0].CanonicalLatex != base.Formulas[0].CanonicalLatex {
		t.Fatalf("unexpected base projection: %#v", effective)
	}
	if err := ValidateCreateArtifact(effective.EffectiveContract); err != nil {
		t.Fatalf("base projection lost immutable identity: %v", err)
	}

	first := validInput()
	first.Formulas[0].CanonicalLatex = "x=2"
	second := validInput()
	second.Formulas[0].CanonicalLatex = "x=3"
	second.SolutionGraph.Steps[0].NormalizedText = "teacher corrected step"
	second.Blocks[0].Normalized = "teacher corrected block"
	for index, corrected := range []CreateArtifactInput{first, second} {
		_, err = corrections.CreateCorrection(ctx, "tenant-a", base.ID, "teacher-1", CreateCorrectionInput{
			ExpectedArtifactVersion: base.Version,
			Reason:                  "formula correction",
			Operations:              []CorrectionOperation{{Type: "correct_formula", TargetID: "formula-1", Payload: map[string]any{"revision": index + 1}}},
			CorrectedContract:       corrected,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	effective, err = ResolveEffectiveArtifact(ctx, artifacts, corrections, "tenant-a", base.AnswerSegmentID)
	if err != nil {
		t.Fatal(err)
	}
	if !effective.Corrected || effective.CorrectionRevision != 2 {
		t.Fatalf("latest correction was not selected: %#v", effective)
	}
	if got := effective.EffectiveContract.Formulas[0].CanonicalLatex; got != "x=3" {
		t.Fatalf("effective formula = %q, want x=3", got)
	}
	if got := effective.BaseArtifact.Formulas[0].CanonicalLatex; got == "x=3" {
		t.Fatal("immutable base artifact was mutated by projection")
	}
	if effective.EffectiveContract.SolutionGraph.Steps[0].NormalizedText != "teacher corrected step" ||
		effective.EffectiveContract.Blocks[0].Normalized != "teacher corrected block" {
		t.Fatal("projection applied only formulas, not the full corrected contract")
	}

	newInput := validInput()
	newInput.InputHash = "sha256:new-crop"
	if _, err = artifacts.CreateArtifact(ctx, "tenant-a", newInput); err != nil {
		t.Fatal(err)
	}
	effective, err = ResolveEffectiveArtifact(ctx, artifacts, corrections, "tenant-a", base.AnswerSegmentID)
	if err != nil || effective.Corrected || effective.CorrectionRevision != 0 || effective.BaseArtifact.Version != 2 {
		t.Fatalf("old artifact correction was applied to the new version: %#v %v", effective, err)
	}
}

func TestResolveEffectiveArtifactDoesNotCrossTenantBoundary(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	corrections := NewMemoryCorrectionStore(artifacts)
	corrected := validInput()
	corrected.Formulas[0].CanonicalLatex = "x=9"
	if _, err = corrections.CreateCorrection(ctx, "tenant-a", base.ID, "teacher-1", CreateCorrectionInput{
		ExpectedArtifactVersion: base.Version,
		Reason:                  "formula correction",
		Operations:              []CorrectionOperation{{Type: "correct_formula", TargetID: "formula-1", Payload: map[string]any{}}},
		CorrectedContract:       corrected,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveEffectiveArtifact(ctx, artifacts, corrections, "tenant-b", base.AnswerSegmentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant projection returned %v", err)
	}
}

func TestCorrectionsRejectConcurrentStaleProjection(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	corrections := NewMemoryCorrectionStore(artifacts)
	revision := int64(0)
	input := CreateCorrectionInput{
		ExpectedArtifactVersion: base.Version, ExpectedCorrectionRevision: &revision,
		Operations:        []CorrectionOperation{{Type: "correct_formula", TargetID: "formula-1", Payload: map[string]any{}}},
		CorrectedContract: validInput(),
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, createErr := corrections.CreateCorrection(ctx, "tenant-a", base.ID, "teacher-1", input)
			results <- createErr
		}()
	}
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrRevisionConflict):
			conflicted++
		default:
			t.Fatalf("unexpected correction error: %v", result)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("same projection edits: %d accepted, %d conflicted", succeeded, conflicted)
	}
}

func TestCorrectionCannotRebindSourceOrSupersededArtifact(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	corrections := NewMemoryCorrectionStore(artifacts)
	input := CreateCorrectionInput{
		ExpectedArtifactVersion: base.Version,
		Operations:              []CorrectionOperation{{Type: "correct_formula", TargetID: "formula-1", Payload: map[string]any{}}},
		CorrectedContract:       validInput(),
	}
	for _, mutate := range []func(*CreateArtifactInput){
		func(c *CreateArtifactInput) { c.InputHash = "another-crop" },
		func(c *CreateArtifactInput) { c.SubjectCode = "chemistry" },
		func(c *CreateArtifactInput) { c.EngineVersion = "another-engine" },
	} {
		input.CorrectedContract = validInput()
		mutate(&input.CorrectedContract)
		if _, err = corrections.CreateCorrection(ctx, "tenant-a", base.ID, "teacher-1", input); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("correction changed immutable source binding: %v", err)
		}
	}
	newInput := validInput()
	newInput.InputHash = "new-crop"
	if _, err = artifacts.CreateArtifact(ctx, "tenant-a", newInput); err != nil {
		t.Fatal(err)
	}
	input.CorrectedContract = validInput()
	if _, err = corrections.CreateCorrection(ctx, "tenant-a", base.ID, "teacher-1", input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("correction edited a superseded artifact: %v", err)
	}
}

func TestCorrectionReadsAndWritesDoNotMutateAuditLog(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	corrections := NewMemoryCorrectionStore(artifacts)
	input := CreateCorrectionInput{
		ExpectedArtifactVersion: base.Version,
		Operations:              []CorrectionOperation{{Type: "correct_formula", TargetID: "formula-1", Payload: map[string]any{"after": "x=3"}}},
		CorrectedContract:       validInput(),
	}
	input.CorrectedContract.Formulas[0].CanonicalLatex = "x=3"
	created, err := corrections.CreateCorrection(ctx, "tenant-a", base.ID, "teacher-1", input)
	if err != nil {
		t.Fatal(err)
	}
	input.Operations[0].Payload["after"] = "mutated input"
	created.Operations[0].Payload["after"] = "mutated output"
	created.CorrectedContract.Formulas[0].CanonicalLatex = "mutated output"
	listed, _ := corrections.ListCorrections(ctx, "tenant-a", base.ID)
	listed[0].CorrectedContract.Formulas[0].CanonicalLatex = "mutated history"
	latest, err := corrections.GetLatestCorrection(ctx, "tenant-a", base.ID)
	if err != nil || latest.Operations[0].Payload["after"] != "x=3" || latest.CorrectedContract.Formulas[0].CanonicalLatex != "x=3" {
		t.Fatalf("append-only audit was mutated through aliasing: %#v %v", latest, err)
	}
}

type latestCorrectionStub struct {
	CorrectionStore
	err        error
	correction Correction
}

func (s latestCorrectionStub) GetCorrection(_ context.Context, _ string, _ string, revision int64) (Correction, error) {
	if s.err != nil || s.correction.Revision != revision {
		if s.err != nil {
			return Correction{}, s.err
		}
		return Correction{}, ErrNotFound
	}
	return s.correction, nil
}

func (s latestCorrectionStub) GetLatestCorrection(context.Context, string, string) (Correction, error) {
	return s.correction, s.err
}

func TestEffectiveProjectionFailsClosedOnLatestCorrectionFailure(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	databaseError := errors.New("correction database unavailable")
	if _, err = ResolveEffectiveArtifact(ctx, artifacts, latestCorrectionStub{err: databaseError}, "tenant-a", base.AnswerSegmentID); !errors.Is(err, databaseError) {
		t.Fatalf("correction read failure silently fell back to stale base: %v", err)
	}
	if _, err = ResolveEffectiveArtifact(ctx, artifacts, latestCorrectionStub{correction: Correction{
		TenantID: "tenant-b", ArtifactID: base.ID, Revision: 1, CorrectedContract: validInput(),
	}}, "tenant-a", base.AnswerSegmentID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mismatched correction was projected: %v", err)
	}
	// 解析器必须使用专门的最新记录查询，不能使用有数量上限的列表。
	effective, err := ResolveEffectiveArtifact(ctx, artifacts, latestCorrectionStub{correction: Correction{
		TenantID: "tenant-a", ArtifactID: base.ID, Revision: 501, CorrectedContract: validInput(),
	}}, "tenant-a", base.AnswerSegmentID)
	if err != nil || effective.CorrectionRevision != 501 {
		t.Fatalf("projection was limited by historical list pagination: %#v %v", effective, err)
	}
}
