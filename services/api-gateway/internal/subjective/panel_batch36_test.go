package subjective

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

func TestPanelBatch36PolicyVersionASCIIBoundaries(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "empty", value: ""},
		{name: "upper", value: "A", valid: true},
		{name: "lower", value: "z", valid: true},
		{name: "digit", value: "0", valid: true},
		{name: "letters_digits", value: "Az09", valid: true},
		{name: "suffix_punctuation", value: "v1._:-", valid: true},
		{name: "128_bytes", value: strings.Repeat("A", 128), valid: true},
		{name: "129_bytes", value: strings.Repeat("A", 129)},
		{name: "leading_dot", value: ".v1"},
		{name: "leading_underscore", value: "_v1"},
		{name: "leading_colon", value: ":v1"},
		{name: "leading_hyphen", value: "-v1"},
		{name: "leading_space", value: " v1"},
		{name: "trailing_space", value: "v1 "},
		{name: "internal_space", value: "v 1"},
		{name: "unicode_first", value: "版v1"},
		{name: "unicode_suffix", value: "v1版"},
		{name: "nul_first", value: "\x00v1"},
		{name: "nul_suffix", value: "v1\x00"},
		{name: "newline", value: "v1\n"},
		{name: "slash", value: "v1/2"},
		{name: "invalid_utf8_first", value: "\xffv1"},
		{name: "invalid_utf8_suffix", value: "v1\xff"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validPanelPolicyVersion(test.value); got != test.valid {
				t.Fatalf("version %q: valid=%v, want %v", test.value, got, test.valid)
			}
		})
	}
}

func TestPanelBatch36OptionalScoreBounds(t *testing.T) {
	zero, midpoint, maximum := 0.0, 2.0, 4.0
	below, above := -0.01, 4.01
	nan, positiveInfinity, negativeInfinity := math.NaN(), math.Inf(1), math.Inf(-1)
	for _, field := range []struct {
		name string
		set  func(*GradingPanel, *float64)
	}{
		{name: "primary_a", set: func(panel *GradingPanel, score *float64) { panel.ScoreA = score }},
		{name: "primary_b", set: func(panel *GradingPanel, score *float64) { panel.ScoreB = score }},
		{name: "arbiter", set: func(panel *GradingPanel, score *float64) { panel.ScoreC = score }},
		{name: "resolved", set: func(panel *GradingPanel, score *float64) { panel.ResolvedScore = score }},
	} {
		t.Run(field.name, func(t *testing.T) {
			for _, test := range []struct {
				name  string
				score *float64
				valid bool
			}{
				{name: "nil", valid: true},
				{name: "zero", score: &zero, valid: true},
				{name: "midpoint", score: &midpoint, valid: true},
				{name: "maximum", score: &maximum, valid: true},
				{name: "below_zero", score: &below},
				{name: "above_maximum", score: &above},
				{name: "nan", score: &nan},
				{name: "positive_infinity", score: &positiveInfinity},
				{name: "negative_infinity", score: &negativeInfinity},
			} {
				t.Run(test.name, func(t *testing.T) {
					panel := GradingPanel{MaxScore: 4, Status: PanelPrimaryPending}
					field.set(&panel, test.score)
					err := validatePanelState(panel)
					if test.valid {
						if err != nil {
							t.Fatalf("optional score was rejected: %v", err)
						}
						return
					}
					if !errors.Is(err, ErrPanelConfiguration) {
						t.Fatalf("invalid optional score: err=%v", err)
					}
				})
			}
		})
	}
}

