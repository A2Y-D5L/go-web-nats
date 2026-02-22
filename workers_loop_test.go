//nolint:testpackage // Worker runtime tests exercise unexported retry/idempotency helpers.
package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type workerDeliveryFixture struct {
	nc    *nats.Conn
	js    jetstream.JetStream
	store *Store
	close func()
}

func newWorkerDeliveryFixture(t *testing.T) *workerDeliveryFixture {
	t.Helper()
	t.Setenv(natsStoreDirEnv, natsStoreDirModeTemp)

	ns, natsURL, nsDir, nsDirTmp, err := startEmbeddedNATS()
	if err != nil {
		t.Skipf("embedded nats unavailable: %v", err)
	}

	nc, err := nats.Connect(natsURL, nats.Name("workers-runtime-test"))
	if err != nil {
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
		t.Skipf("nats connect unavailable: %v", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
		t.Skipf("jetstream setup unavailable: %v", err)
	}

	streamErr := ensureWorkerDeliveryStream(context.Background(), js)
	if streamErr != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
		t.Skipf("worker stream setup unavailable: %v", streamErr)
	}

	store, err := newStore(context.Background(), js)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
		t.Skipf("store setup unavailable: %v", err)
	}

	return &workerDeliveryFixture{
		nc:    nc,
		js:    js,
		store: store,
		close: func() {
			_ = nc.Drain()
			ns.Shutdown()
			ns.WaitForShutdown()
			if nsDirTmp {
				_ = os.RemoveAll(nsDir)
			}
		},
	}
}

func (f *workerDeliveryFixture) Close() {
	if f == nil || f.close == nil {
		return
	}
	f.close()
}

func workerRuntimeSpec(name string) ProjectSpec {
	var spec ProjectSpec
	spec.APIVersion = projectAPIVersion
	spec.Kind = projectKind
	spec.Name = name
	spec.Runtime = "go_1.26"
	spec.Environments = map[string]EnvConfig{
		"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
	}
	spec.NetworkPolicies = NetworkPolicies{
		Ingress: networkPolicyInternal,
		Egress:  networkPolicyInternal,
	}
	return normalizeProjectSpec(spec)
}

func putWorkerRuntimeProjectAndOp(
	t *testing.T,
	store *Store,
	projectID string,
	opID string,
	kind OperationKind,
	spec ProjectSpec,
) {
	t.Helper()
	now := time.Now().UTC()
	project := Project{
		ID:        projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Spec:      spec,
		Status: ProjectStatus{
			Phase:      projectPhaseReady,
			UpdatedAt:  now,
			LastOpID:   "",
			LastOpKind: "",
			Message:    "ready",
		},
	}
	if err := store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("put project: %v", err)
	}
	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Requested: now,
		Finished:  time.Time{},
		Status:    statusMessageQueued,
		Error:     "",
		Steps:     []OpStep{},
	}
	if err := store.PutOp(context.Background(), op); err != nil {
		t.Fatalf("put op: %v", err)
	}
}

