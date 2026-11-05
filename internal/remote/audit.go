package remote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/valkey-io/valkey-go"

	"jira-mcp/internal/audit"
	"jira-mcp/internal/mcp"
)

// The per-identity index and its event keys share a cluster hash tag. Each
// event has its own TTL, so expiry does not depend on subsequent activity.
var appendAudit = valkey.NewLuaScript(`
redis.call('SET', KEYS[2], ARGV[1], 'PX', ARGV[2])
redis.call('ZADD', KEYS[1], ARGV[3], KEYS[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[4])
redis.call('ZREMRANGEBYRANK', KEYS[1], 0, -10001)
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)

var listAudit = valkey.NewLuaScript(`
local keys = redis.call('ZREVRANGEBYSCORE', KEYS[1], '+inf', ARGV[1], 'LIMIT', 0, ARGV[2])
local events = {}
for _, key in ipairs(keys) do
  local value = redis.call('GET', key)
  if value then table.insert(events, value) end
end
return events
`)

type ValkeyAuditStore struct {
	store     *ValkeyStore
	retention time.Duration
}

func NewValkeyAuditStore(store *ValkeyStore, retention time.Duration) (*ValkeyAuditStore, error) {
	if store == nil || retention < time.Second || retention > audit.MaxRetention {
		return nil, errors.New("invalid Valkey audit store or retention")
	}
	return &ValkeyAuditStore{store: store, retention: retention}, nil
}

func auditKey(identity string) (string, error) {
	if identity == "" {
		return "", errors.New("audit identity is required")
	}
	hash := sha256.Sum256([]byte(identity))
	return "jira-mcp:audit:{" + hex.EncodeToString(hash[:]) + "}", nil
}

func (s *ValkeyAuditStore) Append(ctx context.Context, identity string, event mcp.ToolEvent) error {
	key, err := auditKey(identity)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	now := time.Now().UTC()
	ttl := strconv.FormatInt(s.retention.Milliseconds(), 10)
	return appendAudit.Exec(ctx, s.store.client, []string{key + ":index", key + ":event:" + hex.EncodeToString(id[:])}, []string{
		string(encoded), ttl, strconv.FormatInt(now.UnixMilli(), 10), strconv.FormatInt(now.Add(-s.retention).UnixMilli(), 10),
	}).Error()
}

func (s *ValkeyAuditStore) List(ctx context.Context, identity string, limit int) ([]mcp.ToolEvent, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("audit limit must be between 1 and 200")
	}
	key, err := auditKey(identity)
	if err != nil {
		return nil, err
	}
	values, err := listAudit.Exec(ctx, s.store.client, []string{key + ":index"}, []string{strconv.FormatInt(time.Now().Add(-s.retention).UnixMilli(), 10), strconv.Itoa(limit)}).AsStrSlice()
	if err != nil {
		return nil, err
	}
	events := make([]mcp.ToolEvent, 0, len(values))
	for _, value := range values {
		var event mcp.ToolEvent
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}
