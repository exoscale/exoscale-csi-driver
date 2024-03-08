package cluster

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"

	exov3 "github.com/exoscale/egoscale/v3"
)

func (c *Cluster) getLatestSKSVersion() (string, error) {
	versions, err := c.Ego.ListSKSClusterVersions(c.context)
	if err != nil {
		return "", fmt.Errorf("error retrieving SKS versions: %w", err)
	}

	if len(versions.SKSClusterVersions) == 0 {
		return "", fmt.Errorf("no SKS version returned by the API")
	}

	return versions.SKSClusterVersions[0], nil
}

func (c *Cluster) getInstanceType(family, size string) (*exov3.InstanceType, error) {
	instanceTypes, err := c.Ego.ListInstanceTypes(c.context)
	if err != nil {
		return nil, err
	}

	for _, instanceType := range instanceTypes.InstanceTypes {
		if instanceType.Family == exov3.InstanceTypeFamilyStandard && instanceType.Size == exov3.InstanceTypeSizeMedium {
			return c.Ego.GetInstanceType(c.context, instanceType.ID)
		}
	}

	return nil, fmt.Errorf("unable to find instance type %s.%s", family, size)
}

func (c *Cluster) provisionSKSCluster(zone string) error {
	// do nothing if cluster exists
	_, err := c.getCluster()
	if err == nil {
		return nil
	}

	latestSKSVersion, err := c.getLatestSKSVersion()
	if err != nil {
		return err
	}

	// intance type must be at least standard.medium for block storage volume attachment to work
	instanceType, err := c.getInstanceType("standard", "medium")
	if err != nil {
		return err
	}

	newClusterID, err := c.awaitID(c.Ego.CreateSKSCluster(c.context, exov3.CreateSKSClusterRequest{
		Addons: []string{
			"exoscale-cloud-controller",
		},
		Cni:         "calico",
		Description: "This cluster was created to test the exoscale CSI driver in SKS.",
		Name:        c.Name,
		Level:       exov3.CreateSKSClusterRequestLevelPro,
		Version:     latestSKSVersion,
	}))
	if err != nil {
		return err
	}

	c.ID = newClusterID

	if err = c.awaitSuccess(c.Ego.CreateSKSNodepool(c.context, newClusterID, exov3.CreateSKSNodepoolRequest{
		Name:           c.Name + "-nodepool",
		DiskSize:       int64(20),
		Size:           int64(2),
		InstancePrefix: "pool",
		InstanceType:   instanceType,
	})); err != nil {
		// this can error even when the nodepool is successfully created
		// it's probably a bug, so we're not returning the error
		slog.Warn("error creating nodepool", "err", err)
	}
	slog.Info("successfully created cluster", "clusterID", c.ID)

	return nil
}

func exitApplication(msg string, err error) {
	slog.Error(msg, "err", err)

	flag.Usage()

	os.Exit(1)
}

func ConfigureCluster(createCluster bool, name, zone string) (*Cluster, error) {
	v3Client, ctx, ctxCancel, err := createV3ClientAndContext()
	if err != nil {
		return nil, fmt.Errorf("error creating egoscale v3 client: %w", err)
	}

	cluster := &Cluster{
		Ego:           v3Client,
		Name:          name,
		context:       ctx,
		cancelContext: ctxCancel,
	}

	if createCluster {
		err = cluster.provisionSKSCluster(zone)
		if err != nil {
			return nil, fmt.Errorf("error creating SKS cluster: %w", err)
		}
	}

	id, err := cluster.getClusterID()
	if err != nil {
		return nil, fmt.Errorf("error getting cluster ID: %w", err)
	}

	cluster.ID = id
	cluster.APIKeyName = apiKeyPrefix + cluster.Name
	cluster.APIRoleName = cluster.APIKeyName + "-role"

	k, err := cluster.getK8sClients()
	if err != nil {
		return nil, fmt.Errorf("error initializing k8s clients: %w", err)
	}

	cluster.K8s = k

	return cluster, nil
}

func Setup() error {
	if err := flags.ValidateFlags(); err != nil {
		exitApplication("invalid flags", err)

		return err
	}

	var err error
	testCluster, err = ConfigureCluster(*flags.CreateCluster, *flags.ClusterName, *flags.Zone)
	if err != nil {
		return err
	}

	calicoControllerName := "calico-kube-controllers"
	if err := testCluster.awaitDeploymentReadiness(calicoControllerName); err != nil {
		slog.Warn("error while awaiting", "deployment", calicoControllerName, "error", err)
	}

	calicoNodeName := "calico-node"
	if err := testCluster.awaitDaemonSetReadiness(calicoNodeName); err != nil {
		slog.Warn("error while awaiting", "DaemonSet", calicoNodeName, "error", err)
	}

	if !*flags.DontApplyCSI {
		if err := testCluster.applyCSI(); err != nil {
			return fmt.Errorf("error applying CSI: %w", err)
		}
	}

	return nil
}
