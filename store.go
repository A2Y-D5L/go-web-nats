package platform

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

////////////////////////////////////////////////////////////////////////////////
// Persistence: Projects + Ops in KV (JSON)
////////////////////////////////////////////////////////////////////////////////

type Store struct {
	kvProjects jetstream.KeyValue
	kvOps      jetstream.KeyValue
	opEvents   *opEventHub
}

type projectOpsIndex struct {
	IDs       []string  `json:"ids"`
	UpdatedAt time.Time `json:"updated_at"`
}

type projectReleaseIndex struct {
	IDs       []string  `json:"ids"`
	UpdatedAt time.Time `json:"updated_at"`
}

type projectReleaseCurrent struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
}

type projectOpsListQuery struct {
	Limit  int
	Cursor string
	Before string
}

type projectOpsListPage struct {
	Ops        []Operation
	NextCursor string
}

type projectReleaseListQuery struct {
	Limit  int
	Cursor string
}

type projectReleaseListPage struct {
	Items      []ReleaseRecord
	NextCursor string
}

type projectOpsBackfillReport struct {
	ScannedOps              int
	RebuiltProjects         int
	UpdatedProjects         int
	AddedIndexEntries       int
	SkippedMalformedOps     int
	SkippedMissingProjectID int
	SkippedMissingOpID      int
	SkippedReadErrors       int
	Truncated               bool
}

func newStore(ctx context.Context, js jetstream.JetStream) (*Store, error) {
	var projectsKV jetstream.KeyValue
	err := ensureKVBucket(ctx, js, kvBucketProjects, defaultKVProjectHistory, &projectsKV)
	if err != nil {
		return nil, err
	}
	var opsKV jetstream.KeyValue
	err = ensureKVBucket(ctx, js, kvBucketOps, defaultKVOpsHistory, &opsKV)
	if err != nil {
		return nil, err
	}
	return &Store{
		kvProjects: projectsKV,
		kvOps:      opsKV,
		opEvents:   nil,
	}, nil
}

func (s *Store) setOpEvents(hub *opEventHub) {
	if s == nil {
		return
	}
	s.opEvents = hub
}

func (s *Store) PutProject(ctx context.Context, p Project) error {
	p.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.kvProjects.Put(ctx, kvProjectKeyPrefix+p.ID, b)
	return err
}

func (s *Store) GetProject(ctx context.Context, projectID string) (Project, error) {
	e, err := s.kvProjects.Get(ctx, kvProjectKeyPrefix+projectID)
	if err != nil {
		return Project{}, err
	}
	var p Project
	unmarshalErr := json.Unmarshal(e.Value(), &p)
	if unmarshalErr != nil {
		return Project{}, unmarshalErr
	}
	return p, nil
}

func (s *Store) DeleteProject(ctx context.Context, projectID string) error {
	return s.kvProjects.Delete(ctx, kvProjectKeyPrefix+projectID)
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	keys, err := s.kvProjects.Keys(ctx)
	if err != nil {
		// Some KV backends can return ErrNoKeys if empty; treat as empty.
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return []Project{}, nil
		}
		return nil, err
	}
	var out []Project
	for _, k := range keys {
		if !strings.HasPrefix(k, kvProjectKeyPrefix) {
			continue
		}
		projectID := strings.TrimPrefix(k, kvProjectKeyPrefix)
		project, getErr := s.GetProject(ctx, projectID)
		if getErr != nil {
			// best-effort listing
			continue
		}
		out = append(out, project)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) PutOp(ctx context.Context, op Operation) error {
	b, err := json.Marshal(op)
	if err != nil {
		return err
	}
	_, err = s.kvOps.Put(ctx, kvOpKeyPrefix+op.ID, b)
	if err != nil {
		return err
	}
	return s.recordProjectOp(ctx, op.ProjectID, op.ID)
}

