package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
)

////////////////////////////////////////////////////////////////////////////////
// Artifact store (disk)
////////////////////////////////////////////////////////////////////////////////

type ArtifactStore interface {
	ProjectDir(projectID string) string
	EnsureProjectDir(projectID string) (string, error)
	WriteFile(projectID, relPath string, data []byte) (string, error) // returns relative path
	ListFiles(projectID string) ([]string, error)                     // returns relative paths
	ReadFile(projectID, relPath string) ([]byte, error)
	RemoveProject(projectID string) error
}

type FSArtifacts struct {
	root string
}

func NewFSArtifacts(root string) *FSArtifacts {
	return &FSArtifacts{root: root}
}

func (a *FSArtifacts) ProjectDir(projectID string) string {
	return filepath.Join(a.root, projectID)
}

func (a *FSArtifacts) EnsureProjectDir(projectID string) (string, error) {
	dir := a.ProjectDir(projectID)
	if err := os.MkdirAll(dir, dirModePrivateRead); err != nil {
		return "", err
	}
	return dir, nil
}

func (a *FSArtifacts) WriteFile(projectID, relPath string, data []byte) (string, error) {
	dir, err := a.EnsureProjectDir(projectID)
	if err != nil {
		return "", err
	}
	relPath = filepath.Clean(relPath)
	if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return "", errors.New("invalid relPath")
	}
	full := filepath.Join(dir, relPath)
	mkdirErr := os.MkdirAll(filepath.Dir(full), dirModePrivateRead)
	if mkdirErr != nil {
		return "", mkdirErr
	}
	writeErr := os.WriteFile(full, data, fileModePrivate)
	if writeErr != nil {
		return "", writeErr
	}
	return filepath.ToSlash(relPath), nil
}

func (a *FSArtifacts) ListFiles(projectID string) ([]string, error) {
	root := a.ProjectDir(projectID)
	var files []string
	_, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, _ error) error {
		if d == nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (a *FSArtifacts) ReadFile(projectID, relPath string) ([]byte, error) {
	dir := a.ProjectDir(projectID)
	relPath = filepath.Clean(relPath)
	if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return nil, errors.New("invalid relPath")
	}
	full, err := securejoin.SecureJoin(dir, relPath)
	if err != nil {
		return nil, errors.New("invalid relPath")
	}
	// #nosec G703 -- full path is constrained by relPath guards and securejoin above.
	return os.ReadFile(full)
}

func (a *FSArtifacts) RemoveProject(projectID string) error {
	return os.RemoveAll(a.ProjectDir(projectID))
}
