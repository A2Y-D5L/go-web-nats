//nolint:testpackage // Runtime config tests validate unexported resolution helpers directly.
package platform

import "testing"

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
