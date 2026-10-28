package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	"github.com/fsnotify/fsnotify"
	"k8s.io/klog/v2"
)

const (
	// credentialsFileAPIKey and credentialsFileAPISecret are the file names in the credentials
	// directory: the keys of the exoscale-credentials Secret when it is mounted as a volume.
	credentialsFileAPIKey    = "EXOSCALE_API_KEY"
	credentialsFileAPISecret = "EXOSCALE_API_SECRET"

	// credentialsReloadDelay groups the burst of events a Secret update produces into one reload.
	credentialsReloadDelay = time.Second
	// credentialsResyncPeriod re-reads the credentials directory in case a file event was missed,
	// and retries credentials the API rejected.
	credentialsResyncPeriod = time.Minute
	// credentialsValidateTimeout bounds the API call that checks new credentials before using them.
	credentialsValidateTimeout = 30 * time.Second
)

// refreshableClient holds the Exoscale API client used by the driver.
// When the credentials come from a directory (a mounted Secret), it watches that directory
// and rebuilds the client when they change, so rotated credentials are picked up without
// restarting the pod.
type refreshableClient struct {
	mu     sync.RWMutex
	client *v3.Client
	creds  credentials.Value

	dir      string
	endpoint v3.Endpoint
	opts     []v3.ClientOpt
}

// newRefreshableClient builds the client for the zone endpoint.
// dir is the credentials directory to reload from, empty if the credentials are static.
func newRefreshableClient(creds credentials.Value, dir string, endpoint v3.Endpoint, opts []v3.ClientOpt) (*refreshableClient, error) {
	c := &refreshableClient{
		creds:    creds,
		dir:      dir,
		endpoint: endpoint,
		opts:     opts,
	}

	client, err := c.newClient(creds)
	if err != nil {
		return nil, err
	}
	c.client = client

	return c, nil
}

func (c *refreshableClient) newClient(creds credentials.Value) (*v3.Client, error) {
	client, err := v3.NewClient(credentials.NewStaticCredentials(creds.APIKey, creds.APISecret), c.opts...)
	if err != nil {
		return nil, err
	}

	return client.WithEndpoint(c.endpoint), nil
}

// Get returns the current client. Call it once per request instead of keeping the result,
// so that a reload takes effect on the next request.
func (c *refreshableClient) Get() *v3.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.client
}

// readCredentialsDir reads the API key and secret from the files of a mounted Secret.
func readCredentialsDir(dir string) (credentials.Value, error) {
	// The kubelet publishes a new Secret version by swapping the "..data" symlink.
	// Resolve it once so that the key and the secret come from the same version.
	base := dir
	if resolved, err := filepath.EvalSymlinks(filepath.Join(dir, "..data")); err == nil {
		base = resolved
	}

	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			return "", err
		}
		v := strings.TrimSpace(string(b))
		if v == "" {
			return "", fmt.Errorf("%s is empty", filepath.Join(dir, name))
		}
		return v, nil
	}

	apiKey, err := read(credentialsFileAPIKey)
	if err != nil {
		return credentials.Value{}, fmt.Errorf("read credentials: %w", err)
	}
	apiSecret, err := read(credentialsFileAPISecret)
	if err != nil {
		return credentials.Value{}, fmt.Errorf("read credentials: %w", err)
	}

	return credentials.Value{APIKey: apiKey, APISecret: apiSecret}, nil
}

// validateCredentials returns an error if the Exoscale API rejects the client credentials.
// Only an authentication failure is reported, so that valid credentials missing the IAM
// permission for this call, or a transient API error, don't block a rotation.
func validateCredentials(ctx context.Context, client *v3.Client) error {
	_, err := client.ListQuotas(ctx)
	if err == nil {
		return nil
	}

	var apiErr *v3.APIError
	if errors.Is(err, v3.ErrUnauthorized) ||
		(errors.Is(err, v3.ErrForbidden) && errors.As(err, &apiErr) &&
			strings.Contains(strings.ToLower(apiErr.Message), "invalid key")) {
		return err
	}

	klog.Warningf("unable to validate Exoscale API credentials, using them anyway: %v", err)

	return nil
}

// reload rebuilds the client if the credentials in the directory changed.
// Incomplete, unreadable or rejected credentials are ignored and the current client is kept;
// rejected credentials are tried again on the next reload.
func (c *refreshableClient) reload(ctx context.Context) {
	creds, err := readCredentialsDir(c.dir)
	if err != nil {
		klog.Warningf("keeping current Exoscale API credentials: %v", err)
		return
	}

	c.mu.RLock()
	unchanged := creds == c.creds
	c.mu.RUnlock()
	if unchanged {
		return
	}

	client, err := c.newClient(creds)
	if err != nil {
		klog.Errorf("keeping current Exoscale API credentials: new client: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, credentialsValidateTimeout)
	defer cancel()
	if err := validateCredentials(ctx, client); err != nil {
		klog.Errorf("keeping current Exoscale API credentials: new key %s rejected: %v", creds.APIKey, err)
		return
	}

	c.mu.Lock()
	c.client = client
	c.creds = creds
	c.mu.Unlock()

	klog.Infof("Exoscale API credentials reloaded from %s, now using key %s", c.dir, creds.APIKey)
}

// startWatch registers a watch on the credentials directory, then reloads the credentials
// in the background whenever it changes, and every resync period, until ctx is done.
func (c *refreshableClient) startWatch(ctx context.Context, resync time.Duration) {
	// A Secret volume is updated by swapping a "..data" symlink, so the events are on
	// that symlink, not on the credential files: every event in the directory triggers a reload.
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		if err = watcher.Add(c.dir); err != nil {
			_ = watcher.Close()
		}
	}
	if err != nil {
		klog.Errorf("unable to watch credentials directory %s, re-reading it every %s instead: %v", c.dir, resync, err)
		watcher = nil
	}

	go c.watch(ctx, watcher, resync)
}

func (c *refreshableClient) watch(ctx context.Context, watcher *fsnotify.Watcher, resync time.Duration) {
	var (
		events <-chan fsnotify.Event
		errs   <-chan error
	)
	if watcher != nil {
		defer watcher.Close()
		events, errs = watcher.Events, watcher.Errors
	}

	ticker := time.NewTicker(resync)
	defer ticker.Stop()

	var reloadC <-chan time.Time
	for {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			// Don't push back a pending reload: a steady stream of events must not postpone it.
			if reloadC == nil {
				reloadC = time.After(credentialsReloadDelay)
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			klog.Errorf("error while watching credentials directory %s: %v", c.dir, err)
		case <-reloadC:
			reloadC = nil
			c.reload(ctx)
		case <-ticker.C:
			c.reload(ctx)
		case <-ctx.Done():
			return
		}
	}
}
