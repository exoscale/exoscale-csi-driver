package k8s

import (
	"context"
	"io/ioutil"
	"log/slog"
	"strings"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/dynamic"
)

type K8S struct {
	DiscoveryClient   *discovery.DiscoveryClient
	ClientSet         *kubernetes.Clientset
	DynamicClient     *dynamic.DynamicClient
	ResourceList      []*metav1.APIResourceList
	WatchedNamespaces sync.Map
}

func CreateClients(kubeconfig []byte) (*K8S, error) {
	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeconfig)
	if err != nil {
		return nil, err
	}

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}

	_, resourceList, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &K8S{
		DynamicClient:   dynamicClient,
		ClientSet:       clientset,
		DiscoveryClient: discoveryClient,
		ResourceList:    resourceList,
	}, nil
}

func (k *K8S) DeleteManifest(ctx context.Context, file string) error {
	fileContent, err := ioutil.ReadFile(file)
	if err != nil {
		return err
	}

	yamlDocs := strings.Split(string(fileContent), "\n---\n")
	for _, yamlDoc := range yamlDocs {
		// Ignore empty documents
		if strings.TrimSpace(yamlDoc) == "" {
			continue
		}

		if err := k.deleteManifest(ctx, []byte(yamlDoc)); err != nil {
			return err
		}
	}

	return nil
}

func (k *K8S) ApplyManifest(ctx context.Context, file string) error {
	fileContent, err := ioutil.ReadFile(file)
	if err != nil {
		return err
	}

	fileContentStr := string(fileContent)
	if *flags.Image != "" {
		slog.Info("testing CSI image", "path", *flags.Image)

		fileContentStr = strings.ReplaceAll(fileContentStr, `exoscale/csi-driver:latest`, *flags.Image)
	}

	yamlDocs := strings.Split(fileContentStr, "\n---\n")
	for _, yamlDoc := range yamlDocs {
		// Ignore empty documents
		if strings.TrimSpace(yamlDoc) == "" {
			continue
		}

		if err := k.applyManifest(ctx, []byte(yamlDoc)); err != nil {
			return err
		}
	}

	return nil
}

func (k *K8S) findResource(kind string) *metav1.APIResource {
	for _, resources := range k.ResourceList {
		for _, r := range resources.APIResources {
			if r.Kind == kind {
				return &r
			}
		}
	}

	slog.Warn("could not find kind on server", "kind", kind)

	return nil
}

func (k *K8S) deleteManifest(ctx context.Context, manifest []byte) error {
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, gvk, err := decoder.Decode(manifest, nil, obj)
	if err != nil {
		return err
	}

	rrr := k.findResource(obj.GetKind())
	if rrr == nil {
		return nil
	}

	gvr := gvk.GroupVersion().WithResource(rrr.Name)
	namespace := ""
	if rrr.Namespaced {
		namespace = obj.GetNamespace()
	}
	resourceInterface := k.DynamicClient.Resource(gvr).Namespace(namespace)

	slog.Info("deleting resource", "resource", obj.GetName())

	err = resourceInterface.Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
	if err != nil {
		slog.Error("deleting", "resource", gvr, "name", obj.GetName(), "err", err)
	}

	return nil
}

func printEvent(event *v1.Event) {
	switch event.Type {
	case v1.EventTypeWarning:
		slog.Warn("event", "name", event.Name, "message", event.Message, "namespace", event.Namespace)
	case v1.EventTypeNormal:
		slog.Info("event", "name", event.Name, "message", event.Message, "namespace", event.Namespace)
	}
}

func (k *K8S) PrintEvents(ctx context.Context, ns string) {
	// we only need to watch each namespace once
	_, ok := k.WatchedNamespaces.Load(ns)
	if ok {
		return
	}

	k.WatchedNamespaces.Store(ns, struct{}{})

	// first we print all active events
	list, err := k.ClientSet.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Warn("failed to list events", "error", err)
	}

	for _, event := range list.Items {
		printEvent(&event)
	}

	// watch for new events and print them
	watchList, err := k.ClientSet.CoreV1().Events(ns).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Warn("failed to watch events", "error", err)
	}

	select {
	case watchEvent := <-watchList.ResultChan():
		if watchEvent.Type == watch.Added || watchEvent.Type == watch.Modified {
			if event, ok := watchEvent.Object.(*v1.Event); ok {
				printEvent(event)
			}
		}
	case <-ctx.Done():
		return
	}
}

func (k *K8S) applyManifest(ctx context.Context, manifest []byte) error {
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, gvk, err := decoder.Decode(manifest, nil, obj)
	if err != nil {
		return err
	}

	rrr := k.findResource(obj.GetKind())
	if rrr == nil {
		return nil
	}

	gvr := gvk.GroupVersion().WithResource(rrr.Name)
	namespace := ""
	if rrr.Namespaced {
		namespace = obj.GetNamespace()
		go k.PrintEvents(ctx, namespace)
	}
	resourceInterface := k.DynamicClient.Resource(gvr).Namespace(namespace)

	res, err := resourceInterface.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		slog.Info("creating", "resource", gvr, "name", obj.GetName())

		// If the resource doesn't exist, create it
		_, err = resourceInterface.Create(ctx, obj, metav1.CreateOptions{})
		if err != nil {
			slog.Error("failed to create resource", "err", err)
		}
	} else {
		slog.Info("updating", "resource", gvr, "name", obj.GetName())

		obj.SetResourceVersion(res.GetResourceVersion())

		// If it exists, update it
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			_, updateErr := resourceInterface.Update(ctx, obj, metav1.UpdateOptions{})
			return updateErr
		})
		if retryErr != nil {
			slog.Error("failed to update resource", "err", retryErr)
		}
	}

	_, newResourceList, err := k.DiscoveryClient.ServerGroupsAndResources()
	if err != nil {
		slog.Warn("failed to update resource list", "err", err)
	} else {
		k.ResourceList = newResourceList
	}

	return nil
}

// ApplySecret creates the secret needed for the CSI to work if it doesn't exist already.
func (k *K8S) ApplySecret(ctx context.Context, apiKey, apiSecret string) error {
	name := "exoscale-csi-credentials"
	namespace := "kube-system"

	secretData := map[string][]byte{
		util.APIKeyEnvVar:    []byte(apiKey),
		util.APISecretEnvVar: []byte(apiSecret),
	}

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: secretData,
		Type: "Opaque",
	}

	sec := k.ClientSet.CoreV1().Secrets(namespace)
	_, err := sec.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}

		slog.Info("creating secret", "name", secret.Name)
		_, err = sec.Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		slog.Info("updating secret", "name", secret.Name)
		_, err = sec.Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}
