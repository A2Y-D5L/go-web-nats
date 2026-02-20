package platform

import "time"

////////////////////////////////////////////////////////////////////////////////
// Runtime defaults and tunables
////////////////////////////////////////////////////////////////////////////////

const (
	// HTTP.
	httpAddr = "127.0.0.1:8080"

	// Where workers write artifacts.
	defaultArtifactsRoot = "./data/artifacts"

	defaultKVProjectHistory = 25
	defaultKVOpsHistory     = 50
	defaultStartupWait      = 10 * time.Second
	defaultReadHeaderWait   = 5 * time.Second
	apiWaitTimeout          = 45 * time.Second
	gitOpTimeout            = 20 * time.Second
	gitReadTimeout          = 10 * time.Second

	shortIDLength          = 12
	httpServerErrThreshold = 500
	httpClientErrThreshold = 400
	touchedArtifactsCap    = 8
)
