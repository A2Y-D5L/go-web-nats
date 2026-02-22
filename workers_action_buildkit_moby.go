//go:build buildkit

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

const (
	buildkitAddressEnv     = "PAAS_BUILDKIT_ADDR"
	defaultBuildkitAddress = "unix:///run/buildkit/buildkitd.sock"
)

type buildKitImageBuilderBackend struct{}

func buildkitCompiledIn() bool {
	return true
}

func probeBuildkitDaemonReachability(ctx context.Context) error {
	address := buildkitAddress()
	buildkitClient, err := client.New(ctx, address)
	if err != nil {
		return fmt.Errorf("connect buildkit client at %s: %w", address, err)
	}
	if closeErr := buildkitClient.Close(); closeErr != nil {
		return fmt.Errorf("close buildkit client at %s: %w", address, closeErr)
	}
	return nil
}

func (buildKitImageBuilderBackend) name() string {
	return string(imageBuilderModeBuildKit)
}

func (buildKitImageBuilderBackend) build(
	ctx context.Context,
	req imageBuildRequest,
) (imageBuildResult, error) {
	if err := ensureContextAlive(ctx); err != nil {
		return imageBuildResult{}, err
	}

	// Parse first so malformed generated Dockerfiles fail fast with a clear error.
	if _, err := parser.Parse(strings.NewReader(string(req.DockerfileBody))); err != nil {
		return imageBuildResult{}, fmt.Errorf("parse generated dockerfile: %w", err)
	}
	dockerfileDir, err := os.MkdirTemp("", "paas-buildkit-dockerfile-")
	if err != nil {
		return imageBuildResult{}, fmt.Errorf("create buildkit dockerfile temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(dockerfileDir)
	}()
	dockerfileName := "Dockerfile"
	dockerfilePath := filepath.Join(dockerfileDir, dockerfileName)
	if err := os.WriteFile(dockerfilePath, req.DockerfileBody, fileModePrivate); err != nil {
		return imageBuildResult{}, fmt.Errorf("write buildkit dockerfile input: %w", err)
	}

	address := buildkitAddress()
	buildkitClient, err := client.New(ctx, address)
	if err != nil {
		return imageBuildResult{
			message: "buildkit image build unavailable",
			summary: fmt.Sprintf("buildkit unavailable at %s: %v", address, err),
			metadata: map[string]any{
				"strategy":       "buildkit",
				"build_executed": false,
				"address":        address,
			},
			logs: "failed to connect to BuildKit API endpoint",
		}, fmt.Errorf("connect buildkit client at %s: %w", address, err)
	}
	defer func() {
		_ = buildkitClient.Close()
	}()

	solveOpt := client.SolveOpt{
		Frontend: "dockerfile.v0",
		FrontendAttrs: map[string]string{
			"filename": dockerfileName,
		},
		LocalDirs: map[string]string{
			"context":    req.ContextDir,
			"dockerfile": dockerfileDir,
		},
		Exports: []client.ExportEntry{
			{
				Type: "image",
				Attrs: map[string]string{
					"name": req.ImageTag,
					"push": "false",
				},
			},
		},
	}
	solveResp, err := buildkitClient.Solve(ctx, nil, solveOpt, nil)
	if err != nil {
		return imageBuildResult{
			message: "buildkit image build failed",
			summary: fmt.Sprintf("buildkit solve failed for %s: %v", req.ImageTag, err),
			metadata: map[string]any{
				"strategy":       "buildkit",
				"build_executed": false,
				"address":        address,
			},
			logs: "buildkit solve returned an error",
		}, fmt.Errorf("solve image %s via buildkit: %w", req.ImageTag, err)
	}

	return imageBuildResult{
		message: "container image built and published to local daemon",
		summary: "buildkit solve completed successfully",
		metadata: map[string]any{
			"strategy":           "buildkit",
			"build_executed":     true,
			"address":            address,
			"solve_completed_at": time.Now().UTC().Format(time.RFC3339),
			"exporter_response":  solveResp.ExporterResponse,
		},
		logs: "buildkit solve completed with frontend=dockerfile.v0 exporter=image",
	}, nil
}

func buildkitAddress() string {
	raw := strings.TrimSpace(os.Getenv(buildkitAddressEnv))
	if raw == "" {
		return defaultBuildkitAddress
	}
	return raw
}
