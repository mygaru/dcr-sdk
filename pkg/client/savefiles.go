package client

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// SaveToFiles is an MTLSConfig.OnRenewed for the common case of a certificate
// kept in two PEM files: it saves a renewed certificate and key over certPath
// and keyPath, the files the application loads them from, so a restart picks
// the renewed one up.
//
// Each file is replaced atomically - written beside it and renamed over it -
// so a crash leaves either the old file or the new one, never a torn one. The
// key goes first: should the process die between the two, a restart loads a
// mismatched pair and fails loudly rather than running on the old certificate
// until it expires. The key is written 0600, the certificate 0644.
func SaveToFiles(certPath, keyPath string) func(certPEM, keyPEM []byte) error {
	return func(certPEM, keyPEM []byte) error {
		if err := saveFileAtomic(keyPath, keyPEM, 0o600); err != nil {
			return fmt.Errorf("save key %s: %w", keyPath, err)
		}
		if err := saveFileAtomic(certPath, certPEM, 0o644); err != nil {
			return fmt.Errorf("save certificate %s: %w", certPath, err)
		}
		return nil
	}
}

// saveFileAtomic replaces path with data: written to a temporary file in the
// same directory, synced and renamed over it.
func saveFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}

	// the read-back is what a restart would load
	saved, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(saved, data) {
		return fmt.Errorf("%s does not hold what was written", path)
	}
	return nil
}
