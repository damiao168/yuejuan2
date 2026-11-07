package review

import (
	"time"
)

func floatPtr(in float64) *float64 {
	out := in
	return &out
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneSelections(in []RubricSelection) []RubricSelection {
	out := make([]RubricSelection, len(in))
	copy(out, in)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTask(in ReviewTask) ReviewTask {
	in.DueAt = cloneTime(in.DueAt)
	return in
}

func cloneGrade(in HumanGrade) HumanGrade {
	in.RubricSelections = cloneSelections(in.RubricSelections)
	return in
}

func clonePolicy(in DoubleMarkPolicy) DoubleMarkPolicy {
	return in
}

func cloneSession(in DoubleMarkSession) DoubleMarkSession {
	if in.ScoreDifference != nil {
		in.ScoreDifference = floatPtr(*in.ScoreDifference)
	}
	return in
}

func cloneSessionPtr(in *DoubleMarkSession) *DoubleMarkSession {
	if in == nil {
		return nil
	}
	out := cloneSession(*in)
	return &out
}

func cloneArbitration(in ArbitrationTask) ArbitrationTask {
	if in.FinalScore != nil {
		in.FinalScore = floatPtr(*in.FinalScore)
	}
	in.Context = cloneReviewContext(in.Context)
	return in
}

func cloneArbitrationPtr(in *ArbitrationTask) *ArbitrationTask {
	if in == nil {
		return nil
	}
	out := cloneArbitration(*in)
	return &out
}

func cloneFinalGrade(in FinalGrade) FinalGrade {
	return in
}

func cloneFinalGradePtr(in *FinalGrade) *FinalGrade {
	if in == nil {
		return nil
	}
	out := cloneFinalGrade(*in)
	return &out
}
