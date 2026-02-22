package platform

import (
	"context"
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
	natsStoreDirEnv      = "PAAS_NATS_STORE_DIR"

	defaultNATSStoreDir       = "./data/nats"
	natsStoreDirModeTemp      = "temp"
	natsStoreDirModeEphemeral = "ephemeral"
	buildOpTimeout            = 2 * time.Minute
	buildKitProbeTimeout      = 500 * time.Millisecond

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
	opEventsRetention         = 30 * time.Minute
	opEventsHeartbeatInterval = 10 * time.Second

	shortIDLength          = 12
	httpServerErrThreshold = 500
	httpClientErrThreshold = 400
	touchedArtifactsCap    = 8
	opEventsHistoryLimit   = 256
	opEventArtifactsLimit  = 8

	workerDeliveryAckWait    = 15 * time.Second
	workerDeliveryFetchWait  = 2 * time.Second
	workerDeliveryMaxDeliver = 5

	workerDeliveryStreamMaxAge   = 24 * time.Hour
	workerDeliveryStreamMaxMsgs  = int64(20000)
	workerDeliveryStreamMaxBytes = int64(64 * 1024 * 1024)
)

func workerDeliveryRetryBackoff() []time.Duration {
	return []time.Duration{
		1 * time.Second,
		2 * time.Second,
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
	}
}

type imageBuilderModeResolution struct {
	requestedMode     imageBuilderMode
	requestedExplicit bool
	effectiveMode     imageBuilderMode
	requestedWarning  string
	fallbackReason    string
	policyError       string
}

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
	mode, _, err := imageBuilderModeRequestFromEnv()
	return mode, err
}

func imageBuilderModeRequestFromEnv() (imageBuilderMode, bool, error) {
	raw, exists := os.LookupEnv(imageBuilderModeEnv)
	mode, err := parseImageBuilderMode(raw)
	return mode, exists && strings.TrimSpace(raw) != "", err
}

type buildkitProbeFunc func(ctx context.Context) error

type natsStoreDirResolution struct {
	storeDir    string
	isEphemeral bool
}

func resolveNATSStoreDir() natsStoreDirResolution {
	raw, exists := os.LookupEnv(natsStoreDirEnv)
	return resolveNATSStoreDirRaw(raw, exists)
}

func resolveNATSStoreDirRaw(raw string, exists bool) natsStoreDirResolution {
	if !exists {
		return natsStoreDirResolution{
			storeDir:    defaultNATSStoreDir,
			isEphemeral: false,
		}
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return natsStoreDirResolution{
			storeDir:    defaultNATSStoreDir,
			isEphemeral: false,
		}
	}
	switch strings.ToLower(trimmed) {
	case natsStoreDirModeTemp, natsStoreDirModeEphemeral:
		return natsStoreDirResolution{
			storeDir:    "",
			isEphemeral: true,
		}
	default:
		return natsStoreDirResolution{
			storeDir:    trimmed,
			isEphemeral: false,
		}
	}
}

func resolveEffectiveImageBuilderMode(ctx context.Context) imageBuilderModeResolution {
	requestedMode, requestedExplicit, parseErr := imageBuilderModeRequestFromEnv()
	return resolveEffectiveImageBuilderModeWithProbe(
		ctx,
		requestedMode,
		requestedExplicit,
		parseErr,
		buildkitCompiledIn(),
		probeBuildkitDaemonReachability,
	)
}

func resolveEffectiveImageBuilderModeWithProbe(
	ctx context.Context,
	requestedMode imageBuilderMode,
	requestedExplicit bool,
	parseErr error,
	buildkitAvailable bool,
	probe buildkitProbeFunc,
) imageBuilderModeResolution {
	resolution := imageBuilderModeResolution{
		requestedMode:     requestedMode,
		requestedExplicit: requestedExplicit,
		effectiveMode:     requestedMode,
		requestedWarning:  "",
		fallbackReason:    "",
		policyError:       "",
	}
	if parseErr != nil {
		resolution.requestedWarning = parseErr.Error()
	}
	if requestedMode != imageBuilderModeBuildKit {
		return resolution
	}
	if !buildkitAvailable {
		if requestedExplicit {
			resolution.policyError = fmt.Sprintf(
				"explicit %s=buildkit requires a binary built with -tags buildkit",
				imageBuilderModeEnv,
			)
			return resolution
		}
		resolution.effectiveMode = imageBuilderModeArtifact
		resolution.fallbackReason = "buildkit support is unavailable in this binary"
		return resolution
	}
	if probe == nil {
		return resolution
	}
	probeCtx, cancel := context.WithTimeout(ctx, buildKitProbeTimeout)
	defer cancel()
	if err := probe(probeCtx); err != nil {
		if requestedExplicit {
			resolution.policyError = fmt.Sprintf(
				"explicit %s=buildkit requested but BuildKit daemon is unreachable: %v",
				imageBuilderModeEnv,
				err,
			)
			return resolution
		}
		resolution.effectiveMode = imageBuilderModeArtifact
		resolution.fallbackReason = fmt.Sprintf("buildkit daemon is unreachable: %v", err)
	}
	return resolution
}