func (s *Store) PutRelease(ctx context.Context, release ReleaseRecord) (ReleaseRecord, error) {
	release = normalizeReleaseRecord(release)
	if strings.TrimSpace(release.ProjectID) == "" {
		return ReleaseRecord{}, errors.New("project_id required")
	}
	if strings.TrimSpace(release.Environment) == "" {
		return ReleaseRecord{}, errors.New("environment required")
	}
	if strings.TrimSpace(release.OpID) == "" {
		return ReleaseRecord{}, errors.New("op_id required")
	}
	if strings.TrimSpace(release.ID) == "" {
		release.ID = newID()
	}
	if release.CreatedAt.IsZero() {
		release.CreatedAt = time.Now().UTC()
	} else {
		release.CreatedAt = release.CreatedAt.UTC()
	}

	body, err := json.Marshal(release)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if _, err = s.kvOps.Put(ctx, kvReleaseKeyPrefix+release.ID, body); err != nil {
		return ReleaseRecord{}, err
	}
	if err = s.recordProjectRelease(ctx, release.ProjectID, release.Environment, release.ID); err != nil {
		return ReleaseRecord{}, err
	}
	if err = s.writeProjectReleaseCurrent(ctx, release.ProjectID, release.Environment, release.ID); err != nil {
		return ReleaseRecord{}, err
	}
	return release, nil
}

func (s *Store) GetRelease(ctx context.Context, releaseID string) (ReleaseRecord, error) {
	entry, err := s.kvOps.Get(ctx, kvReleaseKeyPrefix+strings.TrimSpace(releaseID))
	if err != nil {
		return ReleaseRecord{}, err
	}
	var release ReleaseRecord
	if unmarshalErr := json.Unmarshal(entry.Value(), &release); unmarshalErr != nil {
		return ReleaseRecord{}, unmarshalErr
	}
	return normalizeReleaseRecord(release), nil
}

func (s *Store) GetOp(ctx context.Context, opID string) (Operation, error) {
	e, err := s.kvOps.Get(ctx, kvOpKeyPrefix+opID)
	if err != nil {
		return Operation{}, err
	}
	var op Operation
	unmarshalErr := json.Unmarshal(e.Value(), &op)
	if unmarshalErr != nil {
		return Operation{}, unmarshalErr
	}
	return op, nil
}

func (s *Store) listProjectOps(
	ctx context.Context,
	projectID string,
	query projectOpsListQuery,
) (projectOpsListPage, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return projectOpsListPage{Ops: []Operation{}, NextCursor: ""}, nil
	}

	limit := normalizeProjectOpsLimit(query.Limit)
	index, err := s.readProjectOpsIndex(ctx, projectID)
	if err != nil {
		return projectOpsListPage{}, err
	}
	if len(index.IDs) == 0 {
		return projectOpsListPage{Ops: []Operation{}, NextCursor: ""}, nil
	}

	start, beforeAt := resolveProjectOpsWindow(index.IDs, query)
	if start >= len(index.IDs) {
		return projectOpsListPage{Ops: []Operation{}, NextCursor: ""}, nil
	}

	return s.collectProjectOpsPage(
		ctx,
		projectID,
		index.IDs[start:],
		limit,
		beforeAt,
	)
}

