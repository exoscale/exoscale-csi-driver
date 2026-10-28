package driver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	"github.com/stretchr/testify/require"
)

const testUserAgent = "exoscale-csi-driver/test"

// writeSecretVolume writes credentials the way the kubelet updates a Secret volume:
// files in a versioned directory, published by atomically swapping the "..data" symlink.
func writeSecretVolume(t *testing.T, dir, version, apiKey, apiSecret string) {
	t.Helper()

	versionDir := filepath.Join(dir, "..version_"+version)
	require.NoError(t, os.Mkdir(versionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(versionDir, credentialsFileAPIKey), []byte(apiKey), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(versionDir, credentialsFileAPISecret), []byte(apiSecret), 0o600))

	tmpLink := filepath.Join(dir, "..data_tmp")
	require.NoError(t, os.Symlink(filepath.Base(versionDir), tmpLink))
	require.NoError(t, os.Rename(tmpLink, filepath.Join(dir, "..data")))

	for _, name := range []string{credentialsFileAPIKey, credentialsFileAPISecret} {
		link := filepath.Join(dir, name)
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			require.NoError(t, os.Symlink(filepath.Join("..data", name), link))
		}
	}
}

func writeCredentialFiles(t *testing.T, dir, apiKey, apiSecret string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, credentialsFileAPIKey), []byte(apiKey), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, credentialsFileAPISecret), []byte(apiSecret), 0o600))
}

// fakeAPI is a fake Exoscale API that records the API key and user agent of each request,
// and rejects the keys in rejected the way the real API rejects an invalid key.
type fakeAPI struct {
	mu        sync.Mutex
	keys      []string
	userAgent string
	rejected  map[string]bool
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var key string
	if _, rest, ok := strings.Cut(r.Header.Get("Authorization"), "credential="); ok {
		key, _, _ = strings.Cut(rest, ",")
	}

	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.userAgent = r.Header.Get("User-Agent")
	rejected := f.rejected[key]
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if rejected {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Invalid key or request signature"}`))
		return
	}
	_, _ = w.Write([]byte(`{"quotas":[]}`))
}

func (f *fakeAPI) reject(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejected[key] = true
}

// signingKey makes a signed request with the current client and returns the key it was signed with.
func (f *fakeAPI) signingKey(c *refreshableClient) (string, error) {
	if _, err := c.Get().ListQuotas(context.Background()); err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keys[len(f.keys)-1], nil
}

func requireSigningKey(t *testing.T, f *fakeAPI, c *refreshableClient, want string) {
	t.Helper()

	key, err := f.signingKey(c)
	require.NoError(t, err)
	require.Equal(t, want, key)
}

func newTestRefreshableClient(t *testing.T, dir string) (*refreshableClient, *fakeAPI) {
	t.Helper()

	api := &fakeAPI{rejected: map[string]bool{}}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	creds, err := readCredentialsDir(dir)
	require.NoError(t, err)

	c, err := newRefreshableClient(creds, dir, v3.Endpoint(srv.URL), []v3.ClientOpt{v3.ClientOptWithUserAgent(testUserAgent)})
	require.NoError(t, err)

	return c, api
}

func TestReadCredentialsDir(t *testing.T) {
	t.Run("trims whitespace", func(t *testing.T) {
		dir := t.TempDir()
		writeCredentialFiles(t, dir, "EXOkey\n", " secret\n")

		creds, err := readCredentialsDir(dir)
		require.NoError(t, err)
		require.Equal(t, credentials.Value{APIKey: "EXOkey", APISecret: "secret"}, creds)
	})

	t.Run("reads a Secret volume", func(t *testing.T) {
		dir := t.TempDir()
		writeSecretVolume(t, dir, "1", "EXOkey", "secret")

		creds, err := readCredentialsDir(dir)
		require.NoError(t, err)
		require.Equal(t, credentials.Value{APIKey: "EXOkey", APISecret: "secret"}, creds)
	})

	t.Run("reads both files through ..data", func(t *testing.T) {
		// Stale top-level files next to a newer "..data" version: the version must win,
		// so the key and the secret can't come from two different versions.
		dir := t.TempDir()
		writeCredentialFiles(t, dir, "EXOstale", "stale-secret")
		writeSecretVolume(t, dir, "2", "EXOkey", "secret")

		creds, err := readCredentialsDir(dir)
		require.NoError(t, err)
		require.Equal(t, credentials.Value{APIKey: "EXOkey", APISecret: "secret"}, creds)
	})

	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, credentialsFileAPIKey), []byte("EXOkey"), 0o600))

		_, err := readCredentialsDir(dir)
		require.Error(t, err)
	})

	t.Run("empty value", func(t *testing.T) {
		dir := t.TempDir()
		writeCredentialFiles(t, dir, "EXOkey", "\n")

		_, err := readCredentialsDir(dir)
		require.ErrorContains(t, err, "is empty")
	})
}

