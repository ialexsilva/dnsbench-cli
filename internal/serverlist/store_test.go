package serverlist

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dnsbench/internal/model"
)

func TestSaveAndLoadUser(t *testing.T) {
	base := t.TempDir()
	servers := []model.Server{
		{
			ID: "home-router", Name: "Home Router", Protocol: model.ProtoUDP,
			Address: "192.168.1.1", Source: model.SourceUser, Enabled: true,
		},
		{
			ID: "my-doh", Name: "My DoH", Protocol: model.ProtoDoH,
			DoHURL: "https://dns.example.com/dns-query", Source: model.SourceUser, Enabled: false,
		},
	}
	if err := SaveUser(base, servers); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadUser(base)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(servers, loaded) {
		t.Errorf("store roundtrip mismatch:\nin:  %+v\nout: %+v", servers, loaded)
	}
}

func TestLoadUserMissingFile(t *testing.T) {
	servers, err := LoadUser(t.TempDir())
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if servers == nil {
		t.Fatal("missing file should return an empty list, got nil")
	}
	if len(servers) != 0 {
		t.Errorf("got %d servers, want 0", len(servers))
	}
}

func TestSaveUserCreatesDirectory(t *testing.T) {
	base := filepath.Join(t.TempDir(), "nested", "config")
	if err := SaveUser(base, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "servers.json")); err != nil {
		t.Fatalf("expected servers.json under %s: %v", base, err)
	}
	loaded, err := LoadUser(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Errorf("got %d servers, want 0", len(loaded))
	}
}

func TestLoadUserInvalidJSON(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "servers.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUser(base); err == nil {
		t.Error("expected error for corrupt servers.json")
	}
}

func TestUserDir(t *testing.T) {
	explicit, err := UserDir("/some/base")
	if err != nil {
		t.Fatal(err)
	}
	if explicit != "/some/base" {
		t.Errorf("UserDir with explicit base returned %q", explicit)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("os.UserConfigDir unavailable: %v", err)
	}
	def, err := UserDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configDir, "dnsbench")
	if def != want {
		t.Errorf("UserDir(\"\") = %q, want %q", def, want)
	}
}
