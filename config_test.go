package main

import (
	"os"
	"testing"
)

func TestLoadConfigPrimaryDomain(t *testing.T) {
	os.Unsetenv("PRIMARY_DOMAIN")
	cfg := loadConfig()
	if cfg.primaryDomain != "cloudlift.run" {
		t.Fatalf("expected default primaryDomain to be 'cloudlift.run', got %q", cfg.primaryDomain)
	}

	os.Setenv("PRIMARY_DOMAIN", "example.com")
	defer os.Unsetenv("PRIMARY_DOMAIN")
	cfg = loadConfig()
	if cfg.primaryDomain != "example.com" {
		t.Fatalf("expected primaryDomain to be 'example.com', got %q", cfg.primaryDomain)
	}
}