func workerPayload(t *testing.T, opID string, kind OperationKind, projectID string, spec ProjectSpec) []byte {
	t.Helper()
	body, err := json.Marshal(ProjectOpMsg{
		OpID:      opID,
		Kind:      kind,
		ProjectID: projectID,
		Spec:      spec,
		DeployEnv: "",
		FromEnv:   "",
		ToEnv:     "",
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}

func workerRuntimeActionSuccess(
	ctx context.Context,
	store *Store,
	_ ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	start := time.Now().UTC()
	_ = markOpStepStart(ctx, store, msg.OpID, "registrar", start, "register app configuration")
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"registrar",
		time.Now().UTC(),
		"project registration upserted",
		"",
		[]string{"registration/project.yaml"},
	)
	return WorkerResultMsg{
		OpID:      "",
		Kind:      "",
		ProjectID: "",
		Spec:      zeroProjectSpec(),
		DeployEnv: "",
		FromEnv:   "",
		ToEnv:     "",
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Worker:    "",
		Message:   "project registration upserted",
		Err:       "",
		Artifacts: []string{"registration/project.yaml"},
		At:        time.Time{},
	}, nil
}

func TestWorkers_JetStreamRetryAvoidsDuplicateStepMutation(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	spec := workerRuntimeSpec("worker-retry")
	opID := "op-worker-retry-1"
	projectID := "project-worker-retry-1"
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpCreate, spec)

	publishAttempts := 0
	resultPublisher := func(
		ctx context.Context,
		js jetstream.JetStream,
		subject string,
		res WorkerResultMsg,
	) error {
		publishAttempts++
		if publishAttempts == 1 {
			return errors.New("simulated transient publish failure")
		}
		return publishWorkerResult(ctx, js, subject, res)
	}

	data := workerPayload(t, opID, OpCreate, projectID, spec)
	log := appLoggerForProcess().Source("workers-test")

	first := handleWorkerDelivery(
		context.Background(),
		fixture.store,
		NewFSArtifacts(t.TempDir()),
		"registrar",
		subjectProjectOpStart,
		subjectRegistrationDone,
		workerRuntimeActionSuccess,
		fixture.js,
		data,
		1,
		log,
		resultPublisher,
		publishWorkerPoison,
	)
	if first.action != workerDeliveryRetry {
		t.Fatalf("expected retry action on first publish failure, got %d", first.action)
	}

	second := handleWorkerDelivery(
		context.Background(),
		fixture.store,
		NewFSArtifacts(t.TempDir()),
		"registrar",
		subjectProjectOpStart,
		subjectRegistrationDone,
		workerRuntimeActionSuccess,
		fixture.js,
		data,
		2,
		log,
		resultPublisher,
		publishWorkerPoison,
	)
	if second.action != workerDeliveryAck {
		t.Fatalf("expected ack action on redelivery, got %d", second.action)
	}

	op, err := fixture.store.GetOp(context.Background(), opID)
	if err != nil {
		t.Fatalf("get op after retries: %v", err)
	}
	if got := len(op.Steps); got != 1 {
		t.Fatalf("expected exactly one worker step after retry handling, got %d", got)
	}
	if op.Steps[0].Worker != "registrar" {
		t.Fatalf("expected worker registrar, got %q", op.Steps[0].Worker)
	}
	if op.Steps[0].EndedAt.IsZero() {
		t.Fatal("expected ended step timestamp to be set")
	}
	if strings.TrimSpace(op.Steps[0].Error) != "" {
		t.Fatalf("expected empty step error, got %q", op.Steps[0].Error)
	}
	if publishAttempts != 2 {
		t.Fatalf("expected two publish attempts, got %d", publishAttempts)
	}
}

func TestWorkers_JetStreamPoisonMarksOpErrorAfterRetryExhaustion(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	spec := workerRuntimeSpec("worker-poison")
	opID := "op-worker-poison-1"
	projectID := "project-worker-poison-1"
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpCreate, spec)

	resultPublisher := func(
		_ context.Context,
		_ jetstream.JetStream,
		_ string,
		_ WorkerResultMsg,
	) error {
		return errors.New("simulated persistent publish failure")
	}

	data := workerPayload(t, opID, OpCreate, projectID, spec)
	log := appLoggerForProcess().Source("workers-test")

	decision := handleWorkerDelivery(
		context.Background(),
		fixture.store,
		NewFSArtifacts(t.TempDir()),
		"registrar",
		subjectProjectOpStart,
		subjectRegistrationDone,
		workerRuntimeActionSuccess,
		fixture.js,
		data,
		uint64(workerDeliveryMaxDeliver),
		log,
		resultPublisher,
		publishWorkerPoison,
	)
	if decision.action != workerDeliveryTerminate {
		t.Fatalf("expected terminate action on retry exhaustion, got %d", decision.action)
	}

	op, err := fixture.store.GetOp(context.Background(), opID)
	if err != nil {
		t.Fatalf("get op after poison: %v", err)
	}
	if op.Status != opStatusError {
		t.Fatalf("expected op status %q, got %q", opStatusError, op.Status)
	}
	if !strings.Contains(op.Error, "worker delivery exhausted retries") {
		t.Fatalf("expected poison error message, got %q", op.Error)
	}

	stream, err := fixture.js.Stream(context.Background(), streamWorkerPipeline)
	if err != nil {
		t.Fatalf("get worker stream: %v", err)
	}
	info, err := stream.Info(context.Background(), jetstream.WithSubjectFilter(subjectWorkerPoison))
	if err != nil {
		t.Fatalf("stream info for poison subject: %v", err)
	}
	if info.State.Msgs < 1 {
		t.Fatalf("expected poison subject to contain at least one message, got %d", info.State.Msgs)
	}
}

