//go:build !buildkit

package platform

import (
	"context"
	"fmt"
)

type buildKitImageBuilderBackend struct{}

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
	buildErr := fmt.Errorf(
		"buildkit mode unavailable: binary was built without BuildKit support (set %s=artifact or rebuild with -tags buildkit)",
		imageBuilderModeEnv,
	)
	return imageBuildResult{
		message: "buildkit image build unavailable",
		summary: buildErr.Error(),
		metadata: map[string]any{
			"strategy":       "buildkit",
			"context_dir":    req.ContextDir,
			"dockerfile":     req.DockerfileRelPath,
			"build_executed": false,
		},
		logs: "BuildKit backend is disabled in this binary",
	}, buildErr
}
