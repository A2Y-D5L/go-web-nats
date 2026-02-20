package platform

import (
	"os"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
// Subjects (register -> bootstrap -> build -> deploy chain) + KV buckets
////////////////////////////////////////////////////////////////////////////////

const (
	// API publishes project operations here.
	subjectProjectOpStart = "paas.project.op.start"

	// Worker pipeline chain.
	subjectRegistrationDone = "paas.project.op.registration.done"
	subjectBootstrapDone    = "paas.project.op.bootstrap.done"
	subjectBuildDone        = "paas.project.op.build.done"
	subjectDeployDone       = "paas.project.op.deploy.done"

	// KV buckets.
	kvBucketProjects = "paas_projects"
	kvBucketOps      = "paas_ops"

	// Project keys in KV.
	kvProjectKeyPrefix = "project/"
	kvOpKeyPrefix      = "op/"

	// HTTP.
	httpAddr = "127.0.0.1:8080"

	// Where workers write artifacts.
	defaultArtifactsRoot = "./data/artifacts"

	// API wait timeout per request.
	apiWaitTimeout = 45 * time.Second

	// Schema defaults (from cfg/project-jsonschema.json).
	projectAPIVersion = "platform.example.com/v2"
	projectKind       = "App"

	defaultKVProjectHistory = 25
	defaultKVOpsHistory     = 50
	defaultStartupWait      = 10 * time.Second
	defaultReadHeaderWait   = 5 * time.Second
	gitOpTimeout            = 20 * time.Second
	gitReadTimeout          = 10 * time.Second
	maxEnvVarValueLength    = 4096
	shortIDLength           = 12
	httpServerErrThreshold  = 500
	httpClientErrThreshold  = 400

	fileModePrivate        os.FileMode = 0o600
	fileModeExecPrivate    os.FileMode = 0o700
	dirModePrivateRead     os.FileMode = 0o750
	projectRelPathPartsMin             = 2
	touchedArtifactsCap                = 8

	networkPolicyInternal = "internal"
	branchMain            = "main"
	projectPhaseReady     = "Ready"
	projectPhaseError     = "Error"
	projectPhaseDel       = "Deleting"
)
