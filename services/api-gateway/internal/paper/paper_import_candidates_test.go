package paper

import (
	"context"
	"testing"
)

func candidateImport(t *testing.T) (*MemoryStore, PaperImportJob) {
	t.Helper()
	store := NewMemoryStore()
	job, err := store.CreatePaperImport(context.Background(), "tenant", "exam", "user", CreatePaperImportInput{Subject: "mathematics", Sources: []CreatePaperImportSourceInput{{FileAssetID: "asset-1", DocumentIndex: 0, RoleHint: "auto"}}})
	if err != nil {
		t.Fatal(err)
	}
	return store, job
}

func issueCodes(job PaperImportJob) map[string]bool {
	out := map[string]bool{}
	for _, issue := range job.StructuredIssues {
		out[issue.Code] = true
	}
	return out
}