func (s *Store) listProjectReleases(
	ctx context.Context,
	projectID string,
	environment string,
	query projectReleaseListQuery,
) (projectReleaseListPage, error) {
	projectID = strings.TrimSpace(projectID)
	environment = normalizeEnvironmentName(environment)
	if projectID == "" || environment == "" {
		return projectReleaseListPage{Items: []ReleaseRecord{}, NextCursor: ""}, nil
	}

	limit := normalizeProjectReleaseLimit(query.Limit)
	index, err := s.readProjectReleaseIndex(ctx, projectID, environment)
	if err != nil {
		return projectReleaseListPage{}, err
	}
	if len(index.IDs) == 0 {
		return projectReleaseListPage{Items: []ReleaseRecord{}, NextCursor: ""}, nil
	}

	start := indexStartFromCursor(index.IDs, query.Cursor)
	if start >= len(index.IDs) {
		return projectReleaseListPage{Items: []ReleaseRecord{}, NextCursor: ""}, nil
	}

	items := make([]ReleaseRecord, 0, limit+1)
	for _, releaseID := range index.IDs[start:] {
		release, getErr := s.GetRelease(ctx, releaseID)
		if getErr != nil {
			if errors.Is(getErr, jetstream.ErrKeyNotFound) {
				continue
			}
			return projectReleaseListPage{}, getErr
		}
		if strings.TrimSpace(release.ProjectID) != projectID {
			continue
		}
		if normalizeEnvironmentName(release.Environment) != environment {
			continue
		}
		items = append(items, release)
		if len(items) > limit {
			break
		}
	}

	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor = strings.TrimSpace(items[len(items)-1].ID)
	}
	return projectReleaseListPage{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

func (s *Store) backfillProjectOpsIndex(
	ctx context.Context,
	maxScan int,
) (projectOpsBackfillReport, error) {
	var report projectOpsBackfillReport

	opsByProject, err := s.scanProjectOpsForBackfill(
		ctx,
		normalizeProjectOpsBackfillScanLimit(maxScan),
		&report,
	)
	if err != nil {
		return report, err
	}
	if len(opsByProject) == 0 {
		return report, nil
	}
	if rebuildErr := s.rebuildProjectOpsIndexes(ctx, opsByProject, &report); rebuildErr != nil {
		return report, rebuildErr
	}
	return report, nil
}

func (s *Store) scanProjectOpsForBackfill(
	ctx context.Context,
	scanLimit int,
	report *projectOpsBackfillReport,
) (map[string][]Operation, error) {
	keys, err := s.kvOps.Keys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return map[string][]Operation{}, nil
		}
		return nil, err
	}
	sort.Strings(keys)

	opsByProject := make(map[string][]Operation)
	for _, key := range keys {
		if !strings.HasPrefix(key, kvOpKeyPrefix) {
			continue
		}
		if report.ScannedOps >= scanLimit {
			report.Truncated = true
			break
		}
		report.ScannedOps++

		op, ok := s.readBackfillOp(ctx, key, report)
		if !ok {
			continue
		}
		projectID := strings.TrimSpace(op.ProjectID)
		opsByProject[projectID] = append(opsByProject[projectID], op)
	}
	return opsByProject, nil
}

func (s *Store) readBackfillOp(
	ctx context.Context,
	key string,
	report *projectOpsBackfillReport,
) (Operation, bool) {
	entry, err := s.kvOps.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			return Operation{}, false
		}
		report.SkippedReadErrors++
		return Operation{}, false
	}

	var op Operation
	if unmarshalErr := json.Unmarshal(entry.Value(), &op); unmarshalErr != nil {
		report.SkippedMalformedOps++
		return Operation{}, false
	}
	if strings.TrimSpace(op.ProjectID) == "" {
		report.SkippedMissingProjectID++
		return Operation{}, false
	}
	if strings.TrimSpace(op.ID) == "" {
		report.SkippedMissingOpID++
		return Operation{}, false
	}
	return op, true
}

func (s *Store) rebuildProjectOpsIndexes(
	ctx context.Context,
	opsByProject map[string][]Operation,
	report *projectOpsBackfillReport,
) error {
	projectIDs := make([]string, 0, len(opsByProject))
	for projectID := range opsByProject {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Strings(projectIDs)

	for _, projectID := range projectIDs {
		report.RebuiltProjects++
		discoveredIDs := orderedUniqueProjectOpIDs(opsByProject[projectID])

		index, err := s.readProjectOpsIndex(ctx, projectID)
		if err != nil {
			return err
		}
		mergedIDs := mergeProjectOpsIDs(discoveredIDs, index.IDs, projectOpsHistoryCap)
		if slices.Equal(index.IDs, mergedIDs) {
			continue
		}

		report.AddedIndexEntries += countBackfillAddedIDs(index.IDs, mergedIDs)

		index.IDs = mergedIDs
		index.UpdatedAt = time.Now().UTC()
		if writeErr := s.writeProjectOpsIndex(ctx, projectID, index); writeErr != nil {
			return writeErr
		}
		report.UpdatedProjects++
	}
	return nil
}

func orderedUniqueProjectOpIDs(ops []Operation) []string {
	if len(ops) == 0 {
		return []string{}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		leftRequested := ops[i].Requested.UTC()
		rightRequested := ops[j].Requested.UTC()
		if !leftRequested.Equal(rightRequested) {
			return leftRequested.After(rightRequested)
		}
		return strings.TrimSpace(ops[i].ID) > strings.TrimSpace(ops[j].ID)
	})

	ids := make([]string, 0, len(ops))
	seen := make(map[string]struct{}, len(ops))
	for _, op := range ops {
		opID := strings.TrimSpace(op.ID)
		if opID == "" {
			continue
		}
		if _, exists := seen[opID]; exists {
			continue
		}
		seen[opID] = struct{}{}
		ids = append(ids, opID)
	}
	return ids
}

