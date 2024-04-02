package cluster

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"

	exov3 "github.com/exoscale/egoscale/v3"
)

func (c *Cluster) tearDownCluster(ctx context.Context) error {
	id, err := c.getClusterID(ctx)
	if err != nil {
		return fmt.Errorf("error getting cluster ID: %w", err)
	}

	cluster, err := c.Ego.GetSKSCluster(ctx, id)
	if err != nil {
		return err
	}

	if len(cluster.Nodepools) > 0 {
		op, err := c.Ego.DeleteSKSNodepool(ctx, cluster.ID, cluster.Nodepools[0].ID)
		if err := c.awaitSuccess(ctx, op, err); err != nil {
			return fmt.Errorf("error deleting nodepool: %w", err)
		}
	}

	op, err := c.Ego.DeleteSKSCluster(ctx, cluster.ID)
	return c.awaitSuccess(ctx, op, err)
}

func (c *Cluster) awaitID(ctx context.Context, op *exov3.Operation, err error) (exov3.UUID, error) {
	if err != nil {
		return "", err
	}

	finishedOP, err := c.Ego.Wait(ctx, op, exov3.OperationStateSuccess)
	if err != nil {
		return "", err
	}

	if finishedOP.Reference != nil {
		return finishedOP.Reference.ID, nil
	}

	return "", nil
}

func (c *Cluster) awaitSuccess(ctx context.Context, op *exov3.Operation, err error) error {
	_, err = c.awaitID(ctx, op, err)
	return err
}

func (c *Cluster) TearDown() error {
	ctx := context.Background()
	if *flags.TearDownCSI {
		if err := c.tearDownCSI(ctx); err != nil {
			return err
		}
	}

	if *flags.TearDownCluster {
		if err := c.tearDownCluster(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (c *Cluster) tearDownCSI(ctx context.Context) error {
	var finalErr error = nil

	for _, manifestPath := range allManifests {
		err := c.K8s.DeleteManifest(ctx, manifestDir+manifestPath)
		if err != nil {
			slog.Error("failed to delete manifest", "manifest", manifestPath, "err", err)

			finalErr = fmt.Errorf("errors while deleting manifests: %w", err)
		}
	}

	err := c.deleteAPIKeyAndRole(ctx)
	if err != nil {
		slog.Error("failed to clean up CSI API key and role", "err", err)

		finalErr = fmt.Errorf("errors while cleaning up CSI API key and role: %w", err)
	}

	return finalErr
}
