package main

import (
	"flag"
	"fmt"
	"os"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	"github.com/exoscale/exoscale-csi-driver/cmd/exoscale-csi-driver/buildinfo"
	"github.com/exoscale/exoscale-csi-driver/driver"

	"github.com/kubernetes-sigs/aws-ebs-csi-driver/cmd/hooks"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var (
	endpoint    = flag.String("endpoint", "unix:/tmp/csi.sock", "CSI endpoint")
	prefix      = flag.String("prefix", "", "Prefix to add in block volume name")
	versionFlag = flag.Bool("version", false, "Print the version and exit")
	mode        = flag.String("mode", string(driver.AllMode), "The mode in which the CSI driver will be run (all, node, controller)")

	// These are set during build time via -ldflags
	version   string = "dirty"
	commit    string
	buildDate string
)

func init() {
	buildinfo.Version = version
	buildinfo.GitCommit = commit
	buildinfo.BuildDate = buildDate
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	if *versionFlag {
		info := driver.GetVersion()

		fmt.Printf("%+v", info)
		os.Exit(0)
	}

	// Handle pre-stop-hook
	if len(os.Args) > 1 && os.Args[1] == "pre-stop-hook" {
		clientset, err := inClusterClient()
		if err != nil {
			klog.ErrorS(err, "unable to communicate with k8s API")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		if err := hooks.PreStop(clientset); err != nil {
			klog.ErrorS(err, "failed to execute PreStop lifecycle hook")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		klog.FlushAndExit(klog.ExitFlushTimeout, 0)
	}

	// Mostly for internal use.
	apiEndpoint := os.Getenv("EXOSCALE_API_ENDPOINT")

	exoDriver, err := driver.NewDriver(&driver.DriverConfig{
		Endpoint:     *endpoint,
		Mode:         driver.Mode(*mode),
		Prefix:       *prefix,
		Credentials:  credentials.NewEnvCredentials(),
		ZoneEndpoint: v3.Endpoint(apiEndpoint),
	})
	if err != nil {
		klog.Error(err)
		klog.Fatalln(err)
	}

	klog.Info("NewDriver OK")

	if err := exoDriver.Run(); err != nil {
		klog.Error(err)
		klog.Fatalln(err)
	}

	klog.Info("Run OK")
}

func inClusterClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain in cluster config: %w", err)
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s api client: %w", err)
	}
	return clientset, nil
}
