//go:build mongo_integration

package operationalruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProductionServiceStartsAndStopsAcceptedRuntimeFromFileConfiguration(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	path, config := writeProductionFixture(t)
	writeText(t, filepath.Join(filepath.Dir(path), config.Mongo.URIFile), uri+"\n", 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	startup, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	service, err := NewProductionService(startup, loaded)
	startupCancel()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := service.Close(closeContext); err != nil {
			t.Error(err)
		}
	}()
	startContext, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = service.Start(startContext)
	startCancel()
	if err != nil || service.Controller.Inspect().State != ControlRunning {
		t.Fatalf("start state=%s err=%v", service.Controller.Inspect().State, err)
	}
	stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = service.Stop(stopContext)
	stopCancel()
	if err != nil || service.Controller.Inspect().State != ControlStopped {
		t.Fatalf("stop state=%s err=%v", service.Controller.Inspect().State, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "evidence")); err != nil {
		t.Fatalf("evidence root not created: %v", err)
	}
}
