package integ

import (
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/exoscale/exoscale/csi-driver/internal/integ/client"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/cluster"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
)

func TestMain(m *testing.M) {
	// cluster creation takes a while so we increase the test timeout
	// This call has to happen before testing.M.Run as that's where
	// the flag `test.timeout` is used.
	err := flag.Set("test.timeout", "30m")
	if err != nil {
		slog.Warn("failed to set test timeout", "error", err)
	}

	exitCode := 0

	err = cluster.Setup()
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

func generatePVCName(testName string) string {
	return fmt.Sprintf("%s-%s-%d", "csi-test-pvc", testName, rand.Int())
}

type getFunc func() interface{}

func awaitExpectation(t *testing.T, expected interface{}, get getFunc) {
	var actual interface{}

	for i := 0; i < 10; i++ {
		actual = get()

		time.Sleep(10 * time.Second)

		if assert.ObjectsAreEqualValues(expected, actual) {
			break
		}
	}

	assert.EqualValues(t, expected, actual)
}

func TestVolumeCreation(t *testing.T) {
	testName := "create-vol"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	pvcName := generatePVCName(testName)
	ns.ApplyPVC(pvcName, false)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, pvcName, metav1.GetOptions{})
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
            claimName: %s
`

func TestVolumeAttach(t *testing.T) {
	testName := "vol-attach"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	pvcName := generatePVCName(testName)
	ns.ApplyPVC(pvcName, false)
	ns.Apply(fmt.Sprintf(basicDeployment, pvcName))

	go ns.K.PrintEvents(ns.CTX, ns.Name)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, pvcName, metav1.GetOptions{})
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
	testName := "del-vol"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	egoClient, err := client.CreateEgoscaleClient()
	assert.NoError(t, err)

	testFunc := func(useRetainStorageClass bool) func(t *testing.T) {
		return func(t *testing.T) {
			pvcName := ""
			if useRetainStorageClass {
				pvcName = generatePVCName(testName + "-retain")
			} else {
				pvcName = generatePVCName(testName)
			}
			ns.ApplyPVC(pvcName, useRetainStorageClass)

			pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

			awaitExpectation(t, "Bound", func() interface{} {
				pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
				assert.NoError(t, err)

				return pvc.Status.Phase
			})

			err := pvcClient.Delete(ns.CTX, pvcName, metav1.DeleteOptions{})
			assert.NoError(t, err)

			awaitExpectation(t, 0, func() interface{} {
				pvcs, err := pvcClient.List(ns.CTX, metav1.ListOptions{})
				assert.NoError(t, err)

				return len(pvcs.Items)
			})

			bsVolList, err := egoClient.ListBlockStorageVolumes(ns.CTX)
			assert.NoError(t, err)
			for _, volume := range ns.Volumes {
				_, err := bsVolList.FindBlockStorageVolume(volume)
				if useRetainStorageClass {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
				}
			}
		}
	}

	t.Run("storage-class-delete", testFunc(false))
	t.Run("storage-class-retain", testFunc(true))
}

const basicSnapshot = `
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: my-snap-1
spec:
  volumeSnapshotClassName: exoscale-snapshot
  source:
    persistentVolumeClaimName: %s
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
	testName := "snapshot"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	pvcName := generatePVCName(testName)
	ns.ApplyPVC(pvcName, false)

	pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})

	// create snapshot
	ns.Apply(fmt.Sprintf(basicSnapshot, pvcName))

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

		return string(pvc.Status.Phase)
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
