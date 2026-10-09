package operationalruntime

import "testing"

func TestLocalTestExecutionRequiresExplicitFileBackedOptIn(t *testing.T) {
	path, config := writeNativeProductionFixture(t)
	loaded, err := LoadProductionConfig(path)
	if err != nil || loaded.Native.LocalTestExecution {
		t.Fatalf("ordinary configuration unexpectedly enabled local tests: %+v %v", loaded.Native, err)
	}
	config.Native.LocalTestExecution = true
	writeJSON(t, path, config, 0600)
	loaded, err = LoadProductionConfig(path)
	if err != nil || !loaded.Native.LocalTestExecution {
		t.Fatalf("explicit local execution setting lost: %+v %v", loaded.Native, err)
	}
}
