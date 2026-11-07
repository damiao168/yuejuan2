package regrade

func cloneItems(values []Item) []Item {
	out := append([]Item(nil), values...)
	for i := range out {
		out[i] = cloneItem(out[i])
	}
	return out
}

func cloneItem(value Item) Item {
	value.CandidateRubricSelections = append([]RubricSelection(nil), value.CandidateRubricSelections...)
	if value.CandidateScore != nil {
		v := *value.CandidateScore
		value.CandidateScore = &v
	}
	if value.ReviewedScore != nil {
		v := *value.ReviewedScore
		value.ReviewedScore = &v
	}
	if value.Delta != nil {
		v := *value.Delta
		value.Delta = &v
	}
	return value
}

func cloneJob(value Job) Job {
	value.Selector = cloneSelector(value.Selector)
	if value.ApprovedAt != nil {
		t := *value.ApprovedAt
		value.ApprovedAt = &t
	}
	if value.FinalizedAt != nil {
		t := *value.FinalizedAt
		value.FinalizedAt = &t
	}
	return value
}

func cloneSelector(value Selector) Selector {
	value.SubmissionIDs = append([]string(nil), value.SubmissionIDs...)
	if value.ScoreBand != nil {
		b := *value.ScoreBand
		if b.Min != nil {
			v := *b.Min
			b.Min = &v
		}
		if b.Max != nil {
			v := *b.Max
			b.Max = &v
		}
		value.ScoreBand = &b
	}
	return value
}

func cloneEvents(values []Event) []Event {
	out := append([]Event(nil), values...)
	for i := range out {
		out[i].Payload = clonePayload(out[i].Payload)
	}
	return out
}

func clonePayload(value map[string]any) map[string]any {
	out := map[string]any{}
	for key, item := range value {
		out[key] = item
	}
	return out
}
