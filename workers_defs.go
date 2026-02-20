package platform

import "context"

type Worker interface {
	Start(ctx context.Context) error
}

type WorkerBase struct {
	name       string
	natsURL    string
	subjectIn  string
	subjectOut string
	artifacts  ArtifactStore
}

func newWorkerBase(
	name, natsURL, subjectIn, subjectOut string,
	artifacts ArtifactStore,
) WorkerBase {
	return WorkerBase{
		name:       name,
		natsURL:    natsURL,
		subjectIn:  subjectIn,
		subjectOut: subjectOut,
		artifacts:  artifacts,
	}
}

type (
	RegistrationWorker     struct{ WorkerBase }
	RepoBootstrapWorker    struct{ WorkerBase }
	ImageBuilderWorker     struct{ WorkerBase }
	ManifestRendererWorker struct{ WorkerBase }
)

func NewRegistrationWorker(natsURL string, artifacts ArtifactStore) *RegistrationWorker {
	return &RegistrationWorker{
		WorkerBase: newWorkerBase(
			"registrar",
			natsURL,
			subjectProjectOpStart,
			subjectRegistrationDone,
			artifacts,
		),
	}
}

func NewRepoBootstrapWorker(natsURL string, artifacts ArtifactStore) *RepoBootstrapWorker {
	return &RepoBootstrapWorker{
		WorkerBase: newWorkerBase(
			"repoBootstrap",
			natsURL,
			subjectRegistrationDone,
			subjectBootstrapDone,
			artifacts,
		),
	}
}

func NewImageBuilderWorker(natsURL string, artifacts ArtifactStore) *ImageBuilderWorker {
	return &ImageBuilderWorker{
		WorkerBase: newWorkerBase(
			"imageBuilder",
			natsURL,
			subjectBootstrapDone,
			subjectBuildDone,
			artifacts,
		),
	}
}

func NewManifestRendererWorker(natsURL string, artifacts ArtifactStore) *ManifestRendererWorker {
	return &ManifestRendererWorker{
		WorkerBase: newWorkerBase(
			"manifestRenderer",
			natsURL,
			subjectBuildDone,
			subjectDeployDone,
			artifacts,
		),
	}
}

func (w *RegistrationWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		registrationWorkerAction,
	)
}

func (w *RepoBootstrapWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		repoBootstrapWorkerAction,
	)
}

func (w *ImageBuilderWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		imageBuilderWorkerAction,
	)
}

func (w *ManifestRendererWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		manifestRendererWorkerAction,
	)
}

type workerFn func(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error)
