package audit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"jira-mcp/internal/mcp"
)

type FileStore struct {
	dir       string
	retention time.Duration
	clock     func() time.Time
	mu        sync.Mutex
	nextSweep map[string]time.Time
}

func NewFileStore(dir string, retention time.Duration) (*FileStore, error) {
	if dir == "" || retention < time.Second || retention > MaxRetention {
		return nil, errors.New("invalid audit directory or retention")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("audit directory must be private")
	}
	return &FileStore{dir: dir, retention: retention, clock: time.Now, nextSweep: make(map[string]time.Time)}, nil
}

func identityHash(identity string) string {
	hash := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(hash[:])
}

func (s *FileStore) identityDir(identity string) (string, error) {
	if identity == "" {
		return "", errors.New("audit identity is required")
	}
	dir := filepath.Join(s.dir, identityHash(identity))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *FileStore) Append(ctx context.Context, identity string, event mcp.ToolEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.identityDir(identity)
	if err != nil {
		return err
	}
	if err := s.sweep(ctx, dir); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	name := fmt.Sprintf("event-%020d-%s.json", s.clock().UTC().UnixNano(), hex.EncodeToString(random[:]))
	path := filepath.Join(dir, name)
	file, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()
	writeErr := json.NewEncoder(file).Encode(event)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	return os.Rename(tempPath, path)
}

func (s *FileStore) sweep(ctx context.Context, dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if now.Before(s.nextSweep[dir]) {
		return nil
	}
	if _, err := s.list(ctx, dir, 0); err != nil {
		return err
	}
	interval := min(time.Hour, s.retention/2)
	s.nextSweep[dir] = now.Add(interval)
	return nil
}

func (s *FileStore) List(ctx context.Context, identity string, limit int) ([]mcp.ToolEvent, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("audit limit must be between 1 and 200")
	}
	dir, err := s.identityDir(identity)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, dir, limit)
}

func (s *FileStore) list(ctx context.Context, dir string, limit int) ([]mcp.ToolEvent, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	result := make([]mcp.ToolEvent, 0)
	cutoff := s.clock().Add(-s.retention).UnixNano()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.Type() != 0 || len(name) != len("event-")+20+1+32+len(".json") || !strings.HasPrefix(name, "event-") || !strings.HasSuffix(name, ".json") || name[26] != '-' {
			continue
		}
		stamp, err := strconv.ParseInt(name[6:26], 10, 64)
		if err != nil {
			continue
		}
		if _, err := hex.DecodeString(name[27:59]); err != nil {
			continue
		}
		path := filepath.Join(dir, name)
		if stamp <= cutoff {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			continue
		}
		if len(result) >= limit {
			continue
		}
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var event mcp.ToolEvent
		err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&event)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		result = append(result, event)
	}
	return result, nil
}
