package agenttools

import "testing"

func TestNativeToolSurfaceDigestBindsPermissions(t *testing.T) {
	readOnly, err := NativeToolSurfaceDigest([]string{"repository.read"})
	if err != nil || !readOnly.Valid() {
		t.Fatalf("read-only digest: %s %v", readOnly, err)
	}
	editable, err := NativeToolSurfaceDigest([]string{"repository.read", "repository.edit"})
	if err != nil || !editable.Valid() || editable == readOnly {
		t.Fatalf("editable digest: %s %v", editable, err)
	}
	reordered, err := NativeToolSurfaceDigest([]string{"repository.edit", "repository.read"})
	if err != nil || reordered != editable {
		t.Fatalf("permission order changed tool surface: %s %s %v", editable, reordered, err)
	}
}
