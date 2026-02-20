package platform

////////////////////////////////////////////////////////////////////////////////
// Domain contracts/defaults
////////////////////////////////////////////////////////////////////////////////

const (
	// Schema defaults (from cfg/project-jsonschema.json).
	projectAPIVersion = "platform.example.com/v2"
	projectKind       = "App"

	maxEnvVarValueLength  = 4096
	networkPolicyInternal = "internal"
	branchMain            = "main"
	projectPhaseReady     = "Ready"
	projectPhaseError     = "Error"
	projectPhaseDel       = "Deleting"
)
