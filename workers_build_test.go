package platform_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestWorkers_ParseImageBuilderMode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		raw       string
		wantMode  string
		wantError bool
	}{
		{name: "default empty", raw: "", wantMode: "buildkit", wantError: false},
		{name: "artifact explicit", raw: "artifact", wantMode: "artifact", wantError: false},
		{name: "artifact uppercase", raw: " ARTIFACT ", wantMode: "artifact", wantError: false},
		{name: "buildkit", raw: "buildkit", wantMode: "buildkit", wantError: false},
		{name: "buildkit uppercase", raw: " BuildKit ", wantMode: "buildkit", wantError: false},
		{name: "invalid", raw: "docker", wantMode: "buildkit", wantError: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotMode, err := platform.ParseImageBuilderModeForTest(tc.raw)
			if gotMode != tc.wantMode {
				t.Fatalf("mode mismatch: got %q want %q", gotMode, tc.wantMode)
			}
			if tc.wantError && err == nil {
				t.Fatal("expected parse error, got nil")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected no parse error, got %v", err)
			}
		})
	}
}

func TestWorkers_ImageBuilderModeFromEnvDefaultsAndInvalidFallback(t *testing.T) {
	t.Setenv("PAAS_IMAGE_BUILDER_MODE", "")
	mode, err := platform.ImageBuilderModeFromEnvForTest()
	if err != nil {
		t.Fatalf("default mode parse error: %v", err)
	}
	if mode != "buildkit" {
		t.Fatalf("default mode mismatch: got %q want buildkit", mode)
	}

	t.Setenv("PAAS_IMAGE_BUILDER_MODE", "not-a-mode")
	mode, err = platform.ImageBuilderModeFromEnvForTest()
	if err == nil {
		t.Fatal("expected invalid mode parse error")
	}
	if mode != "buildkit" {
		t.Fatalf("fallback mode mismatch: got %q want buildkit", mode)
	}
	if !strings.Contains(err.Error(), "PAAS_IMAGE_BUILDER_MODE") {
		t.Fatalf("expected env var name in error, got %v", err)
	}
}

func TestWorkers_ResolveEffectiveImageBuilderMode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		raw               string
		explicit          bool
		buildkitAvailable bool
		probeErr          string
		wantRequested     string
		wantEffective     string
		wantFallback      string
		wantPolicy        string
	}{
		{
			name:              "empty env falls back to artifact when buildkit binary support is missing",
			raw:               "",
			explicit:          false,
			buildkitAvailable: false,
			probeErr:          "",
			wantRequested:     "buildkit",
			wantEffective:     "artifact",
			wantFallback:      "buildkit support is unavailable in this binary",
			wantPolicy:        "",
		},
		{
			name:              "explicit artifact remains artifact",
			raw:               "artifact",
			explicit:          true,
			buildkitAvailable: false,
			probeErr:          "",
			wantRequested:     "artifact",
			wantEffective:     "artifact",
			wantFallback:      "",
			wantPolicy:        "",
		},
		{
			name:              "explicit buildkit remains buildkit when available",
			raw:               "buildkit",
			explicit:          true,
			buildkitAvailable: true,
			probeErr:          "",
			wantRequested:     "buildkit",
			wantEffective:     "buildkit",
			wantFallback:      "",
			wantPolicy:        "",
		},
		{
			name:              "explicit buildkit reports policy error when binary support is missing",
			raw:               "buildkit",
			explicit:          true,
			buildkitAvailable: false,
			probeErr:          "",
			wantRequested:     "buildkit",
			wantEffective:     "buildkit",
			wantFallback:      "",
			wantPolicy:        "explicit PAAS_IMAGE_BUILDER_MODE=buildkit requires a binary built with -tags buildkit",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotRequested, gotEffective, _, gotFallback, gotPolicy := platform.ResolveEffectiveImageBuilderModeForTest(
				tc.raw,
				tc.explicit,
				tc.buildkitAvailable,
				tc.probeErr,
			)
			if gotRequested != tc.wantRequested {
				t.Fatalf("requested mode mismatch: got %q want %q", gotRequested, tc.wantRequested)
			}
			if gotEffective != tc.wantEffective {
				t.Fatalf("effective mode mismatch: got %q want %q", gotEffective, tc.wantEffective)
			}
			if gotFallback != tc.wantFallback {
				t.Fatalf("fallback mismatch: got %q want %q", gotFallback, tc.wantFallback)
			}
			if gotPolicy != tc.wantPolicy {
				t.Fatalf("policy mismatch: got %q want %q", gotPolicy, tc.wantPolicy)
			}
		})
	}
}

