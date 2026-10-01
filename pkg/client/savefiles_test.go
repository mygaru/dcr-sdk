package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveToFilesReplacesBothFiles(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "client.pem"), filepath.Join(dir, "client-key.pem")
	for _, path := range []string{certPath, keyPath} {
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := SaveToFiles(certPath, keyPath)([]byte("new cert"), []byte("new key")); err != nil {
		t.Fatalf("SaveToFiles: %v", err)
	}

	for path, want := range map[string]string{certPath: "new cert", keyPath: "new key"} {
		if got, _ := os.ReadFile(path); string(got) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(path), got, want)
		}
	}
	if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	// no temporary file is left behind
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("directory holds %d files, want the two", len(entries))
	}
}

func TestSaveToFilesReportsAFileItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-dir", "client.pem")

	if err := SaveToFiles(missing, filepath.Join(dir, "client-key.pem"))([]byte("c"), []byte("k")); err == nil {
		t.Fatal("SaveToFiles into a missing directory reported no error")
	}
}
