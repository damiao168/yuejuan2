package db

import "testing"

func TestGatewaySchemaVersionRequiresExactRelease(t *testing.T) {
	for _, actual := range []string{"", "000001", "999999"} {
		if err := validateSchemaVersion(actual); err == nil {
			t.Fatalf("accepted incompatible database version %q", actual)
		}
	}
	if err := validateSchemaVersion(RequiredSchemaVersion); err != nil {
		t.Fatal(err)
	}
}
