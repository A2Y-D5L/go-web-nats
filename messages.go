package platform

import "time"

////////////////////////////////////////////////////////////////////////////////
// NATS messages
////////////////////////////////////////////////////////////////////////////////

type ProjectOpMsg struct {
	OpID      string        `json:"op_id"`
	Kind      OperationKind `json:"kind"`
	ProjectID string        `json:"project_id"`
	Spec      ProjectSpec   `json:"spec"` // create/update only
	DeployEnv string        `json:"deploy_env,omitempty"`
	FromEnv   string        `json:"from_env,omitempty"`
	ToEnv     string        `json:"to_env,omitempty"`
	Err       string        `json:"err,omitempty"`
	At        time.Time     `json:"at"`
}

type WorkerResultMsg struct {
	OpID      string        `json:"op_id"`
	Kind      OperationKind `json:"kind"`
	ProjectID string        `json:"project_id"`
	Spec      ProjectSpec   `json:"spec"`
	DeployEnv string        `json:"deploy_env,omitempty"`
	FromEnv   string        `json:"from_env,omitempty"`
	ToEnv     string        `json:"to_env,omitempty"`
	Worker    string        `json:"worker"`
	Message   string        `json:"message,omitempty"`
	Err       string        `json:"err,omitempty"`
	Artifacts []string      `json:"artifacts,omitempty"` // relative paths
	At        time.Time     `json:"at"`
}

func zeroProjectSpec() ProjectSpec {
	return ProjectSpec{
		APIVersion:      "",
		Kind:            "",
		Name:            "",
		Runtime:         "",
		Capabilities:    nil,
		Environments:    nil,
		NetworkPolicies: NetworkPolicies{Ingress: "", Egress: ""},
	}
}

func newWorkerResultMsg(message string) WorkerResultMsg {
	return WorkerResultMsg{
		OpID:      "",
		Kind:      "",
		ProjectID: "",
		Spec:      zeroProjectSpec(),
		DeployEnv: "",
		FromEnv:   "",
		ToEnv:     "",
		Worker:    "",
		Message:   message,
		Err:       "",
		Artifacts: nil,
		At:        time.Time{},
	}
}