func countBackfillAddedIDs(existing []string, merged []string) int {
	if len(merged) == 0 {
		return 0
	}
	existingSet := make(map[string]struct{}, len(existing))
	for _, opID := range existing {
		existingSet[strings.TrimSpace(opID)] = struct{}{}
	}
	added := 0
	for _, opID := range merged {
		if _, exists := existingSet[strings.TrimSpace(opID)]; exists {
			continue
		}
		added++
	}
	return added
}

func (s *Store) collectProjectOpsPage(
	ctx context.Context,
	projectID string,
	opIDs []string,
	limit int,
	beforeAt time.Time,
) (projectOpsListPage, error) {
	items := make([]Operation, 0, limit+1)
	for _, opID := range opIDs {
		op, getErr := s.GetOp(ctx, opID)
		if getErr != nil {
			if errors.Is(getErr, jetstream.ErrKeyNotFound) {
				continue
			}
			return projectOpsListPage{}, getErr
		}
		if strings.TrimSpace(op.ProjectID) != projectID {
			continue
		}
		if !beforeAt.IsZero() && !op.Requested.Before(beforeAt) {
			continue
		}
		items = append(items, op)
		if len(items) > limit {
			break
		}
	}

	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		nextCursor = strings.TrimSpace(items[len(items)-1].ID)
	}
	return projectOpsListPage{
		Ops:        items,
		NextCursor: nextCursor,
	}, nil
}

func resolveProjectOpsWindow(ids []string, query projectOpsListQuery) (int, time.Time) {
	beforeRaw := strings.TrimSpace(query.Before)
	beforeCursor := ""
	beforeAt := time.Time{}
	if beforeRaw != "" {
		if parsed, ok := parseProjectOpsBeforeTime(beforeRaw); ok {
			beforeAt = parsed
		} else {
			beforeCursor = beforeRaw
		}
	}

	cursor := strings.TrimSpace(query.Cursor)
	start := 0
	if cursor != "" {
		start = indexStartFromCursor(ids, cursor)
	} else if beforeCursor != "" {
		start = indexStartFromCursor(ids, beforeCursor)
	}
	return start, beforeAt
}

func (s *Store) latestOpEventSequence(opID string) int64 {
	if s == nil || s.opEvents == nil {
		return 0
	}
	return s.opEvents.latestSequence(opID)
}

func normalizeProjectOpsLimit(limit int) int {
	switch {
	case limit <= 0:
		return projectOpsDefaultLimit
	case limit > projectOpsMaxLimit:
		return projectOpsMaxLimit
	default:
		return limit
	}
}

func normalizeProjectReleaseLimit(limit int) int {
	switch {
	case limit <= 0:
		return projectReleaseDefaultLimit
	case limit > projectReleaseMaxLimit:
		return projectReleaseMaxLimit
	default:
		return limit
	}
}

func normalizeProjectOpsBackfillScanLimit(limit int) int {
	switch {
	case limit <= 0:
		return projectOpsBackfillDefaultScanLimit
	case limit > projectOpsBackfillMaxScanLimit:
		return projectOpsBackfillMaxScanLimit
	default:
		return limit
	}
}

func mergeProjectOpsIDs(primary, secondary []string, limit int) []string {
	if limit <= 0 {
		return []string{}
	}
	capHint := min(limit, len(primary)+len(secondary))
	if capHint < 0 {
		capHint = limit
	}
	merged := make([]string, 0, capHint)
	seen := make(map[string]struct{}, capHint)

	appendFrom := func(ids []string) bool {
		for _, raw := range ids {
			opID := strings.TrimSpace(raw)
			if opID == "" {
				continue
			}
			if _, exists := seen[opID]; exists {
				continue
			}
			seen[opID] = struct{}{}
			merged = append(merged, opID)
			if len(merged) >= limit {
				return true
			}
		}
		return false
	}

	if appendFrom(primary) {
		return merged
	}
	_ = appendFrom(secondary)
	return merged
}

