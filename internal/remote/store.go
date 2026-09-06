package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"
)

var ErrNotFound = errors.New("remote state not found")

type Store interface {
	Put(ctx context.Context, key string, value any, ttl time.Duration) error
	Get(ctx context.Context, key string, value any) error
	Delete(ctx context.Context, key string) error
	Close()
}

type ValkeyStore struct {
	client valkey.Client
}

func NewValkeyStore(rawURL string) (*ValkeyStore, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid VALKEY_URL")
	}
	option := valkey.ClientOption{InitAddress: []string{u.Host}}
	if u.Scheme == "rediss" {
		option.TLSConfig = &tls.Config{ServerName: u.Hostname()}
	}
	if u.User != nil {
		username := u.User.Username()
		password, _ := u.User.Password()
		option.AuthCredentialsFn = func(valkey.AuthCredentialsContext) (valkey.AuthCredentials, error) {
			return valkey.AuthCredentials{Username: username, Password: password}, nil
		}
	}
	client, err := valkey.NewClient(option)
	if err != nil {
		return nil, err
	}
	return &ValkeyStore{client: client}, nil
}

func (s *ValkeyStore) Put(ctx context.Context, key string, value any, ttl time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	cmd := s.client.B().Set().Key(key).Value(string(b))
	if ttl > 0 {
		return s.client.Do(ctx, cmd.ExSeconds(int64(ttl/time.Second)).Build()).Error()
	}
	return s.client.Do(ctx, cmd.Build()).Error()
}

func (s *ValkeyStore) Get(ctx context.Context, key string, value any) error {
	encoded, err := s.client.Do(ctx, s.client.B().Get().Key(key).Build()).ToString()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "nil") {
			return ErrNotFound
		}
		return err
	}
	return json.Unmarshal([]byte(encoded), value)
}

func (s *ValkeyStore) Delete(ctx context.Context, key string) error {
	return s.client.Do(ctx, s.client.B().Del().Key(key).Build()).Error()
}

func (s *ValkeyStore) Close() { s.client.Close() }