func TestWorkers_BookkeepingEmitsSingleTerminalEventPerFailureState(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	hub := newOpEventHub(opEventsHistoryLimit, opEventsRetention)
	fixture.store.setOpEvents(hub)

	spec := workerRuntimeSpec("worker-events")
	opID := "op-worker-events-1"
	projectID := "project-worker-events-1"
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpDeploy, spec)

	err := markOpStepStart(
		context.Background(),
		fixture.store,
		opID,
		"deployer",
		time.Now().UTC(),
		"deploy manifests for a single environment",
	)
	if err != nil {
		t.Fatalf("mark step start: %v", err)
	}
	err = finalizeOp(context.Background(), fixture.store, opID, projectID, OpDeploy, opStatusError, "boom")
	if err != nil {
		t.Fatalf("finalize op error: %v", err)
	}
	err = markOpStepEnd(
		context.Background(),
		fixture.store,
		opID,
		"deployer",
		time.Now().UTC(),
		"",
		"boom",
		nil,
	)
	if err != nil {
		t.Fatalf("mark step end: %v", err)
	}

	hub.mu.Lock()
	stream := hub.streams[opID]
	records := append([]opEventRecord(nil), stream.records...)
	hub.mu.Unlock()

	failedEvents := 0
	for _, record := range records {
		if record.Name == opEventFailed {
			failedEvents++
		}
	}
	if failedEvents != 1 {
		t.Fatalf("expected exactly one %q event, got %d", opEventFailed, failedEvents)
	}
}

func TestWorkers_FinalWaiterReceivesJetStreamPublishedFinalResult(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	waiters := newWaiterHub()
	subs, err := subscribeFinalResults(fixture.nc, waiters)
	if err != nil {
		t.Fatalf("subscribe final results: %v", err)
	}
	defer func() {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
	}()

	opID := "op-final-waiter-1"
	ch := waiters.register(opID)
	defer waiters.unregister(opID)

	var spec ProjectSpec
	spec.APIVersion = projectAPIVersion
	spec.Kind = projectKind
	spec.Name = "waiter-check"
	spec.Runtime = "go_1.26"
	spec.Environments = map[string]EnvConfig{
		"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
	}
	spec.NetworkPolicies = NetworkPolicies{
		Ingress: networkPolicyInternal,
		Egress:  networkPolicyInternal,
	}
	msg := ProjectOpMsg{
		OpID:      opID,
		Kind:      OpDeploy,
		ProjectID: "project-final-waiter-1",
		Spec:      normalizeProjectSpec(spec),
		DeployEnv: "dev",
		FromEnv:   "",
		ToEnv:     "",
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStageDeploy,
			Environment: "dev",
			FromEnv:     "",
			ToEnv:       "",
		},
		Err: "",
		At:  time.Now().UTC(),
	}
	res := finalizeWorkerResult(msg, "deployer", newWorkerResultMsg("deployment complete"))
	res.Artifacts = []string{"deploy/dev/rendered.yaml"}

	publishErr := publishWorkerResult(context.Background(), fixture.js, subjectDeploymentDone, res)
	if publishErr != nil {
		t.Fatalf("publish final result with jetstream: %v", publishErr)
	}

	select {
	case got := <-ch:
		if got.OpID != opID {
			t.Fatalf("expected delivered op id %q, got %q", opID, got.OpID)
		}
		if got.Worker != "deployer" {
			t.Fatalf("expected delivered worker %q, got %q", "deployer", got.Worker)
		}
		if got.Kind != OpDeploy {
			t.Fatalf("expected delivered kind %q, got %q", OpDeploy, got.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for final waiter delivery")
	}
}
