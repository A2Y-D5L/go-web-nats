//nolint:exhaustruct // Table-driven test cases omit unused fields for clarity.
package platform_test

import (
	"testing"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestAPI_IsMainBranchWebhook(t *testing.T) {
	cases := []struct {
		branch string
		ref    string
		want   bool
	}{
		{branch: "main", want: true},
		{branch: "Main", want: true},
		{branch: "refs/heads/main", want: true},
		{branch: "feature/test", want: false},
		{ref: "refs/heads/main", want: true},
		{ref: "heads/main", want: true},
		{ref: "refs/heads/dev", want: false},
		{ref: "main", want: true},
		{branch: "feature/x", ref: "refs/heads/main", want: true},
	}
	for _, tc := range cases {
		got := platform.IsMainBranchWebhookForTest(tc.branch, tc.ref)
		if got != tc.want {
			t.Fatalf("isMainBranchWebhook(%q,%q)=%v want %v", tc.branch, tc.ref, got, tc.want)
		}
	}
}

func TestAPI_CommitWatcherEnabledParsing(t *testing.T) {
	t.Setenv("PAAS_ENABLE_COMMIT_WATCHER", "")
	if platform.CommitWatcherEnabledForTest() {
		t.Fatal("expected watcher to be disabled when env is unset")
	}

	cases := []struct {
		raw  string
		want bool
	}{
		{raw: "1", want: true},
		{raw: "true", want: true},
		{raw: "TRUE", want: true},
		{raw: "0", want: false},
		{raw: "false", want: false},
		{raw: "off", want: false},
	}
	for _, tc := range cases {
		t.Setenv("PAAS_ENABLE_COMMIT_WATCHER", tc.raw)
		got := platform.CommitWatcherEnabledForTest()
		if got != tc.want {
			t.Fatalf("commitWatcherEnabled(%q)=%v want %v", tc.raw, got, tc.want)
		}
	}
}

func TestAPI_ShouldSkipSourceCommitMessage(t *testing.T) {
	cases := []struct {
		message string
		want    bool
	}{
		{message: "platform-sync: render manifests", want: true},
		{message: " platform-sync: bootstrap ", want: true},
		{message: "feat: add health endpoint", want: false},
		{message: "", want: false},
	}
	for _, tc := range cases {
		got := platform.ShouldSkipSourceCommitMessageForTest(tc.message)
		if got != tc.want {
			t.Fatalf("shouldSkipSourceCommitMessage(%q)=%v want %v", tc.message, got, tc.want)
		}
	}
}

func TestAPI_MarkSourceCommitSeen(t *testing.T) {
	api := platform.NewTestAPI(newMemArtifacts())

	firstSeen, err := platform.MarkSourceCommitSeenForTest(api, "p1", "abc123")
	if err != nil {
		t.Fatalf("first mark source commit: %v", err)
	}
	if !firstSeen {
		t.Fatal("expected first commit mark to be new")
	}

	secondSeen, err := platform.MarkSourceCommitSeenForTest(api, "p1", "abc123")
	if err != nil {
		t.Fatalf("second mark source commit: %v", err)
	}
	if secondSeen {
		t.Fatal("expected second mark with same commit to be duplicate")
	}

	blankSeen, err := platform.MarkSourceCommitSeenForTest(api, "p1", "")
	if err != nil {
		t.Fatalf("blank commit should not fail: %v", err)
	}
	if !blankSeen {
		t.Fatal("expected blank commit to bypass dedupe")
	}
}

func TestAPI_ProjectSupportsEnvironment(t *testing.T) {
	spec := platform.NormalizeProjectSpecForTest(platform.ProjectSpec{
		APIVersion: platform.ProjectAPIVersionForTest,
		Kind:       platform.ProjectKindForTest,
		Name:       "svc",
		Runtime:    "go_1.26",
		Environments: map[string]platform.EnvConfig{
			"staging": {Vars: map[string]string{}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	})

	if !platform.ProjectSupportsEnvironmentForTest(spec, "dev") {
		t.Fatal("expected dev to be supported by default deployment environment")
	}
	if !platform.ProjectSupportsEnvironmentForTest(spec, "staging") {
		t.Fatal("expected staging to be supported from project spec")
	}
	if platform.ProjectSupportsEnvironmentForTest(spec, "prod") {
		t.Fatal("expected prod to be unsupported when not defined in spec")
	}
}