func normalizeReleaseRecord(in ReleaseRecord) ReleaseRecord {
	release := in
	release.ID = strings.TrimSpace(release.ID)
	release.ProjectID = strings.TrimSpace(release.ProjectID)
	release.OpID = strings.TrimSpace(release.OpID)
	release.Environment = normalizeEnvironmentName(release.Environment)
	release.FromEnv = normalizeEnvironmentName(release.FromEnv)
	release.ToEnv = normalizeEnvironmentName(release.ToEnv)
	release.Image = strings.TrimSpace(release.Image)
	release.RenderedPath = strings.Trim(strings.TrimSpace(release.RenderedPath), "/")
	if release.Environment == "" && release.ToEnv != "" {
		release.Environment = release.ToEnv
	}
	if release.ToEnv == "" {
		release.ToEnv = release.Environment
	}
	if release.DeliveryStage == "" {
		switch release.OpKind {
		case OpRelease:
			release.DeliveryStage = DeliveryStageRelease
		case OpPromote:
			release.DeliveryStage = DeliveryStagePromote
		case OpDeploy, OpCreate, OpUpdate, OpDelete, OpCI:
			release.DeliveryStage = DeliveryStageDeploy
		default:
			release.DeliveryStage = DeliveryStageDeploy
		}
	}
	if release.OpKind == "" {
		switch release.DeliveryStage {
		case DeliveryStageRelease:
			release.OpKind = OpRelease
		case DeliveryStagePromote:
			release.OpKind = OpPromote
		case DeliveryStageDeploy:
			release.OpKind = OpDeploy
		default:
			release.OpKind = OpDeploy
		}
	}
	return release
}

func parseProjectOpsBeforeTime(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return ts.UTC(), true
	}
	if ts, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return ts.UTC(), true
	}
	return time.Time{}, false
}

func indexStartFromCursor(ids []string, cursor string) int {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0
	}
	for idx, id := range ids {
		if id == cursor {
			return idx + 1
		}
	}
	return len(ids)
}

func (s *Store) recordProjectOp(ctx context.Context, projectID, opID string) error {
	projectID = strings.TrimSpace(projectID)
	opID = strings.TrimSpace(opID)
	if projectID == "" || opID == "" {
		return nil
	}

	index, err := s.readProjectOpsIndex(ctx, projectID)
	if err != nil {
		return err
	}

	if slices.Contains(index.IDs, opID) {
		index.UpdatedAt = time.Now().UTC()
		return s.writeProjectOpsIndex(ctx, projectID, index)
	}

	index.IDs = append([]string{opID}, index.IDs...)
	if len(index.IDs) > projectOpsHistoryCap {
		index.IDs = append([]string(nil), index.IDs[:projectOpsHistoryCap]...)
	}
	index.UpdatedAt = time.Now().UTC()
	return s.writeProjectOpsIndex(ctx, projectID, index)
}

func (s *Store) recordProjectRelease(ctx context.Context, projectID, environment, releaseID string) error {
	projectID = strings.TrimSpace(projectID)
	environment = normalizeEnvironmentName(environment)
	releaseID = strings.TrimSpace(releaseID)
	if projectID == "" || environment == "" || releaseID == "" {
		return nil
	}

	index, err := s.readProjectReleaseIndex(ctx, projectID, environment)
	if err != nil {
		return err
	}
	if slices.Contains(index.IDs, releaseID) {
		index.UpdatedAt = time.Now().UTC()
		return s.writeProjectReleaseIndex(ctx, projectID, environment, index)
	}

	index.IDs = append([]string{releaseID}, index.IDs...)
	if len(index.IDs) > projectReleaseHistoryCap {
		index.IDs = append([]string(nil), index.IDs[:projectReleaseHistoryCap]...)
	}
	index.UpdatedAt = time.Now().UTC()
	return s.writeProjectReleaseIndex(ctx, projectID, environment, index)
}

func (s *Store) readProjectOpsIndex(ctx context.Context, projectID string) (projectOpsIndex, error) {
	entry, err := s.kvOps.Get(ctx, projectOpsIndexKey(projectID))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return projectOpsIndex{
				IDs:       []string{},
				UpdatedAt: time.Time{},
			}, nil
		}
		return projectOpsIndex{}, err
	}
	var index projectOpsIndex
	if unmarshalErr := json.Unmarshal(entry.Value(), &index); unmarshalErr != nil {
		return projectOpsIndex{}, unmarshalErr
	}
	if index.IDs == nil {
		index.IDs = []string{}
	}
	return index, nil
}

