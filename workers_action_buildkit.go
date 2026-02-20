package platform

import "context"

type imageBuildRequest struct {
	OpID              string
	ProjectID         string
	Spec              ProjectSpec
	ImageTag          string
	ContextDir        string
	DockerfileBody    []byte
	DockerfileRelPath string
}

type imageBuildResult struct {
	message  string
	summary  string
	metadata map[string]any
	logs     string
}

type imageBuilderBackend interface {
	name() string
	build(ctx context.Context, req imageBuildRequest) (imageBuildResult, error)
}
