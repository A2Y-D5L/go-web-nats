//nolint:testpackage // Runtime config tests validate unexported resolution helpers directly.
package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveNATSStoreDirRawDefaultsToPersistentDataDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		raw    string
		exists bool
	}{
		{name: "env missing", raw: "", exists: false},
		{name: "env empty", raw: "", exists: true},
		{name: "env whitespace", raw: "   ", exists: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveNATSStoreDirRaw(tc.raw, tc.exists)
			if got.isEphemeral {
				t.Fatalf("expected persistent mode, got ephemeral")
			}
			if got.storeDir != defaultNATSStoreDir {
				t.Fatalf("expected default store dir %q, got %q", defaultNATSStoreDir, got.storeDir)
			}
		})
	}
}

func TestResolveNATSStoreDirRawAcceptsExplicitEphemeralModes(t *testing.T) {
	t.Parallel()

	cases := []string{
		natsStoreDirModeTemp,
		" TEMP ",
		natsStoreDirModeEphemeral,
		" Ephemeral ",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			got := resolveNATSStoreDirRaw(raw, true)
			if !got.isEphemeral {
				t.Fatalf("expected ephemeral mode for %q", raw)
			}
			if got.storeDir != "" {
				t.Fatalf("expected empty storeDir for ephemeral mode, got %q", got.storeDir)
			}
		})
	}
}

func TestResolveNATSStoreDirRawUsesConfiguredPersistentDir(t *testing.T) {
	t.Parallel()

	got := resolveNATSStoreDirRaw(" ./runtime/state/nats ", true)
	if got.isEphemeral {
		t.Fatal("expected persistent mode")
	}
	if got.storeDir != "./runtime/state/nats" {
		t.Fatalf("unexpected resolved dir: %q", got.storeDir)
	}
}

func TestResolveArtifactsRootRawUsesEnvOverride(t *testing.T) {
	t.Parallel()

	got := resolveArtifactsRootRaw("darwin", " /tmp/my-artifacts ", true, "/Users/tester", "")
	if !got.fromEnv {
		t.Fatal("expected fromEnv=true for explicit override")
	}
	if got.root != "/tmp/my-artifacts" {
		t.Fatalf("unexpected override root: %q", got.root)
	}
}

func TestResolveArtifactsRootRawDefaultsByOS(t *testing.T) {
	t.Parallel()

	darwin := resolveArtifactsRootRaw("darwin", "", false, "/Users/tester", "")
	wantDarwin := filepath.Join(
		"/Users/tester",
		"Library",
		"Application Support",
		artifactsAppFolderName,
		"artifacts",
	)
	if darwin.root != wantDarwin {
		t.Fatalf("darwin default mismatch: got=%q want=%q", darwin.root, wantDarwin)
	}
	if darwin.fromEnv {
		t.Fatal("expected fromEnv=false for default resolution")
	}

	linuxXDG := resolveArtifactsRootRaw("linux", "", false, "/home/tester", "/var/lib/state")
	wantLinuxXDG := filepath.Join("/var/lib/state", artifactsAppFolderName, "artifacts")
	if linuxXDG.root != wantLinuxXDG {
		t.Fatalf("linux XDG default mismatch: got=%q want=%q", linuxXDG.root, wantLinuxXDG)
	}

	linuxHome := resolveArtifactsRootRaw("linux", "", false, "/home/tester", " ")
	wantLinuxHome := filepath.Join("/home/tester", ".local", "state", artifactsAppFolderName, "artifacts")
	if linuxHome.root != wantLinuxHome {
		t.Fatalf("linux home fallback mismatch: got=%q want=%q", linuxHome.root, wantLinuxHome)
	}
}

func TestShouldLogLegacyArtifactsMigrationNotice(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	legacyRoot := filepath.Join(baseDir, "legacy")
	if err := os.MkdirAll(legacyRoot, dirModePrivateRead); err != nil {
		t.Fatalf("mkdir legacy root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "marker.txt"), []byte("legacy"), fileModePrivate); err != nil {
		t.Fatalf("write legacy marker: %v", err)
	}

	t.Run("logs when legacy exists and new root is missing/empty", func(t *testing.T) {
		t.Parallel()
		res := artifactsRootResolution{
			root:       filepath.Join(baseDir, "new-root"),
			fromEnv:    false,
			legacyRoot: legacyRoot,
		}
		if !shouldLogLegacyArtifactsMigrationNotice(res) {
			t.Fatal("expected migration notice")
		}
	})

	t.Run("skips when root configured via env override", func(t *testing.T) {
		t.Parallel()
		res := artifactsRootResolution{
			root:       filepath.Join(baseDir, "new-root"),
			fromEnv:    true,
			legacyRoot: legacyRoot,
		}
		if shouldLogLegacyArtifactsMigrationNotice(res) {
			t.Fatal("expected migration notice suppression for env override")
		}
	})

	t.Run("skips when new root already has data", func(t *testing.T) {
		t.Parallel()
		newRoot := filepath.Join(baseDir, "has-data")
		if err := os.MkdirAll(newRoot, dirModePrivateRead); err != nil {
			t.Fatalf("mkdir new root: %v", err)
		}
		if err := os.WriteFile(filepath.Join(newRoot, "marker.txt"), []byte("new"), fileModePrivate); err != nil {
			t.Fatalf("write new marker: %v", err)
		}
		res := artifactsRootResolution{
			root:       newRoot,
			fromEnv:    false,
			legacyRoot: legacyRoot,
		}
		if shouldLogLegacyArtifactsMigrationNotice(res) {
			t.Fatal("expected no migration notice when new root is non-empty")
		}
	})
}
