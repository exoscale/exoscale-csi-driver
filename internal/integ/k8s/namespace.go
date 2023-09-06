package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"
)

type Namespace struct {
	K    *K8S
	t    *testing.T
	Name string
	CTX  context.Context
}

func (ns *Namespace) Apply(manifest string) {
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, gvk, err := decoder.Decode([]byte(manifest), nil, obj)
	if err != nil {
		ns.t.Error("failed to decode manifest")

		return
	}

	obj.SetNamespace(ns.Name)

	res := ns.K.findResource(obj.GetKind())
	if res == nil {
		ns.t.Error("unknown resource")

		return
	}

	gvr := gvk.GroupVersion().WithResource(res.Name)
	resourceInterface := ns.K.DynamicClient.Resource(gvr).Namespace(ns.Name)

	slog.Info("creating", "resource", gvr, "name", obj.GetName())

	_, err = resourceInterface.Create(ns.CTX, obj, metav1.CreateOptions{})
	if err != nil {
		slog.Error("failed to create resource", "err", err)
	}

	assert.NoError(ns.t, err)
}

func generateNSName(testName string) string {
	return fmt.Sprintf("%s-%s-%d", "csi-test-ns", testName, rand.Int())
}

func CreateTestNamespace(t *testing.T, k *K8S, testName string) *Namespace {
	name := generateNSName(testName)
	ns := &Namespace{
		t:    t,
		K:    k,
		Name: name,
		CTX:  context.Background(),
	}

	namespace := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	slog.Info("creating test namespace", "name", ns.Name)

	_, err := ns.K.ClientSet.CoreV1().Namespaces().Create(ns.CTX, namespace, metav1.CreateOptions{})
	assert.NoError(ns.t, err)

	if !*flags.DontCleanUpTestNS {
		t.Cleanup(func() {
			slog.Info("cleaning up test namespace", "name", ns.Name)
			err := ns.K.ClientSet.CoreV1().Namespaces().Delete(ns.CTX, name, metav1.DeleteOptions{})
			assert.NoError(ns.t, err)
		})
	}

	return ns
}