func TestPanelBatch36StateAndResolutionBoundaries(t *testing.T) {
	score := 2.0
	// 固定提取前的状态校验边界；NaN/+Inf MaxScore 的拒绝仍由原创建入口负责。
	for _, test := range []struct {
		name  string
		panel GradingPanel
		valid bool
	}{
		{name: "zero_maximum", panel: GradingPanel{MaxScore: 0, Status: PanelPrimaryPending}},
		{name: "negative_maximum", panel: GradingPanel{MaxScore: -1, Status: PanelPrimaryPending}},
		{name: "existing_nan_maximum", panel: GradingPanel{MaxScore: math.NaN(), Status: PanelPrimaryPending, ScoreA: &score}, valid: true},
		{name: "existing_infinite_maximum", panel: GradingPanel{MaxScore: math.Inf(1), Status: PanelPrimaryPending, ScoreA: &score}, valid: true},
		{name: "unknown_status", panel: GradingPanel{MaxScore: 4, Status: "unknown"}},
		{name: "resolved_missing_score", panel: GradingPanel{MaxScore: 4, Status: PanelResolved, ResolutionSource: ResolutionHuman}},
		{name: "resolved_unknown_source", panel: GradingPanel{MaxScore: 4, Status: PanelResolved, ResolvedScore: &score, ResolutionSource: "unknown"}},
		{name: "resolved_primary", panel: GradingPanel{MaxScore: 4, Status: PanelResolved, ResolvedScore: &score, ResolutionSource: ResolutionPrimaryConsensus}, valid: true},
		{name: "resolved_arbiter", panel: GradingPanel{MaxScore: 4, Status: PanelResolved, ResolvedScore: &score, ResolutionSource: ResolutionArbiter}, valid: true},
		{name: "resolved_human", panel: GradingPanel{MaxScore: 4, Status: PanelResolved, ResolvedScore: &score, ResolutionSource: ResolutionHuman}, valid: true},
		{name: "review_empty_source", panel: GradingPanel{MaxScore: 4, Status: PanelHumanReview}, valid: true},
		{name: "review_human_source", panel: GradingPanel{MaxScore: 4, Status: PanelHumanReview, ResolutionSource: ResolutionHuman}, valid: true},
		{name: "review_primary_source", panel: GradingPanel{MaxScore: 4, Status: PanelHumanReview, ResolutionSource: ResolutionPrimaryConsensus}},
		{name: "review_arbiter_source", panel: GradingPanel{MaxScore: 4, Status: PanelHumanReview, ResolutionSource: ResolutionArbiter}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePanelState(test.panel)
			if test.valid {
				if err != nil {
					t.Fatalf("existing state boundary was rejected: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrPanelConfiguration) {
				t.Fatalf("invalid state was accepted: %v", err)
			}
		})
	}
}

type panelBatch36ShadowStore struct {
	PanelShadowEvaluationStore
	existing []gradingevaluation.PanelObservation
	addCalls int
}

func (s *panelBatch36ShadowStore) ListPanelObservations(context.Context, string, string) ([]gradingevaluation.PanelObservation, error) {
	return s.existing, nil
}

func (s *panelBatch36ShadowStore) AddPanelObservation(ctx context.Context, tenantID, runID string, item gradingevaluation.PanelObservation) (gradingevaluation.PanelObservation, error) {
	s.addCalls++
	return s.PanelShadowEvaluationStore.AddPanelObservation(ctx, tenantID, runID, item)
}

func panelBatch36SavedObservation(sample PanelShadowSample) gradingevaluation.PanelObservation {
	resolved := 2.0
	return gradingevaluation.PanelObservation{
		ID: "saved-observation", ResponseKey: sample.ResponseKey, ResponseFingerprint: sample.ResponseFingerprint,
		EducationStage: string(sample.Context.AssessmentSnapshot.EducationStage),
		Subject:        string(sample.Context.AssessmentSnapshot.SubjectCode), Archetype: sample.Context.AssessmentSnapshot.ArchetypeCode,
		ReferenceKind: gradingevaluation.ReferenceHumanAdjudicated, ReferenceScore: sample.ReferenceScore,
		MaxScore: sample.Context.Question.Score, ScoreA: resolved, ScoreB: resolved, ResolvedScore: &resolved,
		ResolutionSource: ResolutionPrimaryConsensus, ReferenceReviewers: sample.ReferenceReviewers,
		ReferenceAdjudicated: sample.ReferenceAdjudicated, PrimaryACostMicros: sample.PrimaryACostMicros,
		PrimaryBCostMicros: sample.PrimaryBCostMicros, ArbiterCostMicros: sample.ArbiterCostMicros,
	}
}

func panelBatch36FreshSample(sample PanelShadowSample) PanelShadowSample {
	sample.ResponseKey = "opaque-response-2"
	sample.ResponseFingerprint = strings.Repeat("c", 64)
	sample.Context.SegmentID = "segment-2"
	return sample
}

func panelBatch36BadSample(sample PanelShadowSample) PanelShadowSample {
	sample = panelBatch36FreshSample(sample)
	sample.ReferenceAdjudicated = false
	return sample
}

func panelBatch36ReferenceConflict(sample PanelShadowSample) gradingevaluation.PanelObservation {
	saved := panelBatch36SavedObservation(sample)
	saved.ReferenceScore++
	return saved
}

func TestPanelBatch36ShadowPreflightErrorOrder(t *testing.T) {
	// 同时安排多个失败条件，保护首个错误类别以及整批检查早于模型和写入的顺序。
	for _, test := range []struct {
		name  string
		setup func(PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation)
		want  error
	}{
		{name: "late_invalid_sample", want: ErrInvalidInput, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			return []PanelShadowSample{sample, panelBatch36BadSample(sample)}, nil
		}},
		{name: "duplicate_key", want: ErrInvalidInput, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			duplicate := panelBatch36FreshSample(sample)
			duplicate.ResponseKey = sample.ResponseKey
			return []PanelShadowSample{sample, duplicate}, nil
		}},
		{name: "duplicate_fingerprint", want: ErrInvalidInput, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			duplicate := panelBatch36FreshSample(sample)
			duplicate.ResponseFingerprint = sample.ResponseFingerprint
			return []PanelShadowSample{sample, duplicate}, nil
		}},
		{name: "saved_reference_conflict", want: gradingevaluation.ErrConflict, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			return []PanelShadowSample{sample}, []gradingevaluation.PanelObservation{panelBatch36ReferenceConflict(sample)}
		}},
		{name: "invalid_before_saved_reference", want: ErrInvalidInput, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			saved := panelBatch36ReferenceConflict(sample)
			sample.ReferenceReviewers = 1
			return []PanelShadowSample{sample}, []gradingevaluation.PanelObservation{saved}
		}},
		{name: "fingerprint_before_saved_reference", want: ErrInvalidInput, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			saved := panelBatch36ReferenceConflict(sample)
			saved.ResponseFingerprint = strings.Repeat("d", 64)
			other := panelBatch36SavedObservation(sample)
			other.ResponseKey = "opaque-other"
			return []PanelShadowSample{sample}, []gradingevaluation.PanelObservation{saved, other}
		}},
		{name: "first_reference_before_later_invalid", want: gradingevaluation.ErrConflict, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			return []PanelShadowSample{sample, panelBatch36BadSample(sample)}, []gradingevaluation.PanelObservation{panelBatch36ReferenceConflict(sample)}
		}},
		{name: "first_invalid_before_later_reference", want: ErrInvalidInput, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			return []PanelShadowSample{panelBatch36BadSample(sample), sample}, []gradingevaluation.PanelObservation{panelBatch36ReferenceConflict(sample)}
		}},
		{name: "existing_fingerprint_before_invalid", want: gradingevaluation.ErrConflict, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			saved := panelBatch36SavedObservation(sample)
			other := saved
			other.ResponseKey = "opaque-other"
			return []PanelShadowSample{panelBatch36BadSample(sample)}, []gradingevaluation.PanelObservation{saved, other}
		}},
		{name: "saved_replay_before_later_invalid", want: ErrInvalidInput, setup: func(sample PanelShadowSample) ([]PanelShadowSample, []gradingevaluation.PanelObservation) {
			return []PanelShadowSample{sample, panelBatch36BadSample(sample)}, []gradingevaluation.PanelObservation{panelBatch36SavedObservation(sample)}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner, store, a, sample, runID := shadowRunnerFixture(t)
			samples, existing := test.setup(sample)
			recording := &panelBatch36ShadowStore{PanelShadowEvaluationStore: store, existing: existing}
			runner.store = recording
			items, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, PanelDecisionConfig{PolicyVersion: "shadow-v1"}, samples)
			if !errors.Is(err, test.want) || items != nil {
				t.Fatalf("preflight result: items=%#v err=%v, want %v", items, err, test.want)
			}
			b := runner.panel.agents.PrimaryB.Adapter.(*panelTestAdapter)
			c := runner.panel.agents.Arbiter.Adapter.(*panelTestAdapter)
			if len(a.inputs)+len(b.inputs)+len(c.inputs) != 0 || recording.addCalls != 0 {
				t.Fatalf("preflight dispatched work: A=%d B=%d C=%d writes=%d", len(a.inputs), len(b.inputs), len(c.inputs), recording.addCalls)
			}
		})
	}
}

