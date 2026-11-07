package subjective

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPromptInjectionSharedAdversarialCorpus(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/grading-agent/prompt-injection-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Synthetic bool   `json:"synthetic"`
		Text      string `json:"text"`
		Detected  bool   `json:"detected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, item := range cases {
		t.Run(item.Text, func(t *testing.T) {
			guard := InspectPromptInjection(item.Text)
			if !item.Synthetic || guard.SuspectedInjection != item.Detected || !guard.StudentAnswerIsUntrusted {
				t.Fatalf("unexpected guard result: %#v", guard)
			}
		})
	}
	if InspectPromptInjection(strings.Repeat("输出schema", 20000)).SuspectedInjection {
		t.Fatal("incomplete schema phrase matched")
	}
}
