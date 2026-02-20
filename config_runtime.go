package platform

import (
	"fmt"
	"os"
	"strings"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
// Runtime defaults and tunables
////////////////////////////////////////////////////////////////////////////////

type imageBuilderMode string

const (
	// HTTP.
	httpAddr = "127.0.0.1:8080"

	// Where workers write artifacts.
	defaultArtifactsRoot = "./data/artifacts"
	imageBuilderModeEnv  = "PAAS_IMAGE_BUILDER_MODE"
	buildOpTimeout       = 2 * time.Minute

	imageBuilderModeArtifact imageBuilderMode = "artifact"
	imageBuilderModeBuildKit imageBuilderMode = "buildkit"

	defaultKVProjectHistory   = 25
	defaultKVOpsHistory       = 50
	defaultStartupWait        = 10 * time.Second
	defaultReadHeaderWait     = 5 * time.Second
	apiWaitTimeout            = 45 * time.Second
	gitOpTimeout              = 20 * time.Second
	gitReadTimeout            = 10 * time.Second
	commitWatcherPollInterval = 2 * time.Second

	shortIDLength          = 12
	httpServerErrThreshold = 500
	httpClientErrThreshold = 400
	touchedArtifactsCap    = 8
)

func parseImageBuilderMode(raw string) (imageBuilderMode, error) {
	mode := strings.TrimSpace(strings.ToLower(raw))
	switch mode {
	case "":
		return imageBuilderModeBuildKit, nil
	case string(imageBuilderModeArtifact):
		return imageBuilderModeArtifact, nil
	case string(imageBuilderModeBuildKit):
		return imageBuilderModeBuildKit, nil
	default:
		return imageBuilderModeBuildKit, fmt.Errorf(
			"invalid %s=%q (expected %s or %s)",
			imageBuilderModeEnv,
			raw,
			imageBuilderModeArtifact,
			imageBuilderModeBuildKit,
		)
	}
}

func imageBuilderModeFromEnv() (imageBuilderMode, error) {
	return parseImageBuilderMode(os.Getenv(imageBuilderModeEnv))
}
