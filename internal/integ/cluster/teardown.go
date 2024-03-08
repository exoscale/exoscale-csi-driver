package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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

func (c Cluster) Wait(ctx context.Context, op *exov3.Operation, states ...exov3.OperationState) (*exov3.Operation, error) {
	if op == nil {
		return nil, fmt.Errorf("operation is nil")
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// if op.State != exov3.OperationStatePending {
	// 	return op, nil
	// }

	var operation *exov3.Operation
polling:
	for {
		select {
		case <-ticker.C:
			o, err := c.Ego.GetOperation(ctx, op.ID)
			if err != nil {
				return nil, err
			}
			if o.State == exov3.OperationStatePending {
				continue
			}

			operation = o
			break polling
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if len(states) == 0 {
		return operation, nil
	}

	for _, st := range states {
		if operation.State == st {
			return operation, nil
		}
	}

	var ref exov3.OperationReference
	if operation.Reference != nil {
		ref = *operation.Reference
	}

	return nil,
		fmt.Errorf("operation: %q %v, state: %s, reason: %q, message: %q",
			operation.ID,
			ref,
			operation.State,
			operation.Reason,
			operation.Message,
		)
}

func (c *Cluster) awaitID(op *exov3.Operation, err error) (exov3.UUID, error) {
	if err != nil {
		return "", err
	}

	finishedOP, err := c.Wait(c.context, op, exov3.OperationStateSuccess)
	if err != nil {
		return "", err
	}

	if finishedOP.Reference != nil {
		return finishedOP.Reference.ID, nil
	}

	return "", nil
}

func (c *Cluster) awaitSuccess(op *exov3.Operation, err error) error {
	_, err = c.awaitID(op, err)
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
