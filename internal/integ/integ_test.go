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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/cluster"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/util"
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

func awaitExpectation[T any](t *testing.T, expected T, get func() T) {
	var actual T

	for i := 0; i < 10; i++ {
		var err error = nil

		actual = func() T {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("failed %v", r)
				}
			}()

			return get()
		}()

		time.Sleep(10 * time.Second)

		if err != nil {
			continue
		}

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
	ns.ApplyPVC(pvcName, "10Gi", false)

	awaitExpectation(t, "Bound", func() interface{} {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, pvcName, metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})
}

func TestInvalidVolumeSize(t *testing.T) {
	testName := "invalid-size-vol"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	pvcName := generatePVCName(testName)
	ns.ApplyPVC(pvcName, "205713Mi", false)
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
	ns.ApplyPVC(pvcName, "10Gi", false)
	ns.Apply(fmt.Sprintf(basicDeployment, pvcName))

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

	egoClient, err := util.CreateEgoscaleClient()
	assert.NoError(t, err)

	testFunc := func(useRetainStorageClass bool) func(t *testing.T) {
		return func(t *testing.T) {
			pvcName := ""
			if useRetainStorageClass {
				pvcName = generatePVCName(testName + "-retain")
			} else {
				pvcName = generatePVCName(testName)
			}
			ns.ApplyPVC(pvcName, "10Gi", useRetainStorageClass)

			pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

			getPVC := func() *corev1.PersistentVolumeClaim {
				pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
				assert.NoError(t, err)

				return pvc
			}

			awaitExpectation(t, corev1.ClaimBound, func() corev1.PersistentVolumeClaimPhase {
				pvc := getPVC()

				return pvc.Status.Phase
			})

			pvc := getPVC()
			assert.NotNil(t, pvc)
			pvName := getPVC().Spec.VolumeName

			err = pvcClient.Delete(ns.CTX, pvcName, metav1.DeleteOptions{})
			assert.NoError(t, err)

			awaitExpectation(t, 0, func() int {
				pvcs, err := pvcClient.List(ns.CTX, metav1.ListOptions{})
				assert.NoError(t, err)

				return len(pvcs.Items)
			})

			expectedVolumeName := ""
			if useRetainStorageClass {
				// The volume should be retained, hence a b/s volume with the same name as the pvc should be found.
				expectedVolumeName = pvName

				t.Cleanup(func() {
					// delete the retained volume after the test
					bsVolList, err := egoClient.ListBlockStorageVolumes(ns.CTX)
					assert.NoError(t, err)

					bsVol, err := bsVolList.FindBlockStorageVolume(pvName)
					if err == nil {
						op, err := egoClient.DeleteBlockStorageVolume(ns.CTX, bsVol.ID)
						if err != nil {
							slog.Warn("failed to clean up volume", "name", bsVol.Name, "err", err)
						}

						if _, err := egoClient.Wait(ns.CTX, op, v3.OperationStateSuccess); err != nil {
							slog.Warn("failed to clean up volume", "name", bsVol.Name, "err", err)
						}
					}
				})
			}

			awaitExpectation(t, expectedVolumeName, func() string {
				bsVolList, err := egoClient.ListBlockStorageVolumes(ns.CTX)
				assert.NoError(t, err)

				bsVol, _ := bsVolList.FindBlockStorageVolume(pvName)
				return bsVol.Name
			})
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
	ns.ApplyPVC(pvcName, "10Gi", false)

	pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

	awaitExpectation(t, corev1.ClaimBound, func() corev1.PersistentVolumeClaimPhase {
		pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})

	// create snapshot
	ns.Apply(fmt.Sprintf(basicSnapshot, pvcName))

	snapshotClient := ns.K.DynamicClient.Resource(getSnapshotCRDResource()).Namespace(ns.Name)

	awaitExpectation(t, true, func() bool {
		crdInstance, err := snapshotClient.Get(ns.CTX, "my-snap-1", v1.GetOptions{})
		if !assert.NoError(t, err) {
			return false
		}

		status, ok := crdInstance.Object["status"].(map[string]interface{})
		if !assert.True(t, ok) {
			return false
		}

		readyToUse, ok := status["readyToUse"]
		if !ok {
			return false
		}

		readyToUseBool, ok := readyToUse.(bool)
		if !ok {
			return false
		}

		return readyToUseBool
	})

	// create volume from snapshot
	ns.Apply(basicVolumeFromSnapshot)

	awaitExpectation(t, corev1.ClaimBound, func() corev1.PersistentVolumeClaimPhase {
		pvc, err := pvcClient.Get(ns.CTX, "my-snap-1-pvc", metav1.GetOptions{})
		assert.NoError(t, err)

		return pvc.Status.Phase
	})

	// delete snapshot
	err := snapshotClient.Delete(ns.CTX, "my-snap-1", v1.DeleteOptions{})
	assert.NoError(t, err)

	awaitExpectation(t, 0, func() int {
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
