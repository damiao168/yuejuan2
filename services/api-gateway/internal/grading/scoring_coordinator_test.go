package grading

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestScoringObservationKeepsUnresolvedSharedMemberIndependent(t *testing.T) {
	input := ScoringObservationInput{InputVersionID: "input", Members: []ObservationMember{{SegmentID: "s12", QuestionID: "q12"}, {SegmentID: "s13", QuestionID: "q13"}}}
	raw := json.RawMessage(`{"input_version_id":"input","observations":[{"id":"o12","question_id":"q12"}],"members":[{"answer_segment_id":"s12","question_id":"q12","presence":"present","answer":{"type":"latex","value":"-1/2"},"association":"matched","confidence":0.98,"evidence_ids":["o12"]}]}`)
	members, err := validateScoringObservation(raw, input)
	if err != nil {
		t.Fatal(err)
	}
	if members["s12"].Answer.Value != "-1/2" || members["s13"].Presence != "uncertain" || members["s13"].Answer != nil {
		t.Fatalf("shared members leaked: %#v", members)
	}
}

func TestScoringObservationRejectsDifferentInputAndFabricatedBlank(t *testing.T) {
	input := ScoringObservationInput{InputVersionID: "input", Members: []ObservationMember{{SegmentID: "s", QuestionID: "q"}}}
	cases := []struct {
		raw  string
		want error
	}{
		{`{"input_version_id":"old","members":[]}`, ErrRevisionConflict},
		{`{"input_version_id":"input","members":[{"answer_segment_id":"s","question_id":"other","presence":"uncertain","association":"unresolved"}]}`, ErrInvalidInput},
		{`{"input_version_id":"input","members":[{"answer_segment_id":"s","question_id":"q","presence":"blank","association":"matched","confidence":0.98,"evidence_ids":[]}]}`, ErrInvalidInput},
		{`{"input_version_id":"input","members":[{"answer_segment_id":"s","question_id":"q","presence":"blank","answer":{"type":"text","value":"answer key"},"association":"matched","confidence":0.98,"evidence_ids":["o"]}]}`, ErrInvalidInput},
	}
	for _, item := range cases {
		if _, err := validateScoringObservation(json.RawMessage(item.raw), input); !errors.Is(err, item.want) {
			t.Fatalf("raw=%s got=%v want=%v", item.raw, err, item.want)
		}
	}
}

func TestScoringObservationKeepsUnansweredSubpartWithoutRejectingWholeQuestion(t *testing.T) {
	input := ScoringObservationInput{InputVersionID: "input", Members: []ObservationMember{{SegmentID: "s", QuestionID: "q"}}}
	for _, test := range []struct {
		parts string
		valid bool
	}{{`["first step","","third step"]`, true}, {`[""," ",""]`, false}} {
		raw := json.RawMessage(`{"input_version_id":"input","observations":[{"id":"o","question_id":"q"}],"members":[{"answer_segment_id":"s","question_id":"q","presence":"present","answer":{"type":"parts","value":"","parts":` + test.parts + `},"association":"matched","confidence":0.98,"evidence_ids":["o"]}]}`)
		members, err := validateScoringObservation(raw, input)
		if !test.valid {
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("all empty subparts accepted: %v", err)
			}
			continue
		}
		if err != nil || len(members["s"].Answer.Parts) != 3 || members["s"].Answer.Parts[1] != "" {
			t.Fatalf("unanswered subpart lost: %#v err=%v", members, err)
		}
	}
}