func (s *Store) writeProjectOpsIndex(ctx context.Context, projectID string, index projectOpsIndex) error {
	body, err := json.Marshal(index)
	if err != nil {
		return err
	}
	_, err = s.kvOps.Put(ctx, projectOpsIndexKey(projectID), body)
	return err
}

func (s *Store) readProjectReleaseIndex(
	ctx context.Context,
	projectID string,
	environment string,
) (projectReleaseIndex, error) {
	entry, err := s.kvOps.Get(ctx, projectReleaseIndexKey(projectID, environment))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return projectReleaseIndex{
				IDs:       []string{},
				UpdatedAt: time.Time{},
			}, nil
		}
		return projectReleaseIndex{}, err
	}
	var index projectReleaseIndex
	if unmarshalErr := json.Unmarshal(entry.Value(), &index); unmarshalErr != nil {
		return projectReleaseIndex{}, unmarshalErr
	}
	if index.IDs == nil {
		index.IDs = []string{}
	}
	return index, nil
}

func (s *Store) writeProjectReleaseIndex(
	ctx context.Context,
	projectID string,
	environment string,
	index projectReleaseIndex,
) error {
	body, err := json.Marshal(index)
	if err != nil {
		return err
	}
	_, err = s.kvOps.Put(ctx, projectReleaseIndexKey(projectID, environment), body)
	return err
}

func (s *Store) readProjectReleaseCurrent(
	ctx context.Context,
	projectID string,
	environment string,
) (projectReleaseCurrent, bool, error) {
	entry, err := s.kvOps.Get(ctx, projectReleaseCurrentKey(projectID, environment))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return projectReleaseCurrent{}, false, nil
		}
		return projectReleaseCurrent{}, false, err
	}
	var current projectReleaseCurrent
	if unmarshalErr := json.Unmarshal(entry.Value(), &current); unmarshalErr != nil {
		return projectReleaseCurrent{}, false, unmarshalErr
	}
	current.ID = strings.TrimSpace(current.ID)
	if current.ID == "" {
		return projectReleaseCurrent{}, false, nil
	}
	return current, true, nil
}

func (s *Store) writeProjectReleaseCurrent(
	ctx context.Context,
	projectID string,
	environment string,
	releaseID string,
) error {
	current := projectReleaseCurrent{
		ID:        strings.TrimSpace(releaseID),
		UpdatedAt: time.Now().UTC(),
	}
	body, err := json.Marshal(current)
	if err != nil {
		return err
	}
	_, err = s.kvOps.Put(ctx, projectReleaseCurrentKey(projectID, environment), body)
	return err
}

func (s *Store) getProjectCurrentRelease(
	ctx context.Context,
	projectID string,
	environment string,
) (ReleaseRecord, bool, error) {
	current, ok, err := s.readProjectReleaseCurrent(ctx, projectID, environment)
	if err != nil || !ok {
		return ReleaseRecord{}, false, err
	}
	release, err := s.GetRelease(ctx, current.ID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return ReleaseRecord{}, false, nil
		}
		return ReleaseRecord{}, false, err
	}
	projectID = strings.TrimSpace(projectID)
	environment = normalizeEnvironmentName(environment)
	if strings.TrimSpace(release.ProjectID) != projectID {
		return ReleaseRecord{}, false, nil
	}
	if normalizeEnvironmentName(release.Environment) != environment {
		return ReleaseRecord{}, false, nil
	}
	return release, true, nil
}

func projectOpsIndexKey(projectID string) string {
	return kvProjectOpsIndexKeyPrefix + strings.TrimSpace(projectID)
}

func projectReleaseIndexKey(projectID string, environment string) string {
	projectID = strings.TrimSpace(projectID)
	environment = normalizeEnvironmentName(environment)
	return kvProjectReleaseIndexKeyPrefix + projectID + "/" + environment
}

func projectReleaseCurrentKey(projectID string, environment string) string {
	projectID = strings.TrimSpace(projectID)
	environment = normalizeEnvironmentName(environment)
	return kvProjectReleaseCurrentKeyPrefix + projectID + "/" + environment
}