func TestWorkers_SelectImageBuilderBackendByMode(t *testing.T) {
	name, err := platform.SelectImageBuilderBackendNameForTest("artifact")
	if err != nil {
		t.Fatalf("select artifact backend: %v", err)
	}
	if name != "artifact" {
		t.Fatalf("artifact backend mismatch: got %q", name)
	}

	name, err = platform.SelectImageBuilderBackendNameForTest("buildkit")
	if err != nil {
		t.Fatalf("select buildkit backend: %v", err)
	}
	if name != "buildkit" {
		t.Fatalf("buildkit backend mismatch: got %q", name)
	}
}

func TestWorkers_ImageBuilderArtifactModeWhenExplicitlySelected(t *testing.T) {
	t.Setenv("PAAS_IMAGE_BUILDER_MODE", "artifact")
	artifacts := platform.NewFSArtifacts(t.TempDir())
	msg, spec, imageTag := testBuildInputs()

	message, touched, err := platform.RunImageBuilderBuildForTest(context.Background(), artifacts, msg, spec, imageTag)
	if err != nil {
		t.Fatalf("run image builder: %v", err)
	}
	if message != "container image built and published to local daemon" {
		t.Fatalf("unexpected worker message: %q", message)
	}

	want := []string{
		"build/Dockerfile",
		"build/image.txt",
		"build/publish-local-daemon.json",
	}
	assertArtifactSet(t, touched, want)

	if slices.Contains(touched, "build/buildkit-metadata.json") {
		t.Fatalf("artifact mode should not emit buildkit metadata: %v", touched)
	}
}

func TestWorkers_ImageBuilderDefaultModeAutoFallbacksToArtifactWhenBuildkitUnavailable(t *testing.T) {
	t.Setenv("PAAS_IMAGE_BUILDER_MODE", "")
	artifacts := platform.NewFSArtifacts(t.TempDir())
	msg, spec, imageTag := testBuildInputs()

	message, touched, err := platform.RunImageBuilderBuildForTest(context.Background(), artifacts, msg, spec, imageTag)
	if err != nil {
		t.Fatalf("run image builder with default mode fallback: %v", err)
	}
	if message != "container image built and published to local daemon" {
		t.Fatalf("unexpected worker message: %q", message)
	}

	want := []string{
		"build/Dockerfile",
		"build/image.txt",
		"build/publish-local-daemon.json",
	}
	assertArtifactSet(t, touched, want)

	rawPublish, readErr := artifacts.ReadFile(msg.ProjectID, "build/publish-local-daemon.json")
	if readErr != nil {
		t.Fatalf("read publish metadata: %v", readErr)
	}
	var publish map[string]any
	if unmarshalErr := json.Unmarshal(rawPublish, &publish); unmarshalErr != nil {
		t.Fatalf("decode publish metadata: %v", unmarshalErr)
	}
	if publish["requested_builder_mode"] != "buildkit" {
		t.Fatalf("expected requested_builder_mode=buildkit, got %#v", publish["requested_builder_mode"])
	}
	if publish["effective_builder_mode"] != "artifact" {
		t.Fatalf("expected effective_builder_mode=artifact, got %#v", publish["effective_builder_mode"])
	}
	if publish["builder_mode_fallback_reason"] == "" {
		t.Fatalf(
			"expected builder_mode_fallback_reason to be present, got %#v",
			publish["builder_mode_fallback_reason"],
		)
	}
}

