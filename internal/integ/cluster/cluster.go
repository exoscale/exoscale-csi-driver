// Package cluster takes care of provisioning and destroying SKS clusters for testing the CSI
package cluster

import (
	exov3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
)

var (
	testCluster *Cluster
)

func Get() *Cluster {
	return testCluster
}

type Cluster struct {
	Name        string
	ID          exov3.UUID
	K8s         *k8s.K8S
	Ego         *exov3.Client
	APIKeyName  string
	APIRoleName string
}
