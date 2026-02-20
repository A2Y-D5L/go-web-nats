package platform

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
// Domain model: Projects + Operations
////////////////////////////////////////////////////////////////////////////////

type EnvConfig struct {
	Vars map[string]string `json:"vars"`
}

type NetworkPolicies struct {
	Ingress string `json:"ingress"`
	Egress  string `json:"egress"`
}

type ProjectSpec struct {
	APIVersion      string               `json:"apiVersion"`
	Kind            string               `json:"kind"`
	Name            string               `json:"name"`
	Runtime         string               `json:"runtime"`
	Capabilities    []string             `json:"capabilities,omitempty"`
	Environments    map[string]EnvConfig `json:"environments"`
	NetworkPolicies NetworkPolicies      `json:"networkPolicies"`
}

type ProjectStatus struct {
	Phase      string    `json:"phase"`        // Ready | Reconciling | Deleting | Error
	UpdatedAt  time.Time `json:"updated_at"`   //
	LastOpID   string    `json:"last_op_id"`   //
	LastOpKind string    `json:"last_op_kind"` // create|update|delete|ci|deploy|promote
	Message    string    `json:"message,omitempty"`
}

type Project struct {
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Spec      ProjectSpec   `json:"spec"`
	Status    ProjectStatus `json:"status"`
}

type OperationKind string

const (
	OpCreate  OperationKind = "create"
	OpUpdate  OperationKind = "update"
	OpDelete  OperationKind = "delete"
	OpCI      OperationKind = "ci"
	OpDeploy  OperationKind = "deploy"
	OpPromote OperationKind = "promote"
)

type OpStep struct {
	Worker    string    `json:"worker"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Message   string    `json:"message,omitempty"`
	Error     string    `json:"error,omitempty"`
	Artifacts []string  `json:"artifacts,omitempty"` // relative paths
}

type Operation struct {
	ID        string        `json:"id"`
	Kind      OperationKind `json:"kind"`
	ProjectID string        `json:"project_id"`
	Requested time.Time     `json:"requested"`
	Finished  time.Time     `json:"finished"`
	Status    string        `json:"status"` // queued|running|done|error
	Error     string        `json:"error,omitempty"`
	Steps     []OpStep      `json:"steps"`
}

var (
	projectNameRe  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	runtimeRe      = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*(\.[0-9]+(\.[0-9]+)*)?$`)
	capabilityRe   = regexp.MustCompile(`^[a-z][a-z0-9_\-]*[a-z0-9]$`)
	envNameRe      = regexp.MustCompile(`^[a-z][a-z0-9_\-]*[a-z0-9]$`)
	envVarNameRe   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	networkValueRe = regexp.MustCompile(`^(internal|none)$`)
)

func normalizeProjectSpec(in ProjectSpec) ProjectSpec {
	spec := in
	spec.APIVersion = strings.TrimSpace(spec.APIVersion)
	if spec.APIVersion == "" {
		spec.APIVersion = projectAPIVersion
	}
	spec.Kind = strings.TrimSpace(spec.Kind)
	if spec.Kind == "" {
		spec.Kind = projectKind
	}

	spec.Name = strings.TrimSpace(spec.Name)
	spec.Runtime = strings.TrimSpace(spec.Runtime)

	spec.NetworkPolicies.Ingress = strings.TrimSpace(spec.NetworkPolicies.Ingress)
	spec.NetworkPolicies.Egress = strings.TrimSpace(spec.NetworkPolicies.Egress)
	if spec.NetworkPolicies.Ingress == "" {
		spec.NetworkPolicies.Ingress = networkPolicyInternal
	}
	if spec.NetworkPolicies.Egress == "" {
		spec.NetworkPolicies.Egress = networkPolicyInternal
	}

	seenCaps := map[string]struct{}{}
	var caps []string
	for _, c := range spec.Capabilities {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seenCaps[c]; ok {
			continue
		}
		seenCaps[c] = struct{}{}
		caps = append(caps, c)
	}
	spec.Capabilities = caps

	if spec.Environments == nil {
		spec.Environments = map[string]EnvConfig{}
	}
	for envName, envCfg := range spec.Environments {
		if envCfg.Vars == nil {
			envCfg.Vars = map[string]string{}
		}
		spec.Environments[envName] = envCfg
	}
	return spec
}

func validateProjectSpec(spec ProjectSpec) error {
	if err := validateProjectCore(spec); err != nil {
		return err
	}
	if err := validateCapabilities(spec.Capabilities); err != nil {
		return err
	}
	if err := validateEnvironments(spec.Environments); err != nil {
		return err
	}
	return validateNetworkPolicies(spec.NetworkPolicies)
}

func validateProjectCore(spec ProjectSpec) error {
	if spec.APIVersion != projectAPIVersion {
		return fmt.Errorf("apiVersion must be %q", projectAPIVersion)
	}
	if spec.Kind != projectKind {
		return fmt.Errorf("kind must be %q", projectKind)
	}
	if len(spec.Name) < 1 || len(spec.Name) > 63 || !projectNameRe.MatchString(spec.Name) {
		return fmt.Errorf("name must match %s", projectNameRe.String())
	}
	if len(spec.Runtime) < 1 || len(spec.Runtime) > 128 || !runtimeRe.MatchString(spec.Runtime) {
		return fmt.Errorf("runtime must match %s", runtimeRe.String())
	}
	return nil
}

func validateCapabilities(capabilities []string) error {
	for _, capability := range capabilities {
		if len(capability) > 64 || !capabilityRe.MatchString(capability) {
			return fmt.Errorf("invalid capability %q", capability)
		}
	}
	return nil
}

func validateEnvironments(envs map[string]EnvConfig) error {
	if len(envs) < 1 {
		return errors.New("environments must include at least one environment")
	}
	for envName, envCfg := range envs {
		if len(envName) > 32 || !envNameRe.MatchString(envName) {
			return fmt.Errorf("invalid environment name %q", envName)
		}
		if err := validateEnvironmentVars(envName, envCfg.Vars); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvironmentVars(envName string, vars map[string]string) error {
	for key, value := range vars {
		if len(key) > 128 || !envVarNameRe.MatchString(key) {
			return fmt.Errorf("invalid environment variable name %q in %q", key, envName)
		}
		if len(value) > maxEnvVarValueLength {
			return fmt.Errorf("env var %q in %q exceeds max length", key, envName)
		}
	}
	return nil
}

func validateNetworkPolicies(policies NetworkPolicies) error {
	if !networkValueRe.MatchString(policies.Ingress) {
		return errors.New("networkPolicies.ingress must be internal or none")
	}
	if !networkValueRe.MatchString(policies.Egress) {
		return errors.New("networkPolicies.egress must be internal or none")
	}
	return nil
}
