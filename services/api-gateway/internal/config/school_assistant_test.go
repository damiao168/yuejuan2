package config

import "testing"

func TestSchoolAssistantOptimizationSwitchesHaveConservativeDefaultsAndIndependentRollback(t *testing.T) {
	for _, key := range []string{"EDUGRADE_ASSISTANT_DIRECT_ANSWER_ENABLED", "EDUGRADE_ASSISTANT_MEMORY_DISABLED", "EDUGRADE_ASSISTANT_CACHE_COALESCING_DISABLED"} {
		t.Setenv(key, "")
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchoolAssistant != (SchoolAssistantConfig{}) {
		t.Fatal("experimental direct answer should be opt-in")
	}
	t.Setenv("EDUGRADE_ASSISTANT_DIRECT_ANSWER_ENABLED", "true")
	t.Setenv("EDUGRADE_ASSISTANT_MEMORY_DISABLED", "true")
	t.Setenv("EDUGRADE_ASSISTANT_CACHE_COALESCING_DISABLED", "true")
	cfg, err = Load("")
	if err != nil || !cfg.SchoolAssistant.DirectAnswerEnabled || !cfg.SchoolAssistant.DisableMemory || !cfg.SchoolAssistant.DisableCacheCoalescing {
		t.Fatalf("optimization rollback switches were ignored: %+v %v", cfg.SchoolAssistant, err)
	}
}
