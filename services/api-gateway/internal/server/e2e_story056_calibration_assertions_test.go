package server

import (
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"encoding/json"
	"net/http"
	"testing"
)

func e2eStory056CalibrationExpectedOptions(t *testing.T, db *sql.DB, tenantID, caseID string) []string {
	t.Helper()
	var decision string
	var observedRaw, measurementsRaw []byte
	if err := db.QueryRow(`
SELECT observed_decision,observed_options,measurements
FROM omr_calibration_case
WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, caseID).Scan(&decision, &observedRaw, &measurementsRaw); err != nil {
		t.Fatalf("load blind calibration ground truth fixture: %v", err)
	}
	var options []string
	if err := json.Unmarshal(observedRaw, &options); err != nil {
		t.Fatalf("decode calibration observed options: %v", err)
	}
	if len(options) > 0 {
		return options
	}
	if decision == "blank" {
		return []string{}
	}
	var measurements []map[string]any
	if err := json.Unmarshal(measurementsRaw, &measurements); err != nil {
		t.Fatalf("decode calibration measurements: %v", err)
	}
	if len(measurements) == 0 {
		t.Fatalf("ambiguous calibration case has no fixture ground truth: %s", caseID)
	}
	option, ok := measurements[0]["option"].(string)
	if !ok || option == "" {
		t.Fatalf("ambiguous calibration case has invalid fixture ground truth: %#v", measurements[0])
	}
	return []string{option}
}

func e2eStory056CalibrationCaseForQuestionAndStratum(t *testing.T, cases []any, questionID, stratum string) map[string]any {
	t.Helper()
	for _, raw := range cases {
		item, ok := raw.(map[string]any)
		if ok && e2eString(t, item, "question_id") == questionID && e2eString(t, item, "sample_stratum") == stratum {
			return item
		}
	}
	t.Fatalf("missing calibration case for question %s in stratum %s", questionID, stratum)
	return nil
}

func e2eStory056CalibrationReprocessAndClaim(t *testing.T, router http.Handler, adminToken, segmentID, suffix string) (string, map[string]any, story056WorkerLease) {
	t.Helper()
	run := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/reprocess-score", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": "story056-calibration-" + suffix,
	}), http.StatusCreated)["scoring_run"].(map[string]any)
	runID := e2eString(t, run, "id")
	item, lease := e2eStory056ClaimSingleOMR(t, router, adminToken, runID, "story056-calibration-worker")
	return runID, item, lease
}

// 校准结果仍分别携带算法版本、配置版本、配置哈希和参考图哈希，不能用一个版本值替代其余证据。
type story056CalibrationProfile struct {
	Version, ProfileVersion, ProfileHash, ReferenceSHA256 string
}

func e2eStory056CalibrationResultPayload(t *testing.T, lease story056WorkerLease, item map[string]any, overlay files.FileAsset, profile story056CalibrationProfile) map[string]any {
	t.Helper()
	version, profileVersion, profileHash, referenceSHA256 := profile.Version, profile.ProfileVersion, profile.ProfileHash, profile.ReferenceSHA256
	payload := e2eStory056OMRResultPayload(t, lease, item, overlay, version, []map[string]any{{"option": "A", "foreground_delta": 0.9}}, false)
	payload["profile_version"] = profileVersion
	payload["profile_hash"] = profileHash
	payload["reference_sha256"] = referenceSHA256
	payload["thresholds"] = map[string]any{
		"marked_threshold": 0.12, "ambiguous_threshold": 0.04, "minimum_margin": 0.04,
		"border_fraction": 0.12, "reference_mask_dilation_pixels": 1,
	}
	return payload
}
