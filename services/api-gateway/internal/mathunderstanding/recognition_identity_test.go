package mathunderstanding

import (
	"encoding/json"
	"testing"
)

func TestRecognitionResultIdentitySurvivesStoreClone(t *testing.T) {
	input, err := prepareRuntimeArtifact(runtimeFormulaInput())
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	artifact, err := store.CreateArtifact(t.Context(), "tenant-a", input)
	if err != nil {
		t.Fatal(err)
	}
	if !sameRecognitionResult(artifact, input) {
		existing, _ := json.Marshal(artifact.CreateArtifactInput)
		incoming, _ := json.Marshal(input)
		t.Fatalf("clone changed recognition identity:\nstored=%s\nincoming=%s", existing, incoming)
	}
}
