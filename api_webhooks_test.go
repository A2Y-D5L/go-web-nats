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
