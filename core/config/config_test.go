package config

import (
	"testing"
)

func TestValidateForServingRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	c := Load()
	if err := c.ValidateForServing(); err == nil {
		t.Fatal("expected error for empty DATABASE_URL")
	}
}

func TestPortParsing(t *testing.T) {
	t.Setenv("A1_SHIFTHUB_PORT", "9090")
	if got := Port("SHIFTHUB", 8081); got != 9090 {
		t.Fatalf("Port = %d, want 9090", got)
	}
	t.Setenv("A1_SHIFTHUB_PORT", "not-a-port")
	if got := Port("SHIFTHUB", 8081); got != 8081 {
		t.Fatalf("Port with invalid value = %d, want default 8081", got)
	}
}
