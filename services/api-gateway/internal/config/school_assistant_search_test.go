package config

import "testing"

func TestSchoolAssistantSearchURLConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"", true},
		{"http://searxng:8080/search", true},
		{"https://search.example.org/prefix/search", true},
		{"searxng:8080/search", false},
		{"file:///etc/passwd", false},
		{"https://user:secret@search.example.org/search", false},
		{"https://search.example.org/search?q=private", false},
		{"https://search.example.org/search?", false},
		{"https://search.example.org/search#fragment", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("EDUGRADE_SCHOOL_ASSISTANT_SEARCH_URL", tc.value)
			cfg, err := Load("")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err == nil && cfg.SchoolAssistantSearchURL != tc.value {
				t.Fatal("search URL was not loaded")
			}
		})
	}
}
