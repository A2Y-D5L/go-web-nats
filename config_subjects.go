package platform

////////////////////////////////////////////////////////////////////////////////
// Subjects (register -> bootstrap -> build -> deploy chain) + process channels + KV buckets
////////////////////////////////////////////////////////////////////////////////

const (
	// API publishes project operations here.
	subjectProjectOpStart = "paas.project.op.start"

	// Worker pipeline chain.
	subjectRegistrationDone = "paas.project.op.registration.done"
	subjectBootstrapDone    = "paas.project.op.bootstrap.done"
	subjectBuildDone        = "paas.project.op.build.done"
	subjectDeployDone       = "paas.project.op.deploy.done"

	// Standalone process subjects.
	subjectDeploymentStart = "paas.project.process.deployment.start"
	subjectDeploymentDone  = "paas.project.process.deployment.done"
	subjectPromotionStart  = "paas.project.process.promotion.start"
	subjectPromotionDone   = "paas.project.process.promotion.done"

	// KV buckets.
	kvBucketProjects = "paas_projects"
	kvBucketOps      = "paas_ops"

	// Project keys in KV.
	kvProjectKeyPrefix = "project/"
	kvOpKeyPrefix      = "op/"
)
