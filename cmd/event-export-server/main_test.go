package main

import "testing"

func TestLoadConfigurationRequiresSecretsAndLoopback(t *testing.T) {
	for _, name := range []string{"TEKROO_EVENT_EXPORT_ADDRESS", "TEKROO_EVENT_EXPORT_MONGO_URI", "TEKROO_EVENT_EXPORT_MONGO_DATABASE", "TEKROO_EVENT_EXPORT_SOURCE_ID", "TEKROO_EVENT_EXPORT_PRINCIPAL_ID", "TEKROO_EVENT_EXPORT_BEARER_TOKEN", "TEKROO_EVENT_EXPORT_CURSOR_KEY_HEX"} {
		t.Setenv(name, "")
	}
	if _, err := loadConfiguration(); err == nil {
		t.Fatal("configuration without secrets passed")
	}
	t.Setenv("TEKROO_EVENT_EXPORT_MONGO_URI", "mongodb://127.0.0.1:27017")
	t.Setenv("TEKROO_EVENT_EXPORT_MONGO_DATABASE", "teams")
	t.Setenv("TEKROO_EVENT_EXPORT_BEARER_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("TEKROO_EVENT_EXPORT_CURSOR_KEY_HEX", "3031323334353637383961626364656630313233343536373839616263646566")
	configuration, err := loadConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.address != "127.0.0.1:8087" || configuration.sourceID != "teams-main" || len(configuration.cursorKey) != 32 {
		t.Fatalf("configuration = %#v", configuration)
	}
	t.Setenv("TEKROO_EVENT_EXPORT_ADDRESS", "0.0.0.0:8087")
	if _, err := loadConfiguration(); err == nil {
		t.Fatal("non-loopback listener passed")
	}
}
