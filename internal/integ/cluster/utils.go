package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/util"

	exov3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
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

func (c *Cluster) getClusterID(ctx context.Context) (exov3.UUID, error) {
	if err := flags.ValidateFlags(); err != nil {
		return "", err
	}

	clusterList, err := c.Ego.ListSKSClusters(ctx)
	if err != nil {
		return "", err
	}

	cluster, err := clusterList.FindSKSCluster(*flags.ClusterName)
	if err != nil {
		return "", err
	}

	return cluster.ID, nil
}

func (c *Cluster) getCluster(ctx context.Context) (*exov3.SKSCluster, error) {
	id, err := c.getClusterID(ctx)
	if err != nil {
		return nil, err
	}

	return c.Ego.GetSKSCluster(ctx, exov3.UUID(id))
}

func (c *Cluster) getKubeconfig(ctx context.Context) ([]byte, error) {
	cluster, err := c.Ego.GetSKSCluster(ctx, c.ID)
	if err != nil {
		return nil, err
	}

	base64KubeConfig, err := c.Ego.GenerateSKSClusterKubeconfig(ctx, cluster.ID, exov3.SKSKubeconfigRequest{
		Groups: []string{"system:masters"},
		Ttl:    2 * 60 * 60,
		User:   "admin",
	})
	if err != nil {
		return nil, err
	}

	return base64.StdEncoding.DecodeString(base64KubeConfig.Kubeconfig)
}

func (c *Cluster) getK8sClients(ctx context.Context) (*k8s.K8S, error) {
	kubeconfig, err := c.getKubeconfig(ctx)
	if err != nil {
		return nil, err
	}

	return k8s.CreateClients(kubeconfig)
}

func (c *Cluster) deleteAPIKeyAndRole(ctx context.Context) error {
	keys, err := c.Ego.ListAPIKeys(ctx)
	if err != nil {
		return fmt.Errorf("error listing api keys: %w", err)
	}

	for _, key := range keys.APIKeys {
		if key.Name != c.APIKeyName {
			continue
		}

		op, err := c.Ego.DeleteAPIKey(ctx, key.Key)
		if err := c.awaitSuccess(ctx, op, err); err != nil {
			return fmt.Errorf("error deleting existing IAM key: %w", err)
		}
	}

	roles, err := c.Ego.ListIAMRoles(ctx)
	if err != nil {
		return fmt.Errorf("error listing iam roles: %w", err)
	}

	role, err := roles.FindIAMRole(c.APIRoleName)
	if err != nil {
		// no role to delete
		return nil
	}

	op, err := c.Ego.DeleteIAMRole(ctx, role.ID)
	if err := c.awaitSuccess(ctx, op, err); err != nil {
		slog.Error("deleting IAM role", "name", role.Name, "err", err)
	}

	return nil
}

