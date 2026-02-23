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

	first, err := fixture.store.PutRelease(ctx, ReleaseRecord{
		ID:            "",
		ProjectID:     projectID,
		Environment:   "staging",
		OpID:          "op-release-1",
		OpKind:        OpPromote,
		DeliveryStage: DeliveryStagePromote,
		FromEnv:       "dev",
		ToEnv:         "staging",
		Image:         "local/store-releases:1111",
		RenderedPath:  "promotions/dev-to-staging/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put first release: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected generated release id for first record")
	}

	gotFirst, err := fixture.store.GetRelease(ctx, first.ID)
	if err != nil {
		t.Fatalf("get first release: %v", err)
	}
	if gotFirst.OpID != first.OpID {
		t.Fatalf("expected first op_id %q, got %q", first.OpID, gotFirst.OpID)
	}
	if gotFirst.Environment != "staging" {
		t.Fatalf("expected first environment staging, got %q", gotFirst.Environment)
	}

	second, err := fixture.store.PutRelease(ctx, ReleaseRecord{
		ID:            "",
		ProjectID:     projectID,
		Environment:   "staging",
		OpID:          "op-release-2",
		OpKind:        OpRelease,
		DeliveryStage: DeliveryStageRelease,
		FromEnv:       "staging",
		ToEnv:         "prod",
		Image:         "local/store-releases:2222",
		RenderedPath:  "releases/staging-to-prod/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put second release: %v", err)
	}

	_, err = fixture.store.PutRelease(ctx, ReleaseRecord{
		ID:            "",
		ProjectID:     projectID,
		Environment:   "dev",
		OpID:          "op-release-3",
		OpKind:        OpDeploy,
		DeliveryStage: DeliveryStageDeploy,
		FromEnv:       "",
		ToEnv:         "dev",
		Image:         "local/store-releases:3333",
		RenderedPath:  "deploy/dev/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put third release in dev: %v", err)
	}

	current, ok, err := fixture.store.getProjectCurrentRelease(ctx, projectID, "staging")
	if err != nil {
		t.Fatalf("get current staging release: %v", err)
	}
	if !ok {
		t.Fatal("expected current staging release pointer")
	}
	if current.ID != second.ID {
		t.Fatalf("expected current release id %q, got %q", second.ID, current.ID)
	}

	pageOne, err := fixture.store.listProjectReleases(
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
	if pageOne.Items[0].ID != second.ID {
		t.Fatalf("expected latest staging release %q, got %q", second.ID, pageOne.Items[0].ID)
	}
	if pageOne.NextCursor != second.ID {
		t.Fatalf("expected next cursor %q, got %q", second.ID, pageOne.NextCursor)
	}

	pageTwo, err := fixture.store.listProjectReleases(
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
	if pageTwo.Items[0].ID != first.ID {
		t.Fatalf("expected older staging release %q, got %q", first.ID, pageTwo.Items[0].ID)
	}
	if pageTwo.NextCursor != "" {
		t.Fatalf("expected empty final next_cursor, got %q", pageTwo.NextCursor)
	}
}
