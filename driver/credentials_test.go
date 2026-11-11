package driver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	"github.com/stretchr/testify/require"
)

// writeSecretVolume writes credentials the way the kubelet updates a Secret volume:
// files in a versioned directory, published by atomically swapping the "..data" symlink.
func writeSecretVolume(t *testing.T, dir, version, apiKey, apiSecret string) {
	t.Helper()

	versionDir := filepath.Join(dir, "..version_"+version)
	require.NoError(t, os.Mkdir(versionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(versionDir, credentials.SecretDirAPIKeyFile), []byte(apiKey), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(versionDir, credentials.SecretDirAPISecretFile), []byte(apiSecret), 0o600))

	tmpLink := filepath.Join(dir, "..data_tmp")
	require.NoError(t, os.Symlink(filepath.Base(versionDir), tmpLink))
	require.NoError(t, os.Rename(tmpLink, filepath.Join(dir, "..data")))

	for _, name := range []string{credentials.SecretDirAPIKeyFile, credentials.SecretDirAPISecretFile} {
		link := filepath.Join(dir, name)
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			require.NoError(t, os.Symlink(filepath.Join("..data", name), link))
		}
	}
}

// fakeAPI is a fake Exoscale API that records the API key of each request, and rejects the
// keys not in valid the way the real API rejects an unknown or deleted key.
type fakeAPI struct {
	mu    sync.Mutex
	keys  []string
	valid map[string]bool
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var key string
	if _, rest, ok := strings.Cut(r.Header.Get("Authorization"), "credential="); ok {
		key, _, _ = strings.Cut(rest, ",")
	}

	f.mu.Lock()
	f.keys = append(f.keys, key)
	valid := f.valid[key]
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if !valid {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Invalid key or request signature"}`))
		return
	}
	_, _ = w.Write([]byte(`{"quotas":[]}`))
}

func (f *fakeAPI) setValid(keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.valid = map[string]bool{}
	for _, k := range keys {
		f.valid[k] = true
	}
}

// received returns the keys of the requests received since the last call.
func (f *fakeAPI) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	keys := f.keys
	f.keys = nil
	return keys
}

func newTestDriverClient(t *testing.T, config *DriverConfig) (*v3.Client, *credentials.Credentials, *fakeAPI) {
	t.Helper()

	api := &fakeAPI{}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	api.setValid("EXOold")

	opts := []v3.ClientOpt{v3.ClientOptWithEndpoint(v3.Endpoint(srv.URL))}
	creds, err := newCredentials(t.Context(), config, opts)
	require.NoError(t, err)
	client, err := v3.NewClient(creds, opts...)
	require.NoError(t, err)

	return client, creds, api
}

func TestNewCredentialsWithoutDir(t *testing.T) {
	creds := credentials.NewStaticCredentials("EXOold", "old-secret")

	got, err := newCredentials(t.Context(), &DriverConfig{Credentials: creds}, nil)
	require.NoError(t, err)
	require.Same(t, creds, got)
}

// TestNewCredentialsRotation checks the rotation of the key in the mounted Secret, when the
// old key is deleted before the driver re-reads the Secret: the request rejected for the old
// key is retried with the new one.
func TestNewCredentialsRotation(t *testing.T) {
	dir := t.TempDir()
	writeSecretVolume(t, dir, "1", "EXOold", "old-secret")
	client, _, api := newTestDriverClient(t, &DriverConfig{CredentialsDir: dir})
	require.Equal(t, []string{"EXOold"}, api.received(), "the initial key is validated")

	_, err := client.ListQuotas(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"EXOold"}, api.received())

	writeSecretVolume(t, dir, "2", "EXOnew", "new-secret")
	api.setValid("EXOnew")

	_, err = client.ListQuotas(t.Context())
	require.NoError(t, err)
	// Rejected request, validation of the new key, retried request.
	require.Equal(t, []string{"EXOold", "EXOnew", "EXOnew"}, api.received())

	_, err = client.ListQuotas(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"EXOnew"}, api.received())
}

// TestNewCredentialsRejectedKey checks that a key the API rejects isn't used.
func TestNewCredentialsRejectedKey(t *testing.T) {
	dir := t.TempDir()
	writeSecretVolume(t, dir, "1", "EXOold", "old-secret")
	client, creds, api := newTestDriverClient(t, &DriverConfig{CredentialsDir: dir})
	api.received()

	writeSecretVolume(t, dir, "2", "EXObad", "bad-secret")
	require.Error(t, creds.Reload(t.Context()))
	require.Equal(t, []string{"EXObad"}, api.received(), "the new key is validated")

	_, err := client.ListQuotas(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"EXOold"}, api.received())
}

// TestNewCredentialsIncompleteSecret checks that the driver doesn't start without a key.
func TestNewCredentialsIncompleteSecret(t *testing.T) {
	dir := t.TempDir()
	writeSecretVolume(t, dir, "1", "EXOold", "")

	_, err := newCredentials(t.Context(), &DriverConfig{CredentialsDir: dir}, nil)
	require.ErrorIs(t, err, credentials.ErrMissingIncomplete)
}
