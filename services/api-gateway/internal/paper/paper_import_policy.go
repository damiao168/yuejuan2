package paper

import (
	"encoding/json"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func normalizePaperImportSourceInputs(input CreatePaperImportInput) []CreatePaperImportSourceInput {
	out := append([]CreatePaperImportSourceInput{}, input.Sources...)
	if len(out) == 0 {
		if input.PaperFileAssetID != "" {
			out = append(out, CreatePaperImportSourceInput{FileAssetID: input.PaperFileAssetID, DocumentIndex: 0, RoleHint: "question"})
		}
		if input.AnswerFileAssetID != "" && input.AnswerFileAssetID != input.PaperFileAssetID {
			out = append(out, CreatePaperImportSourceInput{FileAssetID: input.AnswerFileAssetID, DocumentIndex: len(out), RoleHint: "answer"})
		}
	}
	for i := range out {
		if out[i].DocumentIndex < 0 {
			out[i].DocumentIndex = i
		}
		out[i].RoleHint = defaultRoleHint(out[i].RoleHint)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DocumentIndex < out[j].DocumentIndex })
	return out
}

func normalizePaperImportSubject(authoritative, requested string) (string, error) {
	authoritativeCode, authoritativeOK := assessment.NormalizeSubjectCode(authoritative)
	requestedCode, requestedOK := assessment.NormalizeSubjectCode(requested)
	if !authoritativeOK || !requestedOK || authoritativeCode != requestedCode {
		return "", ErrInvalidInput
	}
	return string(authoritativeCode), nil
}

func validateCreatePaperImportSources(sources []CreatePaperImportSourceInput) error {
	if len(sources) == 0 {
		return ErrInvalidInput
	}
	seenFiles := map[string]bool{}
	seenIndexes := map[int]bool{}
	for _, source := range sources {
		if strings.TrimSpace(source.FileAssetID) == "" || source.DocumentIndex < 0 || seenFiles[source.FileAssetID] || seenIndexes[source.DocumentIndex] || !validPaperImportRole(source.RoleHint, true) {
			return ErrInvalidInput
		}
		seenFiles[source.FileAssetID] = true
		seenIndexes[source.DocumentIndex] = true
	}
	return nil
}

func validateReplacePaperImportSources(sources []ReplacePaperImportSourceInput, active map[string]bool) error {
	seenIDs := map[string]bool{}
	seenIndexes := map[int]bool{}
	for _, source := range sources {
		if !active[source.ID] || source.DocumentIndex < 0 || seenIDs[source.ID] || seenIndexes[source.DocumentIndex] || !validPaperImportRole(source.RoleHint, true) {
			return ErrInvalidInput
		}
		seenIDs[source.ID] = true
		seenIndexes[source.DocumentIndex] = true
	}
	return nil
}

func defaultRoleHint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "auto"
	}
	return value
}

func validPaperImportRole(value string, allowAuto bool) bool {
	switch defaultRoleHint(value) {
	case "question", "answer", "solution", "rubric", "mixed", "unknown":
		return true
	case "auto":
		return allowAuto
	}
	return false
}

func paperImportCommandHash(input any) string {
	raw, _ := json.Marshal(input)
	return contentHash(raw)
}
