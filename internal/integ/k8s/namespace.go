package k8s

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"testing"

	"text/template"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/client"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"
)

type Namespace struct {
	K       *K8S
	t       *testing.T
	Name    string
	CTX     context.Context
	Volumes []string
}

type PVC struct {
	Name             string
	StorageClassName string
}

var pvcTemplate = `
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ .Name }}
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
  storageClassName: {{ .StorageClassName }}`

func (ns *Namespace) ApplyPVC(name string, useStorageClassRetain bool) {
	tmpl := template.New("volumeTemplate")
	parsedTmpl, err := tmpl.Parse(pvcTemplate)
	if err != nil {
		slog.Error("failed to parse PVC template", "err", err)

		return
	}

	data := PVC{
		Name:             name,
		StorageClassName: "exoscale-sbs",
	}

	if useStorageClassRetain {
		data.StorageClassName = "exoscale-bs-retain"
	}

	buf := &bytes.Buffer{}
	if parsedTmpl.Execute(buf, data) != nil {
		slog.Error("failed to execute PVC template", "err", err)

		return
	}

	ns.Apply(buf.String())

	ns.Volumes = append(ns.Volumes, name)
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

	go ns.K.PrintEvents(ns.CTX, ns.Name)

	if !*flags.DontCleanUpTestNS {
		t.Cleanup(func() {
			slog.Info("cleaning up test namespace", "name", ns.Name)
			err := ns.K.ClientSet.CoreV1().Namespaces().Delete(ns.CTX, name, metav1.DeleteOptions{})
			assert.NoError(ns.t, err)

			// delete volumes that may have been retained
			egoClient, err := client.CreateEgoscaleClient()
			assert.NoError(ns.t, err)

			bsVolList, err := egoClient.ListBlockStorageVolumes(ns.CTX)
			assert.NoError(t, err)
			for _, volume := range ns.Volumes {
				bsVol, err := bsVolList.FindBlockStorageVolume(volume)
				if err == nil {
					op, err := egoClient.DeleteBlockStorageVolume(ns.CTX, bsVol.ID)
					if err != nil {
						slog.Warn("failed to clean up volume", "name", bsVol.Name, "err", err)
					}

					if _, err := egoClient.Wait(ns.CTX, op); err != nil {
						slog.Warn("failed to clean up volume", "name", bsVol.Name, "err", err)
					}
				}
			}
		})
	}

	return ns
}
