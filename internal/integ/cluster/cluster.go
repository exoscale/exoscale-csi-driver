// Package cluster takes care of provisioning and destroying SKS clusters for testing the CSI
package cluster

import (
	"context"

	exov2 "github.com/exoscale/egoscale/v2"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
)

var (
	testCluster *Cluster
)

func Get() *Cluster {
	return testCluster
}

type Cluster struct {
	exoV2Context       context.Context
	exoV2ContextCancel context.CancelFunc

	Name        string
	ID          string
	K8s         *k8s.K8S
	Ego         *exov2.Client
	APIKeyName  string
	APIRoleName string
}
