package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

func TestBatch35MathEvidencePreservesCropBindingErrors(t *testing.T) {
	cases := []mathEvidenceCropBindingCase{
		{name: "legacy optional crop"},
		{name: "required store missing", requireCrop: true, wantErr: ErrActiveCropUnavailable},
		{name: "query error", store: true, storeErr: errors.New("crop lookup failed"), wantErr: ErrActiveCropUnavailable},
		{name: "missing hash before identity", store: true, mutate: func(crop *segment.SegmentEvidence) {
			crop.CropSHA256, crop.SegmentID = " ", "wrong-segment"
		}, wantErr: ErrActiveCropUnavailable},
		{name: "segment mismatch", store: true, mutate: func(crop *segment.SegmentEvidence) { crop.SegmentID = "wrong-segment" }, wantErr: mathunderstanding.ErrRevisionConflict},
		{name: "question mismatch", store: true, mutate: func(crop *segment.SegmentEvidence) { crop.QuestionID = "wrong-question" }, wantErr: mathunderstanding.ErrRevisionConflict},
		{name: "hash mismatch", store: true, mutate: func(crop *segment.SegmentEvidence) { crop.CropSHA256 = strings.Repeat("0", 64) }, wantErr: mathunderstanding.ErrRevisionConflict},
		{name: "matching normalized hash", requireCrop: true, store: true, mutate: func(crop *segment.SegmentEvidence) {
			crop.CropSHA256 = " " + strings.ToUpper(crop.CropSHA256) + " "
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertMathEvidenceCropBindingErrors(t, tc)
		})
	}
}

func TestBatch35MathEvidencePreservesVerificationQuality(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*mathunderstanding.CreateArtifactInput)
		want   float64
	}{
		{"missing verification", func(input *mathunderstanding.CreateArtifactInput) { input.Verifications = nil }, 0},
		{"uncertain verification", func(input *mathunderstanding.CreateArtifactInput) { input.Verifications[0].Status = "uncertain" }, 0},
		{"minimum verification confidence", func(input *mathunderstanding.CreateArtifactInput) {
			check := input.Verifications[0]
			check.ID, check.Confidence = "syntax-2", .45
			input.Verifications = append(input.Verifications, check)
		}, .45},
		{"contradicted retains confidence", func(input *mathunderstanding.CreateArtifactInput) {
			input.Verifications[0].Status, input.Verifications[0].Confidence = "contradicted", .6
		}, .6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, source, artifacts, input := mathGradingFixture(t)
			tc.mutate(&input)
			batch35PublishVerifiedMath(t, artifacts, input)
			evidence, err := source.Prepare(context.Background(), "tenant-a", value)
			if err != nil || evidence == nil {
				t.Fatalf("evidence unavailable: %#v %v", evidence, err)
			}
			if evidence.Quality.Verification != tc.want || evidence.Quality.Critical != minMathEvidenceQuality(evidence.Quality) {
				t.Fatalf("verification or critical formula changed: %#v", evidence.Quality)
			}
		})
	}
}

func TestBatch35MathEvidenceProjectionKeepsRevisionAndSanitizedLinks(t *testing.T) {
	value, source, artifacts, input := mathGradingFixture(t)
	step := input.SolutionGraph.Steps[0]
	step.ID, step.OrderHint, step.Confidence = "step-2", 2, .79
	input.SolutionGraph.Steps = append(input.SolutionGraph.Steps, step)
	diagram := input.Blocks[0]
	diagram.ID, diagram.Kind = "diagram-2", "diagram"
	input.Blocks = append(input.Blocks, diagram)
	value.Rubric.Points[0].EvidenceRequirements = []paper.EvidenceRequirement{{Type: "concept", Target: "factorization"}}
	artifact := batch35PublishVerifiedMath(t, artifacts, input)
	evidence, err := source.Prepare(context.Background(), "tenant-a", value)
	if err != nil || evidence == nil {
		t.Fatalf("evidence unavailable: %#v %v", evidence, err)
	}
	if evidence.ArtifactID != artifact.ID || evidence.ArtifactVersion != 3 || evidence.CorrectionRevision != 2 || evidence.ScoringVersion != MathScoringVersionV1 {
		t.Fatalf("verified version binding changed: %#v", evidence)
	}
	if !evidence.HasDiagram || !evidence.GraphUncertain || !evidence.RequiredRubricUncertain {
		t.Fatalf("review flags lost: %#v", evidence)
	}
	if len(evidence.Steps) != 2 || len(evidence.Formulas) != 1 || !reflect.DeepEqual(evidence.Formulas[0].StepIDs, []string{"step-1", "step-2"}) {
		t.Fatalf("step/formula links lost their order: %#v", evidence)
	}
	if evidence.effective.BaseArtifact.ID != artifact.ID || evidence.frozen.SnapshotID != value.AssessmentSnapshot.ID || evidence.cropInputHash != input.InputHash {
		t.Fatal("private settlement binding was not retained")
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"ast"`, `"details"`, `"raw_latex"`, `"symbols"`, `"effective"`, `"frozen"`, `"baseline"`, `"cropInputHash"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("private math field leaked: %s", forbidden)
		}
	}
}

func batch35PublishVerifiedMath(t *testing.T, artifacts *mathunderstanding.MemoryStore, input mathunderstanding.CreateArtifactInput) mathunderstanding.Artifact {
	t.Helper()
	current, err := artifacts.GetLatestArtifact(context.Background(), "tenant-a", input.AnswerSegmentID)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := artifacts.CreateDerivedArtifact(context.Background(), "tenant-a", current.ID, 2, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

type mathEvidenceCropBindingCase struct {
	name        string
	requireCrop bool
	store       bool
	mutate      func(*segment.SegmentEvidence)
	storeErr    error
	wantErr     error
}

func assertMathEvidenceCropBindingErrors(t *testing.T, tc mathEvidenceCropBindingCase) {
	t.Helper()
	value, source, _, input := mathGradingFixture(t)
	source.requireCrop = tc.requireCrop
	var cropStore *activeCropEvidenceFake
	if tc.store {
		crop := segment.SegmentEvidence{SegmentID: value.SegmentID, QuestionID: value.Question.ID, CropSHA256: input.InputHash}
		if tc.mutate != nil {
			tc.mutate(&crop)
		}
		cropStore = &activeCropEvidenceFake{
			tenantID: "tenant-a", segmentID: value.SegmentID, questionID: value.Question.ID,
			values: []segment.SegmentEvidence{crop}, err: tc.storeErr,
		}
		source.Crops = cropStore
	}
	evidence, err := source.Prepare(context.Background(), "tenant-a", value)
	if !errors.Is(err, tc.wantErr) {
		t.Fatalf("want %v, got %v", tc.wantErr, err)
	}
	if tc.wantErr == nil && evidence == nil || tc.wantErr != nil && evidence != nil {
		t.Fatalf("crop binding result changed: evidence=%#v err=%v", evidence, err)
	}
	if cropStore != nil && cropStore.calls != 1 {
		t.Fatalf("expected one tenant/question-bound crop lookup, got %d", cropStore.calls)
	}
}
