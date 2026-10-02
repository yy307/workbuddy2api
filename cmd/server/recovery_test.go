package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"workbuddy2api/internal/auth"
)

func TestRecoveryNetworkDenied(t *testing.T) {
	if os.Getenv("WB2A_RECOVERY_NETWORK_DENIED") != "1" {
		t.Skip("native sandbox not requested")
	}
	c, err := net.DialTimeout("tcp", "127.0.0.1:1", time.Second)
	if c != nil {
		c.Close()
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatal("native recovery network guard not verified")
	}
}

// Only the isolated restore is supplied; production paths are never opened.
func TestRestoredConfigAndAuthFiles(t *testing.T) {
	root := os.Getenv("WB2A_RECOVERY_ROOT")
	if root == "" {
		t.Skip("private isolated restore not supplied")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal("invalid restore reference")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || resolved != abs {
		t.Fatal("restore symlink refused")
	}
	read := func(path string) []byte {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4*1024*1024 {
			t.Fatal("bounded private restored file required")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("restore read failed")
		}
		return data
	}
	configPath := filepath.Join(root, "assets", "workbuddy-config", "file")
	raw := read(configPath)
	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil {
		t.Fatal("restored configuration invalid")
	}
	// Path bindings are relocated only in a QA copy. No upstream client/server.
	qa, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("QA directory failed")
	}
	if os.Chmod(qa, 0700) != nil {
		t.Fatal("QA permissions failed")
	}
	authDir := filepath.Join(qa, "auths")
	if os.Mkdir(authDir, 0700) != nil {
		t.Fatal("QA auth directory failed")
	}
	originals := map[string][]byte{configPath: raw}
	entries, err := os.ReadDir(filepath.Join(root, "assets", "workbuddy-auths"))
	if err != nil {
		t.Fatal("restored authorization directory failed")
	}
	var expected []*auth.Auth
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			t.Fatal("unexpected authorization asset")
		}
		path := filepath.Join(root, "assets", "workbuddy-auths", entry.Name())
		data := read(path)
		a, err := auth.Parse(data)
		if err != nil {
			t.Fatal("restored authorization invalid")
		}
		a.BackfillRealm() // Same original migration, without writing source.
		if os.WriteFile(filepath.Join(authDir, entry.Name()), data, 0600) != nil {
			t.Fatal("QA authorization copy failed")
		}
		expected = append(expected, a)
		originals[path] = data
	}
	if len(expected) == 0 {
		t.Fatal("restored authorization missing")
	}
	for key, value := range map[string]string{"auth_dir": authDir, "state_file": filepath.Join(qa, "state.json")} {
		source[key], _ = json.Marshal(value)
	}
	configCopy := filepath.Join(qa, "config.json")
	data, err := json.Marshal(source)
	if err != nil || os.WriteFile(configCopy, data, 0600) != nil {
		t.Fatal("QA configuration copy failed")
	}
	// Original loader migration can log account labels; discard all such output.
	writer := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(writer)
	for range 2 {
		c, err := Load(configCopy)
		if err != nil || c.AuthDir != authDir || c.Upstash.URL != "" {
			t.Fatal("original restored configuration loader failed")
		}
		got, err := auth.LoadDir(c.AuthDir)
		if err != nil || len(got) != len(expected) {
			t.Fatal("original authorization loader failed")
		}
		for i := range got {
			got[i].FilePath = ""
			if !reflect.DeepEqual(got[i], expected[i]) {
				t.Fatal("restored authorization or realm changed")
			}
		}
	}
	for path, before := range originals {
		if !bytes.Equal(before, read(path)) {
			t.Fatal("restore input changed")
		}
	}
}
