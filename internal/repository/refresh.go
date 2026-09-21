package repository

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// RefreshingCredentialStore refreshes Google OAuth access tokens on Resolve
// when they are close to expiry. List and Get never see plaintext and never
// refresh.
type RefreshingCredentialStore struct {
	CredentialRepository
	client               *http.Client
	tokenURL             string
	platformClientID     string
	platformClientSecret string
	locks                sync.Map
}

// NewRefreshingCredentialStore wraps an inner store. A nil inner is returned
// unchanged so composition can skip wrapping when credential storage is off.
// platformClientID and platformClientSecret are used when the credential
// itself has no Google Cloud client (the n8n Cloud / KilasFlow platform app).
func NewRefreshingCredentialStore(inner CredentialRepository, client *http.Client, tokenURL, platformClientID, platformClientSecret string) CredentialRepository {
	if inner == nil {
		return nil
	}
	return &RefreshingCredentialStore{
		CredentialRepository: inner, client: client, tokenURL: tokenURL,
		platformClientID: platformClientID, platformClientSecret: platformClientSecret,
	}
}

// Resolve decrypts the payload and, for Google OAuth types, refreshes the
// access token when it is within two minutes of expiry.
func (store *RefreshingCredentialStore) Resolve(ctx context.Context, tenant TenantScope, credentialID string) (credentials.Record, map[string]string, error) {
	record, fields, err := store.CredentialRepository.Resolve(ctx, tenant, credentialID)
	if err != nil {
		return credentials.Record{}, nil, err
	}
	if !credentials.IsGoogleOAuth(record.Type) || !credentials.TokenNeedsRefresh(fields, time.Now().UTC()) {
		return record, fields, nil
	}

	lock := store.lockFor(credentialID)
	lock.Lock()
	defer lock.Unlock()

	record, fields, err = store.CredentialRepository.Resolve(ctx, tenant, credentialID)
	if err != nil {
		return credentials.Record{}, nil, err
	}
	if !credentials.TokenNeedsRefresh(fields, time.Now().UTC()) {
		return record, fields, nil
	}

	clientID := strings.TrimSpace(fields["clientId"])
	clientSecret := strings.TrimSpace(fields["clientSecret"])
	if clientID == "" {
		clientID = strings.TrimSpace(store.platformClientID)
	}
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(store.platformClientSecret)
	}
	refreshToken := strings.TrimSpace(fields["refresh_token"])
	if clientID == "" || clientSecret == "" || refreshToken == "" {
		return credentials.Record{}, nil, fmt.Errorf("google credential %q cannot refresh: connect it again", record.Name)
	}
	if store.client == nil {
		return credentials.Record{}, nil, fmt.Errorf("oauth http client is not configured")
	}
	token, err := credentials.RefreshGoogleToken(store.client, store.tokenURL, clientID, clientSecret, refreshToken)
	if err != nil {
		return credentials.Record{}, nil, fmt.Errorf("refresh google token: %w", err)
	}
	merged := credentials.MergeGoogleToken(fields, token)
	updated, err := store.CredentialRepository.Update(ctx, tenant, credentialID, credentials.Record{
		Name: record.Name, Type: record.Type, Fields: merged, AllowedDomains: record.AllowedDomains,
	})
	if err != nil {
		return credentials.Record{}, nil, fmt.Errorf("store refreshed google token: %w", err)
	}
	return updated, merged, nil
}

func (store *RefreshingCredentialStore) lockFor(credentialID string) *sync.Mutex {
	actual, _ := store.locks.LoadOrStore(credentialID, &sync.Mutex{})
	return actual.(*sync.Mutex)
}
