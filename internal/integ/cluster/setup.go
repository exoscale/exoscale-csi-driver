package cluster

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"

	exov2 "github.com/exoscale/egoscale/v2"
)

func (c *Cluster) getLatestSKSVersion() (string, error) {
	versions, err := c.Ego.ListSKSClusterVersions(c.exoV2Context)
	if err != nil {
		return "", fmt.Errorf("error retrieving SKS versions: %w", err)
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("no SKS version returned by the API")
	}

	return versions[0], nil
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
	instanceType, err := c.Ego.FindInstanceType(c.exoV2Context, zone, "standard.medium")
	if err != nil {
		return err
	}

	nodepool := exov2.SKSNodepool{
		Name:           ptr(c.Name + "-nodepool"),
		DiskSize:       ptr(int64(20)),
		Size:           ptr(int64(2)),
		InstancePrefix: ptr("pool"),
		InstanceTypeID: instanceType.ID,
	}

	sksCluster := &exov2.SKSCluster{
		AddOns: &[]string{
			// TODO(sauterp) remove once the CCM is no longer necessary for the CSI.
			"exoscale-cloud-controller",
		},
		CNI:         ptr("calico"),
		Description: ptr("This cluster was created to test the exoscale CSI driver in SKS."),
		Name:        ptr(c.Name),
		Nodepools: []*exov2.SKSNodepool{
			ptr(nodepool),
		},
		ServiceLevel: ptr("pro"),
		Version:      ptr(latestSKSVersion),
		Zone:         ptr(zone),
	}

	newCluster, err := c.Ego.CreateSKSCluster(c.exoV2Context, zone, sksCluster)
	if err != nil {
		return err
	}

	c.ID = *newCluster.ID
	slog.Info("successfully created cluster", "clusterID", c.ID)

	_, err = c.Ego.CreateSKSNodepool(c.exoV2Context, zone, newCluster, &nodepool)
	if err != nil {
		// this can error even when the nodepool is successfully created
		// it's probably a bug, so we're not returning the error
		slog.Warn("error creating nodepool", "err", err)
	}

	return nil
}

func exitApplication(msg string, err error) {
	slog.Error(msg, "err", err)

	flag.Usage()

	os.Exit(1)
}

func ConfigureCluster(createCluster bool, name, zone string) (*Cluster, error) {
	v2Client, ctx, ctxCancel, err := createV2ClientAndContext()
	if err != nil {
		return nil, fmt.Errorf("error creating egoscale v2 client: %w", err)
	}

	cluster := &Cluster{
		Ego:                v2Client,
		Name:               name,
		exoV2Context:       ctx,
		exoV2ContextCancel: ctxCancel,
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

	if !*flags.DontApplyCSI {
		if err := testCluster.applyCSI(); err != nil {
			return fmt.Errorf("error applying CSI: %w", err)
		}
	}

	// give the CSI some time to become ready
	// TODO find a more appropriate way to do this.
	time.Sleep(30 * time.Second)

	return nil
}
