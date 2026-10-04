//go:build mongo_integration

package operationalruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileBackedNativeServiceRestartsWithoutOpenHands(t *testing.T) {
	process, uri := startRuntimeMongod(t)
	defer stopRuntimeMongod(process)
	path, config := writeNativeProductionFixture(t)
	writeText(t, filepath.Join(filepath.Dir(path), config.Mongo.URIFile), uri+"\n", 0o600)
	loaded, err := LoadProductionConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		startup, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
		service, startErr := NewProductionService(startup, loaded)
		cancelStartup()
		if startErr != nil {
			t.Fatalf("native service construction %d: %v", attempt, startErr)
		}
		if !service.qualificationNative {
			t.Fatal("file-backed native service selected the OpenHands execution boundary")
		}
		runCtx, cancelRun := context.WithTimeout(context.Background(), 10*time.Second)
		startErr = service.Start(runCtx)
		cancelRun()
		if startErr != nil {
			t.Fatalf("native service start %d: %v", attempt, startErr)
		}
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
		stopErr := service.Stop(stopCtx)
		cancelStop()
		if stopErr != nil {
			t.Fatalf("native service stop %d: %v", attempt, stopErr)
		}
		closeCtx, cancelClose := context.WithTimeout(context.Background(), 10*time.Second)
		closeErr := service.Close(closeCtx)
		cancelClose()
		if closeErr != nil {
			t.Fatalf("native service close %d: %v", attempt, closeErr)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "evidence")); err != nil {
		t.Fatalf("native evidence root missing after restart: %v", err)
	}
}
