package assessment

import (
	"errors"
	"testing"
)

func TestBatch49AllowedEvidenceKeepsProfileShapesAndIntersection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowed   any
		selected  []EvidenceType
		archetype []EvidenceType
		required  bool
		want      error
	}{
		{"json slice", []any{string(EvidenceTextSpan), 42, true}, []EvidenceType{EvidenceTextSpan}, []EvidenceType{EvidenceTextSpan}, true, nil},
		{"string slice", []string{string(EvidenceConcept)}, []EvidenceType{EvidenceConcept}, []EvidenceType{EvidenceConcept}, true, nil},
		{"evidence slice", []EvidenceType{EvidenceMathStep}, []EvidenceType{EvidenceMathStep}, []EvidenceType{EvidenceMathStep}, true, nil},
		{"profile alone cannot allow evidence", []string{string(EvidenceConcept)}, []EvidenceType{EvidenceConcept}, []EvidenceType{EvidenceTextSpan}, false, ErrInvalidInput},
		{"archetype alone cannot allow evidence", []string{string(EvidenceTextSpan)}, []EvidenceType{EvidenceConcept}, []EvidenceType{EvidenceConcept}, false, ErrInvalidInput},
		{"json non-string ignored", []any{EvidenceTextSpan}, []EvidenceType{EvidenceTextSpan}, []EvidenceType{EvidenceTextSpan}, false, ErrInvalidInput},
		{"unknown shape rejected", string(EvidenceTextSpan), []EvidenceType{EvidenceTextSpan}, []EvidenceType{EvidenceTextSpan}, false, ErrInvalidInput},
		{"required empty rejected", nil, nil, nil, true, ErrInvalidInput},
		{"optional empty accepted", nil, nil, nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAllowedEvidence(tc.selected, SubjectProfile{EvidencePolicy: map[string]any{"allowed_types": tc.allowed}}, QuestionArchetype{EvidenceTypes: tc.archetype}, tc.required)
			if !errors.Is(err, tc.want) {
				t.Fatalf("validation error=%v, want %v", err, tc.want)
			}
		})
	}
}