func (c *Cluster) applyCSI(ctx context.Context) error {
	if *flags.CreateCSISecret {
		if err := c.deleteAPIKeyAndRole(ctx); err != nil {
			return err
		}

		onlyAllowBlockStorageOperations := exov2.IAMPolicyService{
			Type: ptr("rules"),
			Rules: []exov2.IAMPolicyServiceRule{
				exov2.IAMPolicyServiceRule{
					Action:     ptr("allow"),
					Expression: ptr("operation in ['list-zones', 'get-block-storage-volume', 'list-block-storage-volumes', 'create-block-storage-volume', 'delete-block-storage-volume', 'attach-block-storage-volume-to-instance', 'detach-block-storage-volume', 'update-block-storage-volume-labels', 'resize-block-storage-volume', 'get-block-storage-snapshot', 'list-block-storage-snapshots', 'create-block-storage-snapshot', 'delete-block-storage-snapshot']"),
				},
			},
		}

		op, err := c.Ego.CreateIAMRole(ctx, exov3.CreateIAMRoleRequest{
			Name:        c.APIRoleName,
			Description: "role for the CSI test cluster " + c.Name,
			Editable:    ptr(false),
			Policy: &exov3.IAMPolicy{
				DefaultServiceStrategy: "deny",
				Services: map[string]exov2.IAMPolicyService{
					"compute": onlyAllowBlockStorageOperations,
				},
			},
		})

		roleID, err := c.awaitID(ctx, op, err)
		if err != nil {
			return fmt.Errorf("error creating IAM role: %w", err)
		}

		// necessary due to issue [sc-91482]
		time.Sleep(3 * time.Second)

		apiKey, err := c.Ego.CreateAPIKey(ctx, exov3.CreateAPIKeyRequest{
			Name:   c.APIKeyName,
			RoleID: roleID,
		})
		if err != nil {
			return err
		}

		err = c.K8s.ApplySecret(ctx, apiKey.Key, apiKey.Secret)
		if err != nil {
			return fmt.Errorf("error creating secret: %w", err)
		}
	}

	if *flags.Image != "" {
		slog.Info("testing CSI image", "path", *flags.Image)
	}

	for _, manifestPath := range allManifests {
		err := c.K8s.ApplyManifest(ctx, manifestDir+manifestPath)
		if err != nil {
			return fmt.Errorf("error applying CSI manifest: %q %w", manifestPath, err)
		}
	}

	// the CSI controller needs to restart, in case it is already running, to pick up the new secrets
	c.restartCSIController(ctx)

	controllerName := "exoscale-csi-controller"
	if err := c.awaitDeploymentReadiness(ctx, controllerName); err != nil {
		slog.Warn("error while awaiting", "deployment", controllerName, "error", err)
	}

	nodeDriverName := "exoscale-csi-node"
	if err := c.awaitDaemonSetReadiness(ctx, nodeDriverName); err != nil {
		slog.Warn("error while awaiting", "DaemonSet", nodeDriverName, "error", err)
	}

	return nil
}

func retry(trial func() error, nRetries int, retryInterval time.Duration) error {
	if nRetries == 0 {
		nRetries = 10
	}

	if retryInterval == 0 {
		retryInterval = 10 * time.Second
	}

	for i := 0; i < nRetries-1; i++ {
		if trial() == nil {
			return nil
		}

		time.Sleep(retryInterval)
	}

	return trial()
}

func (c *Cluster) awaitDeploymentReadiness(ctx context.Context, deploymentName string) error {
	return retry(func() error {
		deployment, err := c.K8s.ClientSet.AppsV1().Deployments(csiNamespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// check if deployment is ready
		if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas {
			slog.Info("ready", "deployment", deploymentName)

			return nil
		}

		slog.Info("waiting for deployment to become ready", "deployment", deploymentName)
		return fmt.Errorf("waiting for deployment %q to become ready", deploymentName)
	}, 0, 0)
}

func (c *Cluster) awaitDaemonSetReadiness(ctx context.Context, name string) error {
	return retry(func() error {
		daemonSet, err := c.K8s.ClientSet.AppsV1().DaemonSets(csiNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// check if DaemonSet is ready
		if daemonSet.Status.DesiredNumberScheduled == daemonSet.Status.CurrentNumberScheduled {
			slog.Info("ready", "DaemonSet", name)

			return nil
		}

		slog.Info("waiting for DaemonSet to become ready", "DaemonSet", name)
		return fmt.Errorf("waiting for DaemonSet %q to become ready", name)
	}, 0, 0)
}

func (c *Cluster) restartCSIController(ctx context.Context) {
	deploymentName := "exoscale-csi-controller"
	podsClient := c.K8s.ClientSet.CoreV1().Pods(csiNamespace)
	pods, err := podsClient.List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Warn("failed to list pods", "err", err)
	}

	for _, pod := range pods.Items {
		if strings.HasPrefix(pod.Name, deploymentName) {
			err := podsClient.Delete(ctx, pod.Name, metav1.DeleteOptions{})
			if err != nil {
				slog.Warn("failed to delete pod", "name", pod.Name, "err", err)
			}
		}
	}
}

func createEgoscaleClient() (*exov3.Client, error) {
	v3Client, err := exov3.NewClient(credentials.NewEnvCredentials(), exov3.ClientOptWithEndpoint(exov3.CHGva2))
	if err != nil {
		return nil, fmt.Errorf("error setting up egoscale client: %w", err)
	}

	return v3Client, nil
}
