//go:build !darwin

package agenttools

func cloneCacheFile(source, destination string) (bool, error) { return false, nil }
