package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/util"

	exov2 "github.com/exoscale/egoscale/v2"
	exov2api "github.com/exoscale/egoscale/v2/api"
)

const (
	apiKeyPrefix = "csi-integ-test-key-"
	csiNamespace = "kube-system"
)

var (
	manifestDir = util.GetRepoRootDir() + "deployment/"

	controllerRBACManifest      = "controller-rbac.yaml"
	controllerManifest          = "controller.yaml"
	crdsManifest                = "crds.yaml"
	csiDriverManifest           = "csi-driver.yaml"
	nodeDriverRBACManifest      = "node-driver-rbac.yaml"
	nodeDriverManifest          = "node-driver.yaml"
	storageClassManifest        = "storage-class.yaml"
	volumeSnapshotClassManifest = "volume-snapshot-class.yaml"

	allManifests = []string{
		crdsManifest,
		controllerRBACManifest,
		controllerManifest,
		csiDriverManifest,
		nodeDriverRBACManifest,
		nodeDriverManifest,
		storageClassManifest,
		volumeSnapshotClassManifest,
	}
)

func ptr[T any](v T) *T {
	return &v
}

func getCredentialsFromEnv() (string, string, error) {
	errmsg := "environment variable %q is required"

	apiKey := ""
	apiSecret := ""

	value, ok := os.LookupEnv(util.APIKeyEnvVar)
	if !ok {
		return "", "", fmt.Errorf(errmsg, util.APIKeyEnvVar)
	}
	apiKey = value

	value, ok = os.LookupEnv(util.APISecretEnvVar)
	if !ok {
		return "", "", fmt.Errorf(errmsg, util.APIKeyEnvVar)
	}
	apiSecret = value

	return apiKey, apiSecret, nil
}

func (c *Cluster) getClusterID() (string, error) {
	if err := flags.ValidateFlags(); err != nil {
		return "", err
	}

	cluster, err := c.Ego.FindSKSCluster(c.exoV2Context, *flags.Zone, *flags.ClusterName)
	if err != nil {
		return "", err
	}

	return *cluster.ID, nil
}

func (c *Cluster) getCluster() (*exov2.SKSCluster, error) {
	id, err := c.getClusterID()
	if err != nil {
		return nil, err
	}

	return c.Ego.GetSKSCluster(c.exoV2Context, *flags.Zone, id)
}

func (c *Cluster) getKubeconfig() ([]byte, error) {
	cluster, err := c.Ego.GetSKSCluster(c.exoV2Context, *flags.Zone, c.ID)
	if err != nil {
		return nil, err
	}

	base64KubeConfig, err := c.Ego.GetSKSClusterKubeconfig(c.exoV2Context, *flags.Zone, cluster, "admin", []string{"system:masters"}, 1*time.Hour)
	if err != nil {
		return nil, err
	}

	return base64.StdEncoding.DecodeString(base64KubeConfig)
}

func (c *Cluster) getK8sClients() (*k8s.K8S, error) {
	kubeconfig, err := c.getKubeconfig()
	if err != nil {
		return nil, err
	}

	return k8s.CreateClients(kubeconfig)
}

func (c *Cluster) deleteAPIKeyAndRole() error {
	keys, err := c.Ego.ListAPIKeys(c.exoV2Context, *flags.Zone)
	if err != nil {
		return fmt.Errorf("error listing api keys: %w", err)
	}

	for _, key := range keys {
		if *key.Name != c.APIKeyName {
			continue
		}

		if err := c.Ego.DeleteAPIKey(c.exoV2Context, *flags.Zone, key); err != nil {
			return fmt.Errorf("error deleting existing IAM key: %w", err)
		}
	}

	roles, err := c.Ego.ListIAMRoles(c.exoV2Context, *flags.Zone)
	if err != nil {
		return fmt.Errorf("error listing iam roles: %w", err)
	}

	for _, role := range roles {
		if *role.Name != c.APIRoleName {
			continue
		}

		if err := c.Ego.DeleteIAMRole(c.exoV2Context, *flags.Zone, role); err != nil {
			slog.Error("deleting IAM role", "name", *role.Name, "err", err)
		}
	}

	return nil
}

