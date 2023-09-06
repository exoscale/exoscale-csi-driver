package flags

import (
	"flag"
	"fmt"
	"sync"
)

const (
	clusterNameFlagName       = "cluster-name"
	createClusterFlagName     = "create-cluster"
	createCSISecretFlagName   = "create-csi-secret"
	imageFlagName             = "image"
	dontApplyCSIFlagName      = "dont-apply-csi"
	dontCleanUpTestNSFlagName = "dont-clean-up-test-ns"
	tearDownCSIFlagName       = "tear-down-csi"
	tearDownClusterFlagName   = "tear-down-cluster"

	defaultZone = "ch-gva-2"
)

var (
	ClusterName       = flag.String(clusterNameFlagName, "", "[Required] Name of the cluster to test against.")
	CreateCluster     = flag.Bool(createClusterFlagName, false, "Creates a new SKS cluster before the integration tests if it doesn't exist already.")
	CreateCSISecret   = flag.Bool(createCSISecretFlagName, false, "create-csi-secret")
	Image             = flag.String(imageFlagName, "", "CSI image to test")
	DontApplyCSI      = flag.Bool(dontApplyCSIFlagName, false, "don't apply the CSI manifests before integration tests. This option is only a convenience to speed up development of new tests.")
	DontCleanUpTestNS = flag.Bool(dontCleanUpTestNSFlagName, false, "don't clean up test namespaces(for debugging)")
	TearDownCSI       = flag.Bool(tearDownCSIFlagName, false, "tear down the CSI after the integration tests")
	TearDownCluster   = flag.Bool(tearDownClusterFlagName, false, "destroy test cluster after integration tests have completed")

	Zone = flag.String("zone", defaultZone, "zone where the tests should be executed")
)

var validateFlagsOnce sync.Once

func ValidateFlags() error {
	var err error

	validateFlagsOnce.Do(func() {
		flag.Parse()

		if *ClusterName == "" {
			err = fmt.Errorf("the flags --%s is required", clusterNameFlagName)
		}

		if *Image != "" && *DontApplyCSI {
			err = fmt.Errorf("the flags --%s and --%s are mutually exclusive", imageFlagName, dontApplyCSIFlagName)
		}
	})

	return err
}