func TestRefreshableClientReload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeCredentialFiles(t, dir, "EXOold", "old-secret")
	c, api := newTestRefreshableClient(t, dir)
	requireSigningKey(t, api, c, "EXOold")

	t.Run("unchanged credentials keep the client", func(t *testing.T) {
		before := c.Get()
		c.reload(ctx)
		require.Same(t, before, c.Get())
	})

	t.Run("incomplete credentials keep the client", func(t *testing.T) {
		writeCredentialFiles(t, dir, "EXOnew", "")
		c.reload(ctx)
		requireSigningKey(t, api, c, "EXOold")
	})

	t.Run("rejected credentials keep the client", func(t *testing.T) {
		api.reject("EXObad")
		writeCredentialFiles(t, dir, "EXObad", "bad-secret")
		c.reload(ctx)
		requireSigningKey(t, api, c, "EXOold")
	})

	t.Run("new credentials replace the client", func(t *testing.T) {
		writeCredentialFiles(t, dir, "EXOnew", "new-secret")
		c.reload(ctx)
		requireSigningKey(t, api, c, "EXOnew")
	})

	t.Run("reloaded client keeps the options", func(t *testing.T) {
		requireSigningKey(t, api, c, "EXOnew")
		api.mu.Lock()
		defer api.mu.Unlock()
		// egoscale appends its own version to the user agent.
		require.True(t, strings.HasPrefix(api.userAgent, testUserAgent+" "), api.userAgent)
	})
}

func TestRefreshableClientWatchesSecretVolume(t *testing.T) {
	dir := t.TempDir()
	writeSecretVolume(t, dir, "1", "EXOold", "old-secret")
	c, api := newTestRefreshableClient(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A resync period longer than the test: only file events can trigger the reload.
	c.startWatch(ctx, time.Hour)

	writeSecretVolume(t, dir, "2", "EXOnew", "new-secret")

	require.Eventually(t, func() bool {
		key, err := api.signingKey(c)
		return err == nil && key == "EXOnew"
	}, 10*time.Second, 50*time.Millisecond)
}

func TestRefreshableClientResync(t *testing.T) {
	dir := t.TempDir()
	writeCredentialFiles(t, dir, "EXOold", "old-secret")
	c, api := newTestRefreshableClient(t, dir)
	// A directory that can't be watched makes the resync period the only trigger.
	c.dir = filepath.Join(dir, "missing")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.startWatch(ctx, 50*time.Millisecond)

	require.NoError(t, os.Mkdir(c.dir, 0o755))
	writeCredentialFiles(t, c.dir, "EXOnew", "new-secret")

	require.Eventually(t, func() bool {
		key, err := api.signingKey(c)
		return err == nil && key == "EXOnew"
	}, 5*time.Second, 50*time.Millisecond)
}

func TestRefreshableClientConcurrentReload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeCredentialFiles(t, dir, "EXOa", "secret-a")
	c, api := newTestRefreshableClient(t, dir)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := c.Get().ListQuotas(ctx); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	for i := range 50 {
		if i%2 == 0 {
			writeCredentialFiles(t, dir, "EXOb", "secret-b")
		} else {
			writeCredentialFiles(t, dir, "EXOa", "secret-a")
		}
		c.reload(ctx)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	api.mu.Lock()
	defer api.mu.Unlock()
	for _, key := range api.keys {
		require.Contains(t, []string{"EXOa", "EXOb"}, key)
	}
}