func (c *Cluster) createImagePullSecret() {
	value, ok := os.LookupEnv(util.RegistryUsernameEnvVar)
	if !ok {
		slog.Warn("no registry username set")

		return
	}
	username := value

	value, ok = os.LookupEnv(util.APISecretEnvVar)
	if !ok {
		slog.Warn("no registry password set")

		return
	}
	password := value

	authToken := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	// Encode credentials in base64
	// dockerConfig := fmt.Sprintf(`{"auths":{"ghcr.io":{"username":"%s","password":"%s"}}}`, username, password)
	dockerConfig := fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":"%s"}}}`, authToken)

	// Create the secret object
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "csi-image-pull-secret",
			Namespace: "kube-system",
		},
		Data: map[string][]byte{
			".dockerconfigjson": []byte(dockerConfig),
		},
		Type: "kubernetes.io/dockerconfigjson",
	}

	secretsClient := c.K8s.ClientSet.CoreV1().Secrets("kube-system")

	_, err := secretsClient.Get(c.exoV2Context, secret.Name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			_, err := secretsClient.Create(c.exoV2Context, secret, metav1.CreateOptions{})
			if err != nil {
				slog.Error("failed to create registry secret", "err", err)
				return
			}

			slog.Info("image pull secret created successfully")
			return
		}

		slog.Error("error checking for registry secret", "err", err)
		return
	}

	_, err = secretsClient.Update(c.exoV2Context, secret, metav1.UpdateOptions{})
	if err != nil {
		slog.Error("failed to update registry secret", "err", err)
		return
	}

	slog.Info("image pull secret updated successfully")
}

func (c *Cluster) applyCSI() error {
	// TODO (sauterp) reenable or remove once it is clear which registy should be used for the test images
	// c.createImagePullSecret()

	if *flags.CreateCSISecret {
		if err := c.deleteAPIKeyAndRole(); err != nil {
			return err
		}

		allow := exov2.IAMPolicyService{
			Type: ptr("allow"),
		}

		role, err := c.Ego.CreateIAMRole(c.exoV2Context, *flags.Zone, &exov2.IAMRole{
			Name:        ptr(c.APIRoleName),
			Description: ptr("role for the CSI test cluster " + c.Name),
			Editable:    ptr(false),
			Policy: &exov2.IAMPolicy{
				DefaultServiceStrategy: "deny",
				Services: map[string]exov2.IAMPolicyService{
					"compute": allow,
				},
			},
		})
		if err != nil {
			return fmt.Errorf("error creating IAM role: %w", err)
		}

		apikey := &exov2.APIKey{
			Name:   ptr(c.APIKeyName),
			RoleID: role.ID,
		}

		key, secret, err := c.Ego.CreateAPIKey(c.exoV2Context, *flags.Zone, apikey)
		if err != nil {
			return err
		}

		err = c.K8s.ApplySecret(c.exoV2Context, *key.Key, secret)
		if err != nil {
			return fmt.Errorf("error creating secret: %w", err)
		}
	}

	if *flags.Image != "" {
		slog.Info("testing CSI image", "path", *flags.Image)
	}

	for _, manifestPath := range allManifests {
		err := c.K8s.ApplyManifest(c.exoV2Context, manifestDir+manifestPath)
		if err != nil {
			return fmt.Errorf("error applying CSI manifest: %q %w", manifestPath, err)
		}
	}

	// TODO(sauterp) this shouldn't be necessary anymore once the CSI addon is available.
	// the CSI controller needs to restart to pick up the new secrets
	c.restartCSIController()

	return nil
}

func (c *Cluster) restartCSIController() {
	podsClient := c.K8s.ClientSet.CoreV1().Pods(csiNamespace)
	pods, err := podsClient.List(c.exoV2Context, metav1.ListOptions{})
	if err != nil {
		slog.Warn("failed to list pods", "err", err)
	}

	for _, pod := range pods.Items {
		if strings.HasPrefix(pod.Name, "exoscale-csi-controller") {
			err := podsClient.Delete(c.exoV2Context, pod.Name, metav1.DeleteOptions{})
			if err != nil {
				slog.Warn("failed to delete pod", "name", pod.Name, "err", err)
			}
		}
	}
}

func createV2ClientAndContext() (*exov2.Client, context.Context, context.CancelFunc, error) {
	apiKey, apiSecret, err := getCredentialsFromEnv()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error getting credentials from environment: %w", err)
	}

	timeout := 5 * time.Minute
	v2Client, err := exov2.NewClient(apiKey, apiSecret,
		exov2.ClientOptWithTimeout(timeout),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error setting up egoscale client: %w", err)
	}

	ctx := context.Background()
	ctx, ctxCancel := context.WithTimeout(ctx, timeout)
	ctx = exov2api.WithEndpoint(ctx, exov2api.NewReqEndpoint("", *flags.Zone))

	return v2Client, ctx, ctxCancel, nil
}
