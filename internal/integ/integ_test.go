package integ

import (
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/cluster"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
)

func TestMain(m *testing.M) {
	exitCode := 0

	err := cluster.Setup()
	if err != nil {
		slog.Error("error during setup", "err", err)
		exitCode = 1
	}

	if err == nil {
		exitCode = m.Run()
	}

	if cluster.Get() != nil {
		err = cluster.Get().TearDown()
		if err != nil {
			slog.Error("error during tear down", "err", err)

			exitCode = 1
		}
	}

	os.Exit(exitCode)
}

var basicPVC = `
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-sbs-pvc
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 100Gi
  storageClassName: exoscale-sbs
`

type getFunc func() interface{}

func awaitExpectation(t *testing.T, expected interface{}, get getFunc) {
	var actual interface{}

	for i := 0; i < 10; i++ {
		actual = get()

		time.Sleep(5 * time.Second)

		if assert.ObjectsAreEqualValues(expected, actual) {
			break
		}
	}

	assert.EqualValues(t, expected, actual)
}

func TestVolumeCreation(t *testing.T) {
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, "create-vol")

	ns.Apply(basicPVC)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, "my-sbs-pvc", metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})
}

var basicDeployment = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-awesome-deployment
spec:
  selector:
    matchLabels:
      app: my-awesome-deployment
  replicas: 1
  template:
    metadata:
      labels:
        app: my-awesome-deployment
    spec:
      containers:
        - name: my-awesome-nginx
          image: nginx
          volumeMounts:
          - mountPath: "/var/log/nginx"
            name: my-awesome-logs
      volumes:
        - name: my-awesome-logs
          persistentVolumeClaim:
            claimName: my-sbs-pvc
`

func TestVolumeAttach(t *testing.T) {
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, "vol-attach")

	ns.Apply(basicPVC)
	ns.Apply(basicDeployment)

	go ns.K.PrintEvents(ns.CTX, ns.Name)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, "my-sbs-pvc", metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})

	awaitExpectation(t, "Running", func() interface{} {
		pods, err := ns.K.ClientSet.CoreV1().Pods(ns.Name).List(ns.CTX, metav1.ListOptions{})
		assert.NoError(t, err)

		if len(pods.Items) < 1 {
			return nil
		}

		return pods.Items[0].Status.Phase
	})
}

func TestDeleteVolume(t *testing.T) {
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, "del-vol")

	ns.Apply(basicPVC)

	pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := pvcClient.Get(ns.CTX, "my-sbs-pvc", metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})

	err := pvcClient.Delete(ns.CTX, "my-sbs-pvc", metav1.DeleteOptions{})
	assert.NoError(t, err)

	awaitExpectation(t, 0, func() interface{} {
		pvcs, err := pvcClient.List(ns.CTX, metav1.ListOptions{})
		assert.NoError(t, err)

		return len(pvcs.Items)
	})

	// TODO (sauterp) once ego v3 is available check if volume is deleted (and retainPolicy)
}

const basicSnapshot = `
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: my-snap-1
spec:
  volumeSnapshotClassName: exoscale-snapshot
  source:
    persistentVolumeClaimName: my-sbs-pvc
`

const basicVolumeFromSnapshot = `
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-snap-1-pvc
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 100Gi
  dataSource:
    name: my-snap-1
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
  storageClassName: exoscale-sbs

`

func TestSnapshot(t *testing.T) {
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, "snapshot")

	ns.Apply(basicPVC)

	pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := pvcClient.Get(ns.CTX, "my-sbs-pvc", metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})

	// create snapshot
	ns.Apply(basicSnapshot)

	snapshotClient := ns.K.DynamicClient.Resource(getSnapshotCRDResource()).Namespace(ns.Name)

	awaitExpectation(t, true, func() interface{} {
		crdInstance, err := snapshotClient.Get(ns.CTX, "my-snap-1", v1.GetOptions{})
		if err != nil {
			return err
		}

		status, ok := crdInstance.Object["status"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("type assertion failed")
		}

		return status["readyToUse"]
	})

	// create volume from snapshot
	ns.Apply(basicVolumeFromSnapshot)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := pvcClient.Get(ns.CTX, "my-snap-1-pvc", metav1.GetOptions{})

		if err != nil {
			assert.NoError(t, err)
			return "error"
		}

		return pvc.Status.Phase
	})

	// delete snapshot
	err := snapshotClient.Delete(ns.CTX, "my-snap-1", v1.DeleteOptions{})
	assert.NoError(t, err)

	awaitExpectation(t, 0, func() interface{} {
		snapshots, err := snapshotClient.List(ns.CTX, v1.ListOptions{})
		if err != nil {
			assert.NoError(t, err)
			return 0
		}

		return len(snapshots.Items)
	})
}

func getSnapshotCRDResource() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: "volumesnapshots",
	}
}
