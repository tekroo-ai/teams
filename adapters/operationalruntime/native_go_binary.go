package operationalruntime

import (
	"os/exec"
	"path/filepath"
)

func resolveNativeGoBinary(configured string) (string, error) {
	name := configured
	if name == "" {
		name = "go"
	} else if !filepath.IsAbs(name) {
		return "", invalidConfig("native.go_binary must be an absolute executable path")
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", invalidConfig("native Go test capability requires an installed Go executable; configure native.go_binary")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(path) {
		return "", invalidConfig("native Go executable cannot be resolved")
	}
	return path, nil
}
