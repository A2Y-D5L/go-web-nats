package platform

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
)
