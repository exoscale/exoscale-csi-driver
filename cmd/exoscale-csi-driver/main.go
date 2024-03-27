package main

import (
	"flag"
	"fmt"
	"os"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	"github.com/exoscale/exoscale-csi-driver/cmd/exoscale-csi-driver/buildinfo"
	"github.com/exoscale/exoscale-csi-driver/driver"

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
