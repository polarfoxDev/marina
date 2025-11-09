package helpers

import "testing"

func TestValidateCron(t *testing.T) {
	if err := ValidateCron("0 3 * * *"); err != nil {
		t.Fatalf("expected valid cron, got error: %v", err)
	}
	if err := ValidateCron("0 3 * *"); err == nil {
		t.Fatalf("expected error for short cron, got nil")
	}
}
