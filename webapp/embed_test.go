package webapp

import (
	"bytes"
	"io/fs"
	"os"
	"testing"
)

// TestFSContainsMiniAppFiles verifica che i file incorporati siano identici
// a quelli su disco e che non ci siano file estranei.
func TestFSContainsMiniAppFiles(t *testing.T) {
	want := []string{"css/style.css", "index.html", "js/app.js"}
	var got []string
	err := fs.WalkDir(FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("file incorporati = %v, attesi %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("file incorporati = %v, attesi %v", got, want)
		}
		embedded, err := fs.ReadFile(FS(), name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		onDisk, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("os.ReadFile(%s): %v", name, err)
		}
		if !bytes.Equal(embedded, onDisk) {
			t.Errorf("%s incorporato diverso dal file su disco", name)
		}
	}
}
