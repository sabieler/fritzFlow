package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveRejectsArbitraryCommands(t *testing.T) {
	for _, b := range []string{"rm -rf /", "sh", "calc.exe", "../evil", "evil\nrm", "cmd /c del *"} {
		if _, err := Resolve(b); err == nil {
			t.Errorf("%q must be rejected", b)
		}
	}
	if p, err := Resolve(""); err != nil || p != "" {
		t.Errorf("empty = system default, got %q %v", p, err)
	}
	if _, err := Resolve(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("missing file must be rejected")
	}
}

func TestResolveAcceptsAbsoluteExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path characters differ")
	}
	f := filepath.Join(t.TempDir(), "mybrowser")
	if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if p, err := Resolve(f); err != nil || p != f {
		t.Fatalf("got %q %v", p, err)
	}
}
