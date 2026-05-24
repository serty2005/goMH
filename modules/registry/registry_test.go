package registry

import "testing"

func TestNewDefaultDoesNotExposeAutostartAsTopLevelModule(t *testing.T) {
	registry := NewDefault()

	if _, ok := registry.Get("autostart"); ok {
		t.Fatal("autostart should be available through ServiceUtils, not as a top-level module")
	}
}
