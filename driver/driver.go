package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/exoscale-csi-driver/cmd/exoscale-csi-driver/buildinfo"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// Mode represents the mode in which the CSI driver started
type Mode string

const (
	// ControllerMode represents the controller mode
	ControllerMode Mode = "controller"
	// NodeMode represents the node mode
	NodeMode Mode = "node"
	// AllMode represents the the controller and the node mode at the same time
	AllMode Mode = "all"
)

const (
	// DriverName is the official name for the Exoscale CSI plugin
	DriverName      = "csi.exoscale.com"
	ZoneTopologyKey = "topology." + DriverName + "/zone"
)

// DriverConfig is used to configure a new Driver
type DriverConfig struct {
	Endpoint          string
	Prefix            string
	Mode              Mode
	APIKey, APISecret string
	RestConfig        *rest.Config
}

// Driver implements the interfaces csi.IdentityServer, csi.ControllerServer and csi.NodeServer
type Driver struct {
	controllerService
	nodeService
	config *DriverConfig

	srv *grpc.Server
}

// NewDriver returns a CSI plugin
func NewDriver(config *DriverConfig) (*Driver, error) {
	klog.Infof("driver: %s version: %s", DriverName, buildinfo.Version)
	nodeMeta, err := getExoscaleNodeMetadata()
	if err != nil {
		return nil, fmt.Errorf("new driver: %w", err)
	}

	driver := &Driver{
		config: config,
	}

	var client *v3.Client
	if config.Mode != NodeMode {
		client, err = v3.NewClient(config.APIKey, config.APISecret,
			v3.ClientOptWithURL(nodeMeta.zone),
		)
		if err != nil {
			return nil, fmt.Errorf("new driver: %w", err)
		}
	}

	switch config.Mode {
	case ControllerMode:
		driver.controllerService = newControllerService(client, nodeMeta)
	case NodeMode:
		driver.nodeService = newNodeService(nodeMeta)
	case AllMode:
		driver.controllerService = newControllerService(client, nodeMeta)
		driver.nodeService = newNodeService(nodeMeta)
	default:
		return nil, fmt.Errorf("unknown mode for driver: %s", config.Mode)
	}

	return driver, nil
}

// Run starts the CSI plugin on the given endpoint
func (d *Driver) Run() error {
	endpointURL, err := url.Parse(d.config.Endpoint)
	if err != nil {
		return err
	}

	if endpointURL.Scheme != "unix" {
		klog.Errorf("only unix domain sockets are supported, not %s", endpointURL.Scheme)
		return fmt.Errorf("errSchemeNotSupported")
	}

	addr := path.Join(endpointURL.Host, filepath.FromSlash(endpointURL.Path))

	klog.Infof("Removing existing socket if existing")
	if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
		klog.Errorf("error removing existing socket")
		return fmt.Errorf("errRemovingSocket")
	}

	dir := filepath.Dir(addr)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			return err
		}
	}

	listener, err := net.Listen(endpointURL.Scheme, addr)
	if err != nil {
		return err
	}

	// log error through a grpc unary interceptor
	logErrorHandler := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			klog.Errorf("error for %s: %v", info.FullMethod, err)
		}
		return resp, err
	}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logErrorHandler),
	}

	d.srv = grpc.NewServer(opts...)

	csi.RegisterIdentityServer(d.srv, d)

	switch d.config.Mode {
	case ControllerMode:
		csi.RegisterControllerServer(d.srv, d)
	case NodeMode:
		csi.RegisterNodeServer(d.srv, d)
	case AllMode:
		csi.RegisterControllerServer(d.srv, d)
		csi.RegisterNodeServer(d.srv, d)
	default:
		return fmt.Errorf("unknown mode for driver: %s", d.config.Mode) // should never happen though

	}

	// graceful shutdown
	gracefulStop := make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-gracefulStop
		d.srv.GracefulStop()
	}()

	klog.Infof("CSI server started on %s", d.config.Endpoint)
	return d.srv.Serve(listener)
}

type nodeMetadata struct {
	zone       v3.URL
	InstanceID v3.UUID
}

func customZones(customZonesStr string) (map[string]v3.URL, error) {
	zonesMap := map[string]v3.URL{}
	err := json.Unmarshal([]byte(customZonesStr), &zonesMap)
	if err != nil {
		return nil, err
	}
	return zonesMap, nil
}

func getExoscaleNodeMetadata() (*nodeMetadata, error) {
	podName := os.Getenv("POD_NAME")
	namespace := os.Getenv("POD_NAMESPACE")
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}
	nodeName := pod.Spec.NodeName

	node, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	region, ok := node.Labels["topology.kubernetes.io/region"]
	if !ok {
		return nil, fmt.Errorf("no zone found on node, missing Exoscale CCM")
	}

	if !strings.HasPrefix(node.Spec.ProviderID, "exoscale://") {
		return nil, fmt.Errorf("no Instance ID found on node, missing Exoscale CCM")
	}

	instanceID, err := v3.ParseUUID(node.Spec.ProviderID[len("exoscale://"):])
	if err != nil {
		return nil, fmt.Errorf("node meta data Instance ID %s: %w", node.Spec.ProviderID, err)
	}

	var zonesToURL map[string]v3.URL

	customZonesStr := os.Getenv("ZONE_TO_URL")
	if customZonesStr != "" {
		zonesToURL, err = customZones(customZonesStr)
		if err != nil {
			return nil, fmt.Errorf("couldn't read custom zone mapping %w", err)
		}
	} else {
		zonesToURL = v3.Zones
	}

	zone, ok := zonesToURL[region]
	if !ok {
		return nil, fmt.Errorf("invalid region zone name: %s", region)
	}

	return &nodeMetadata{
		zone:       zone,
		InstanceID: instanceID,
	}, nil
}
