package config

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// 配置测试显式注入合成密钥；产品默认配置仍必须拒绝缺失密钥。
	_ = os.Setenv("EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY", "synthetic-test-only-model-key-32-characters")
	_ = os.Setenv("EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY", "synthetic-test-only-review-key-32-characters")
	os.Exit(m.Run())
}

func TestLocalDeploymentRejectsPublicOrMissingModelKey(t *testing.T) {
	for _, value := range []string{"", localModelCredentialMasterKey, "replace_with_at_least_32_random_characters"} {
		t.Setenv("EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY", value)
		if _, err := Load(""); err == nil {
			t.Fatal("local deployment accepted missing/public model key")
		}
	}
}

func TestLocalDeploymentRequiresIndependentReviewEvidenceKey(t *testing.T) {
	for _, value := range []string{"", os.Getenv("EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY"), "replace_with_a_distinct_32_character_secret"} {
		t.Setenv("EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY", value)
		if _, err := Load(""); err == nil {
			t.Fatal("local deployment accepted missing, shared or example evidence key")
		}
	}
}
