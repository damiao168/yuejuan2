package paper

import (
	"errors"
	"testing"
)

func TestNormalizePaperImportSubject(t *testing.T) {
	tests := []struct {
		name          string
		authoritative string
		requested     string
		want          string
		wantErr       bool
	}{
		{name: "canonical", authoritative: "mathematics", requested: "mathematics", want: "mathematics"},
		{name: "alias", authoritative: "数学", requested: "math", want: "mathematics"},
		{name: "mismatch", authoritative: "mathematics", requested: "english", wantErr: true},
		{name: "invalid authoritative", authoritative: "", requested: "math", wantErr: true},
		{name: "invalid requested", authoritative: "math", requested: "unknown-subject", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizePaperImportSubject(test.authoritative, test.requested)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("expected ErrInvalidInput, got %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("normalize subject = (%q, %v), want (%q, nil)", got, err, test.want)
			}
		})
	}
}

func TestValidateCreatePaperImportSources(t *testing.T) {
	valid := []CreatePaperImportSourceInput{
		{FileAssetID: "file-a", DocumentIndex: 0, RoleHint: "question"},
		{FileAssetID: "file-b", DocumentIndex: 1, RoleHint: "answer"},
	}
	tests := []struct {
		name    string
		sources []CreatePaperImportSourceInput
		wantErr bool
	}{
		{name: "valid", sources: valid},
		{name: "empty", sources: nil, wantErr: true},
		{name: "missing file", sources: []CreatePaperImportSourceInput{{DocumentIndex: 0, RoleHint: "question"}}, wantErr: true},
		{name: "negative index", sources: []CreatePaperImportSourceInput{{FileAssetID: "file-a", DocumentIndex: -1, RoleHint: "question"}}, wantErr: true},
		{name: "duplicate file", sources: append(valid, CreatePaperImportSourceInput{FileAssetID: "file-a", DocumentIndex: 2, RoleHint: "answer"}), wantErr: true},
		{name: "duplicate index", sources: append(valid, CreatePaperImportSourceInput{FileAssetID: "file-c", DocumentIndex: 1, RoleHint: "answer"}), wantErr: true},
		{name: "invalid role", sources: []CreatePaperImportSourceInput{{FileAssetID: "file-a", DocumentIndex: 0, RoleHint: "teacher_copy"}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCreatePaperImportSources(test.sources)
			if test.wantErr != errors.Is(err, ErrInvalidInput) {
				t.Fatalf("validation error = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestNormalizePaperImportSourcesAndCommandHashAreDeterministic(t *testing.T) {
	input := CreatePaperImportInput{
		PaperFileAssetID:  "paper-file",
		AnswerFileAssetID: "answer-file",
		Subject:           "math",
	}
	sources := normalizePaperImportSourceInputs(input)
	if len(sources) != 2 || sources[0].RoleHint != "question" || sources[1].RoleHint != "answer" || sources[0].DocumentIndex != 0 || sources[1].DocumentIndex != 1 {
		t.Fatalf("unexpected normalized sources: %#v", sources)
	}
	first := paperImportCommandHash(input)
	second := paperImportCommandHash(input)
	if first == "" || first != second {
		t.Fatalf("command hash is not deterministic: %q != %q", first, second)
	}
	input.Subject = "english"
	if changed := paperImportCommandHash(input); changed == first {
		t.Fatalf("command hash did not change with request content")
	}
}
