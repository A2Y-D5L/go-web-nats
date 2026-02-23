//nolint:testpackage // Store release tests exercise unexported release index/current helpers.
package platform

import (
	"context"
	"testing"
	"time"
)

func TestStore_ReleasePutGetListAndCurrentPointer(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	ctx := context.Background()
	projectID := "project-store-releases"

	first, second := seedStoreReleases(ctx, t, fixture.store, projectID)
	assertReleaseRoundTrip(ctx, t, fixture.store, first)
	assertCurrentReleasePointer(ctx, t, fixture.store, projectID, second.ID)
	assertStagingReleasePagination(
		ctx,
		t,
		fixture.store,
		projectID,
		first.ID,
		second.ID,
	)
}

func seedStoreReleases(
	ctx context.Context,
	t *testing.T,
	store *Store,
	projectID string,
) (ReleaseRecord, ReleaseRecord) {
	t.Helper()

	first, err := store.PutRelease(ctx, ReleaseRecord{
		ID:                    "",
		ProjectID:             projectID,
		Environment:           "staging",
		OpID:                  "op-release-1",
		OpKind:                OpPromote,
		DeliveryStage:         DeliveryStagePromote,
		FromEnv:               "dev",
		ToEnv:                 "staging",
		Image:                 "local/store-releases:1111",
		RenderedPath:          "promotions/dev-to-staging/rendered.yaml",
		ConfigPath:            "promotions/dev-to-staging/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC().Add(-3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put first release: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected generated release id for first record")
	}

	second, err := store.PutRelease(ctx, ReleaseRecord{
		ID:                    "",
		ProjectID:             projectID,
		Environment:           "staging",
		OpID:                  "op-release-2",
		OpKind:                OpRelease,
		DeliveryStage:         DeliveryStageRelease,
		FromEnv:               "staging",
		ToEnv:                 "prod",
		Image:                 "local/store-releases:2222",
		RenderedPath:          "releases/staging-to-prod/rendered.yaml",
		ConfigPath:            "releases/staging-to-prod/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put second release: %v", err)
	}

	_, err = store.PutRelease(ctx, ReleaseRecord{
		ID:                    "",
		ProjectID:             projectID,
		Environment:           "dev",
		OpID:                  "op-release-3",
		OpKind:                OpDeploy,
		DeliveryStage:         DeliveryStageDeploy,
		FromEnv:               "",
		ToEnv:                 "dev",
		Image:                 "local/store-releases:3333",
		RenderedPath:          "deploy/dev/rendered.yaml",
		ConfigPath:            "deploy/dev/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put third release in dev: %v", err)
	}
	return first, second
}

func assertReleaseRoundTrip(
	ctx context.Context,
	t *testing.T,
	store *Store,
	first ReleaseRecord,
) {
	t.Helper()
	gotFirst, err := store.GetRelease(ctx, first.ID)
	if err != nil {
		t.Fatalf("get first release: %v", err)
	}
	if gotFirst.OpID != first.OpID {
		t.Fatalf("expected first op_id %q, got %q", first.OpID, gotFirst.OpID)
	}
	if gotFirst.Environment != "staging" {
		t.Fatalf("expected first environment staging, got %q", gotFirst.Environment)
	}
	if gotFirst.ConfigPath != "promotions/dev-to-staging/deployment.yaml" {
		t.Fatalf("expected first config_path to round-trip, got %q", gotFirst.ConfigPath)
	}
	if gotFirst.RollbackSafe == nil || !*gotFirst.RollbackSafe {
		t.Fatalf("expected first rollback_safe=true, got %#v", gotFirst.RollbackSafe)
	}
}

func assertCurrentReleasePointer(
	ctx context.Context,
	t *testing.T,
	store *Store,
	projectID string,
	wantCurrentID string,
) {
	t.Helper()
	current, ok, err := store.getProjectCurrentRelease(ctx, projectID, "staging")
	if err != nil {
		t.Fatalf("get current staging release: %v", err)
	}
	if !ok {
		t.Fatal("expected current staging release pointer")
	}
	if current.ID != wantCurrentID {
		t.Fatalf("expected current release id %q, got %q", wantCurrentID, current.ID)
	}
}

func assertStagingReleasePagination(
	ctx context.Context,
	t *testing.T,
	store *Store,
	projectID string,
	wantOlderID string,
	wantLatestID string,
) {
	t.Helper()
	pageOne, err := store.listProjectReleases(
		ctx,
		projectID,
		"staging",
		projectReleaseListQuery{
			Limit:  1,
			Cursor: "",
		},
	)
	if err != nil {
		t.Fatalf("list staging releases page one: %v", err)
	}
	if len(pageOne.Items) != 1 {
		t.Fatalf("expected 1 staging item on first page, got %d", len(pageOne.Items))
	}
	if pageOne.Items[0].ID != wantLatestID {
		t.Fatalf("expected latest staging release %q, got %q", wantLatestID, pageOne.Items[0].ID)
	}
	if pageOne.NextCursor != wantLatestID {
		t.Fatalf("expected next cursor %q, got %q", wantLatestID, pageOne.NextCursor)
	}

	pageTwo, err := store.listProjectReleases(
		ctx,
		projectID,
		"staging",
		projectReleaseListQuery{
			Limit:  1,
			Cursor: pageOne.NextCursor,
		},
	)
	if err != nil {
		t.Fatalf("list staging releases page two: %v", err)
	}
	if len(pageTwo.Items) != 1 {
		t.Fatalf("expected 1 staging item on second page, got %d", len(pageTwo.Items))
	}
	if pageTwo.Items[0].ID != wantOlderID {
		t.Fatalf("expected older staging release %q, got %q", wantOlderID, pageTwo.Items[0].ID)
	}
	if pageTwo.NextCursor != "" {
		t.Fatalf("expected empty final next_cursor, got %q", pageTwo.NextCursor)
	}
}
