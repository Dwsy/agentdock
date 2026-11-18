package taskstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/filelock"
)

type cachedTaskFile struct {
	info os.FileInfo
	task Task
}

type Store struct {
	root      string
	sem       chan struct{}
	listCache map[string]*cachedTaskFile
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("task state root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve task state root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create task state root: %w", err)
	}
	if err := os.Chmod(abs, 0o700); err != nil {
		return nil, fmt.Errorf("secure task state root: %w", err)
	}
	// Channel semaphore lets callers bail out on ctx.Done() instead of piling up
	// behind an uncancellable sync.Mutex when lock contention spikes.
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	return &Store{root: abs, sem: sem, listCache: make(map[string]*cachedTaskFile)}, nil
}

func (s *Store) acquireStoreLock() (func(), error) {
	return s.acquireStoreLockContext(context.Background())
}

func (s *Store) acquireStoreLockContext(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.sem:
	}
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	releaseFileLock, err := filelock.Acquire(lockCtx, filepath.Join(s.root, ".store.lock"))
	cancel()
	if err != nil {
		s.sem <- struct{}{}
		return nil, fmt.Errorf("lock task state: %w", err)
	}
	return func() {
		releaseFileLock()
		s.sem <- struct{}{}
	}, nil
}

func (s *Store) Root() string { return s.root }
