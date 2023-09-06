package main

import (
	"flag"
	"fmt"
	"os"

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

	apiKey := os.Getenv("EXOSCALE_API_KEY")
	apiSecret := os.Getenv("EXOSCALE_API_SECRET")

	// The node mode don't need secrets and do not interact with Exoscale API.
	if *mode != string(driver.NodeMode) && (apiKey == "" || apiSecret == "") {
		klog.Fatalln("missing or incomplete API credentials")
	}

	exoDriver, err := driver.NewDriver(&driver.DriverConfig{
		Endpoint:  *endpoint,
		Mode:      driver.Mode(*mode),
		Prefix:    *prefix,
		APIKey:    apiKey,
		APISecret: apiSecret,
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
