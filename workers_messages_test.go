//nolint:exhaustruct // Message fixtures omit irrelevant fields to keep test intent obvious.
package platform_test

import (
	"encoding/json"
	"testing"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestWorkers_ResultCarriesOpKindAndSpecForNextWorker(t *testing.T) {
	in := platform.WorkerResultMsg{
		OpID:      "op-1",
		Kind:      platform.OpCreate,
		ProjectID: "proj-1",
		Spec: platform.ProjectSpec{
			APIVersion: platform.ProjectAPIVersionForTest,
			Kind:       platform.ProjectKindForTest,
			Name:       "svc",
			Runtime:    "go_1.26",
			Environments: map[string]platform.EnvConfig{
				"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
			},
			NetworkPolicies: platform.NetworkPolicies{
				Ingress: "internal",
				Egress:  "internal",
			},
		},
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal worker result: %v", err)
	}

	var opMsg platform.ProjectOpMsg
	unmarshalErr := json.Unmarshal(b, &opMsg)
	if unmarshalErr != nil {
		t.Fatalf("unmarshal as project op: %v", unmarshalErr)
	}

	if opMsg.Kind != platform.OpCreate {
		t.Fatalf("expected kind %q, got %q", platform.OpCreate, opMsg.Kind)
	}
	if opMsg.Spec.Name != "svc" || opMsg.Spec.Runtime != "go_1.26" {
		t.Fatalf("unexpected spec: %#v", opMsg.Spec)
	}
}
