package platform

import (
	"context"
	"encoding/json"
	"errors"
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
	return err
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