func TestPanelBatch36ShadowReplayPreservesSampleOrder(t *testing.T) {
	runner, store, a, sample, runID := shadowRunnerFixture(t)
	saved := panelBatch36SavedObservation(sample)
	fresh := panelBatch36FreshSample(sample)
	recording := &panelBatch36ShadowStore{PanelShadowEvaluationStore: store, existing: []gradingevaluation.PanelObservation{saved}}
	runner.store = recording
	items, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, PanelDecisionConfig{PolicyVersion: "shadow-v1"}, []PanelShadowSample{fresh, sample})
	if err != nil || len(items) != 2 {
		t.Fatalf("mixed replay failed: items=%#v err=%v", items, err)
	}
	if items[0].ResponseKey != fresh.ResponseKey || items[1].ID != saved.ID || items[1].ResponseKey != sample.ResponseKey {
		t.Fatalf("sample order or saved identity changed: %#v", items)
	}
	b := runner.panel.agents.PrimaryB.Adapter.(*panelTestAdapter)
	c := runner.panel.agents.Arbiter.Adapter.(*panelTestAdapter)
	if len(a.inputs) != 1 || len(b.inputs) != 1 || len(c.inputs) != 0 || recording.addCalls != 1 {
		t.Fatalf("saved replay repeated work: A=%d B=%d C=%d writes=%d", len(a.inputs), len(b.inputs), len(c.inputs), recording.addCalls)
	}
}
