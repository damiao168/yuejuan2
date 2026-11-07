package appeal

import (
	"time"
)

func cloneAppeal(in Appeal) Appeal {
	in.Attachment = cloneMap(in.Attachment)
	in.ReviewedAt = cloneTime(in.ReviewedAt)
	in.RecommendationAt = cloneTime(in.RecommendationAt)
	in.RecommendedScore = cloneFloat(in.RecommendedScore)
	in.ClosedAt = cloneTime(in.ClosedAt)
	in.Adjustments = append([]ScoreAdjustment(nil), in.Adjustments...)
	if in.Evidence != nil {
		ev := *in.Evidence
		ev.AIGrades = cloneMaps(ev.AIGrades)
		ev.HumanGrades = cloneMaps(ev.HumanGrades)
		ev.Rubric = cloneMap(ev.Rubric)
		ev.FinalGrade = cloneMap(ev.FinalGrade)
		in.Evidence = &ev
	}
	return in
}

func cloneFloat(in *float64) *float64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
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

func cloneMaps(in []map[string]any) []map[string]any {
	out := make([]map[string]any, len(in))
	for i := range in {
		out[i] = cloneMap(in[i])
	}
	return out
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