func TestWorkers_ImageBuilderBuildKitModeWritesMetadataArtifacts(t *testing.T) {
	artifacts := platform.NewFSArtifacts(t.TempDir())
	msg, spec, imageTag := testBuildInputs()

	message, touched, err := platform.RunImageBuilderBuildWithBackendForTest(
		context.Background(),
		artifacts,
		msg,
		spec,
		imageTag,
		"buildkit",
		"container image built and published to local daemon",
		"buildkit summary from test backend",
		"buildkit logs from test backend",
		map[string]any{
			"strategy":         "buildkit",
			"build_executed":   true,
			"exporter_backend": "stub",
		},
		"",
	)
	if err != nil {
		t.Fatalf("run buildkit image builder: %v", err)
	}
	if message != "container image built and published to local daemon" {
		t.Fatalf("unexpected worker message: %q", message)
	}

	want := []string{
		"build/Dockerfile",
		"build/buildkit-summary.txt",
		"build/buildkit-metadata.json",
		"build/buildkit.log",
		"build/image.txt",
		"build/publish-local-daemon.json",
	}
	assertArtifactSet(t, touched, want)

	rawMetadata, readErr := artifacts.ReadFile(msg.ProjectID, "build/buildkit-metadata.json")
	if readErr != nil {
		t.Fatalf("read buildkit metadata: %v", readErr)
	}
	var metadata map[string]any
	if unmarshalErr := json.Unmarshal(rawMetadata, &metadata); unmarshalErr != nil {
		t.Fatalf("decode buildkit metadata: %v", unmarshalErr)
	}
	if metadata["status"] != "ok" {
		t.Fatalf("expected status=ok in metadata, got %#v", metadata["status"])
	}
	if metadata["builder_mode"] != "buildkit" {
		t.Fatalf("expected builder_mode=buildkit in metadata, got %#v", metadata["builder_mode"])
	}
	if metadata["effective_builder_mode"] != "buildkit" {
		t.Fatalf(
			"expected effective_builder_mode=buildkit in metadata, got %#v",
			metadata["effective_builder_mode"],
		)
	}
}

func TestWorkers_ImageBuilderBuildKitModeFailsGracefully(t *testing.T) {
	artifacts := platform.NewFSArtifacts(t.TempDir())
	msg, spec, imageTag := testBuildInputs()

	_, touched, err := platform.RunImageBuilderBuildWithBackendForTest(
		context.Background(),
		artifacts,
		msg,
		spec,
		imageTag,
		"buildkit",
		"",
		"buildkit summary",
		"buildkit logs",
		map[string]any{"strategy": "buildkit"},
		"buildkit not available in test",
	)
	if err == nil {
		t.Fatal("expected buildkit mode error, got nil")
	}
	if !strings.Contains(err.Error(), "buildkit not available in test") {
		t.Fatalf("unexpected buildkit error: %v", err)
	}
	if slices.Contains(touched, "build/image.txt") {
		t.Fatalf("image marker should not be written on build failure: %v", touched)
	}
	if slices.Contains(touched, "build/publish-local-daemon.json") {
		t.Fatalf("publish metadata should not be written on build failure: %v", touched)
	}
	for _, artifact := range []string{
		"build/Dockerfile",
		"build/buildkit-summary.txt",
		"build/buildkit-metadata.json",
		"build/buildkit.log",
	} {
		if !slices.Contains(touched, artifact) {
			t.Fatalf("expected artifact %q in touched set: %v", artifact, touched)
		}
	}
}

func TestWorkers_ImageBuilderStepStartMessageIncludesEffectiveModeContext(t *testing.T) {
	message := platform.ImageBuilderStepStartMessageForTest("", false, false, "")
	if !strings.Contains(message, "requested=buildkit") {
		t.Fatalf("expected requested mode context in step message, got %q", message)
	}
	if !strings.Contains(message, "effective=artifact") {
		t.Fatalf("expected effective mode context in step message, got %q", message)
	}
	if !strings.Contains(message, "fallback=buildkit support is unavailable in this binary") {
		t.Fatalf("expected fallback reason in step message, got %q", message)
	}
}

func testBuildInputs() (platform.ProjectOpMsg, platform.ProjectSpec, string) {
	spec := platform.ProjectSpec{
		APIVersion: platform.ProjectAPIVersionForTest,
		Kind:       platform.ProjectKindForTest,
		Name:       "svc",
		Runtime:    "go_1.26",
		Capabilities: []string{
			"http",
		},
		Environments: map[string]platform.EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	}
	msg := platform.ProjectOpMsg{
		OpID:              "op-1234567890ab",
		Kind:              platform.OpCreate,
		ProjectID:         "proj-1",
		Spec:              spec,
		DeployEnv:         "",
		FromEnv:           "",
		ToEnv:             "",
		RollbackReleaseID: "",
		RollbackEnv:       "",
		RollbackScope:     "",
		RollbackOverride:  false,
		Delivery: platform.DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Err: "",
		At:  time.Now().UTC(),
	}
	return msg, spec, "local/svc:op-1234567890"
}

func assertArtifactSet(t *testing.T, got, want []string) {
	t.Helper()
	gotCopy := append([]string{}, got...)
	wantCopy := append([]string{}, want...)
	slices.Sort(gotCopy)
	slices.Sort(wantCopy)
	if !slices.Equal(gotCopy, wantCopy) {
		t.Fatalf("artifact set mismatch: got=%v want=%v", gotCopy, wantCopy)
	}
}
