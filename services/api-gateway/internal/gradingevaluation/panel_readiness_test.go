package gradingevaluation

import "testing"

func TestSeniorNineSubjectShadowReadinessRequiresEverySubjectAndStrongerArbiter(t *testing.T) {
	policy := PanelReadinessPolicy{
		PolicyVersion: "senior-panel-shadow-v1", MinSamplesPerSlice: 4, MinArbitrationsPerSlice: 2,
		MaxFalseConsensusRate: .01, MaxArbitrationErrorRate: .01, MaxResolvedMAE: .01, MinResolvedQWK: .99,
		MinArbiterMAEImprovement: .5, MinResolvedCoverageRate: 1, MaxHumanEscalationRate: 0,
		MaxArbitrationTriggerRate: .5, MaxCostPer1000AnswersMicros: 1000,
	}
	items := []PanelObservation{}
	for _, subject := range seniorPanelSubjects {
		items = append(items, readyPanelSlice(subject)...)
	}
	report, err := AssessSeniorNineSubjectShadowReadiness(items, policy)
	if err != nil || !report.Ready || len(report.MissingSubjects) != 0 || len(report.Slices) != len(seniorPanelSubjects) {
		t.Fatalf("valid nine-subject shadow evidence did not pass: %#v err=%v", report, err)
	}
	missingReport, err := AssessSeniorNineSubjectShadowReadiness(items[:len(items)-4], policy)
	if err != nil || missingReport.Ready || len(missingReport.MissingSubjects) != 1 {
		t.Fatalf("missing subject did not fail closed: %#v err=%v", missingReport, err)
	}
	tweak := append([]PanelObservation{}, items...)
	wrong := 0.0
	for index := range tweak {
		if tweak[index].Subject == seniorPanelSubjects[0] && tweak[index].ScoreC != nil {
			tweak[index].ScoreC, tweak[index].ResolvedScore = &wrong, &wrong
		}
	}
	badReport, err := AssessSeniorNineSubjectShadowReadiness(tweak, policy)
	if err != nil || badReport.Ready {
		t.Fatalf("weak arbiter evidence did not fail closed: %#v err=%v", badReport, err)
	}
}

func TestSeniorPanelReadinessRejectsUnversionedThresholds(t *testing.T) {
	if _, err := AssessSeniorNineSubjectShadowReadiness(nil, PanelReadinessPolicy{}); err != ErrInvalidInput {
		t.Fatalf("unversioned thresholds were accepted: %v", err)
	}
}

func readyPanelSlice(subject string) []PanelObservation {
	values := []PanelObservation{}
	for index, score := range []float64{0, 1} {
		resolved := score
		values = append(values, PanelObservation{ResponseKey: subject + "-consensus-" + string(rune('a'+index)), EducationStage: "senior", Subject: subject, Archetype: "short_answer",
			ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: score, MaxScore: 3, ScoreA: score, ScoreB: score,
			ResolvedScore: &resolved, ResolutionSource: "primary_consensus", ReferenceReviewers: 2, ReferenceAdjudicated: true})
	}
	for index, score := range []float64{2, 3} {
		arbiter, resolved := score, score
		values = append(values, PanelObservation{ResponseKey: subject + "-arbiter-" + string(rune('a'+index)), EducationStage: "senior", Subject: subject, Archetype: "short_answer",
			ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: score, MaxScore: 3, ScoreA: score - 2, ScoreB: score - 1,
			ScoreC: &arbiter, ResolvedScore: &resolved, ArbitrationTriggered: true, ResolutionSource: "arbiter",
			ReferenceReviewers: 2, ReferenceAdjudicated: true})
	}
	return values
}
