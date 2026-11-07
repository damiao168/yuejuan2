package appeal

import (
	"encoding/json"
	"testing"
)

func TestPublishedAppealOmittedOptionalRegionIsAJSONObject(t *testing.T) {
	_, _, item := publishedQuestionWorkflowFixture(t)
	raw, err := json.Marshal(item.SelectedRegion)
	if err != nil || string(raw) != "{}" {
		t.Fatalf("omitted optional region must remain a database-compatible object: %s %v", raw, err)
	}
}
