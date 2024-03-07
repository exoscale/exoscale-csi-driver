package cluster

import (
	"fmt"
	"log/slog"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"

	exov3 "github.com/exoscale/egoscale/v3"
)

func (c *Cluster) tearDownCluster() error {
	id, err := c.getClusterID()
	if err != nil {
		return fmt.Errorf("error getting cluster ID: %w", err)
	}

	cluster, err := c.Ego.GetSKSCluster(c.context, id)
	if err != nil {
		return err
	}

	if len(cluster.Nodepools) > 0 {
		if err := c.awaitSuccess(c.Ego.DeleteSKSNodepool(c.context, cluster.ID, cluster.Nodepools[0].ID)); err != nil {
			return fmt.Errorf("error deleting nodepool: %w", err)
		}
	}

	return c.awaitSuccess(c.Ego.DeleteSKSCluster(c.context, cluster.ID))
}

func (c *Cluster) awaitSuccess(op *exov3.Operation, err error) error {
	if err != nil {
		return err
	}

	_, err = c.Ego.Wait(c.context, op, exov3.OperationStateSuccess)

	return err
}

func (c *Cluster) TearDown() error {
	if *flags.TearDownCSI {
		if err := c.tearDownCSI(); err != nil {
			return err
		}
	}

	if *flags.TearDownCluster {
		if err := c.tearDownCluster(); err != nil {
			return err
		}
	}

	c.cancelContext()

	return nil
}

func (c *Cluster) tearDownCSI() error {
	var finalErr error = nil

	for _, manifestPath := range allManifests {
		err := c.K8s.DeleteManifest(c.context, manifestDir+manifestPath)
		if err != nil {
			slog.Error("failed to delete manifest", "manifest", manifestPath, "err", err)

			finalErr = fmt.Errorf("errors while deleting manifests: %w", err)
		}
	}

	err := c.deleteAPIKeyAndRole()
	if err != nil {
		slog.Error("failed to clean up CSI API key and role", "err", err)

		finalErr = fmt.Errorf("errors while cleaning up CSI API key and role: %w", err)
	}

	return finalErr
}
