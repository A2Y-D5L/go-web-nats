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
	opEvents   *opEventHub
}

func newWorkerBase(
	name, natsURL, subjectIn, subjectOut string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
) WorkerBase {
	return WorkerBase{
		name:       name,
		natsURL:    natsURL,
		subjectIn:  subjectIn,
		subjectOut: subjectOut,
		artifacts:  artifacts,
		opEvents:   opEvents,
	}
}

type (
	RegistrationWorker  struct{ WorkerBase }
	RepoBootstrapWorker struct{ WorkerBase }
	ImageBuilderWorker  struct {
		WorkerBase

		modeResolution imageBuilderModeResolution
	}
	ManifestRendererWorker struct{ WorkerBase }
	DeploymentWorker       struct{ WorkerBase }
	PromotionWorker        struct{ WorkerBase }
)

func NewRegistrationWorker(
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
) *RegistrationWorker {
	return &RegistrationWorker{
		WorkerBase: newWorkerBase(
			"registrar",
			natsURL,
			subjectProjectOpStart,
			subjectRegistrationDone,
			artifacts,
			opEvents,
		),
	}
}

func NewRepoBootstrapWorker(
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
) *RepoBootstrapWorker {
	return &RepoBootstrapWorker{
		WorkerBase: newWorkerBase(
			"repoBootstrap",
			natsURL,
			subjectRegistrationDone,
			subjectBootstrapDone,
			artifacts,
			opEvents,
		),
	}
}

func NewImageBuilderWorker(
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
	modeResolution imageBuilderModeResolution,
) *ImageBuilderWorker {
	return &ImageBuilderWorker{
		WorkerBase: newWorkerBase(
			"imageBuilder",
			natsURL,
			subjectBootstrapDone,
			subjectBuildDone,
			artifacts,
			opEvents,
		),
		modeResolution: modeResolution,
	}
}

func NewManifestRendererWorker(
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
) *ManifestRendererWorker {
	return &ManifestRendererWorker{
		WorkerBase: newWorkerBase(
			"manifestRenderer",
			natsURL,
			subjectBuildDone,
			subjectDeployDone,
			artifacts,
			opEvents,
		),
	}
}

func NewDeploymentWorker(
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
) *DeploymentWorker {
	return &DeploymentWorker{
		WorkerBase: newWorkerBase(
			"deployer",
			natsURL,
			subjectDeploymentStart,
			subjectDeploymentDone,
			artifacts,
			opEvents,
		),
	}
}

func NewPromotionWorker(
	natsURL string,
	artifacts ArtifactStore,
	opEvents *opEventHub,
) *PromotionWorker {
	return &PromotionWorker{
		WorkerBase: newWorkerBase(
			"promoter",
			natsURL,
			subjectPromotionStart,
			subjectPromotionDone,
			artifacts,
			opEvents,
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
		w.opEvents,
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
		w.opEvents,
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
		w.opEvents,
		func(
			actionCtx context.Context,
			store *Store,
			artifacts ArtifactStore,
			msg ProjectOpMsg,
		) (WorkerResultMsg, error) {
			return imageBuilderWorkerActionWithMode(
				actionCtx,
				store,
				artifacts,
				msg,
				w.modeResolution,
			)
		},
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
		w.opEvents,
		manifestRendererWorkerAction,
	)
}

func (w *DeploymentWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		w.opEvents,
		deploymentWorkerAction,
	)
}

func (w *PromotionWorker) Start(ctx context.Context) error {
	return startWorker(
		ctx,
		w.name,
		w.natsURL,
		w.subjectIn,
		w.subjectOut,
		w.artifacts,
		w.opEvents,
		promotionWorkerAction,
	)
}

type workerFn func(ctx context.Context, store *Store, artifacts ArtifactStore, msg ProjectOpMsg) (WorkerResultMsg, error)
