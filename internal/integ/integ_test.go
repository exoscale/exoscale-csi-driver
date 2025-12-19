package integ

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"

	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/cluster"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/flags"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/k8s"
	"github.com/exoscale/exoscale/csi-driver/internal/integ/util"
)

func TestMain(m *testing.M) {
	// cluster creation takes a while so we increase the test timeout
	// This call has to happen before testing.M.Run as that's where
	// the flag `test.timeout` is used.
	err := flag.Set("test.timeout", "60m")
	if err != nil {
		slog.Warn("failed to set test timeout", "error", err)
	}

	if err := flags.ValidateFlags(); err != nil {
		slog.Error("invalid flags", "err", err)
		flag.Usage()
		os.Exit(1)
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
	t.Helper()

	var actual T

	for range 20 {
		var err error = nil

		actual = func() T {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("failed %v", r)
				}
			}()

			return get()
		}()

		time.Sleep(20 * time.Second)

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
	ns.ApplyPVC(pvcName, "1Gi", false)

	awaitExpectation(t, "Bound", func() any {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, pvcName, metav1.GetOptions{})
		require.NoError(t, err)

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
	ns.ApplyPVC(pvcName, "1Gi", false)
	ns.Apply(fmt.Sprintf(basicDeployment, pvcName))

	awaitExpectation(t, "Bound", func() any {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, pvcName, metav1.GetOptions{})
		require.NoError(t, err)

		return pvc.Status.Phase
	})

	awaitExpectation(t, "Running", func() any {
		pods, err := ns.K.ClientSet.CoreV1().Pods(ns.Name).List(ns.CTX, metav1.ListOptions{})
		require.NoError(t, err)

		if len(pods.Items) < 1 {
			return nil
		}

		return pods.Items[0].Status.Phase
	})
}

func TestDeleteVolume(t *testing.T) {
	testName := "del-vol"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	egoClient, err := util.CreateEgoscaleClient(ns.CTX, v3.ZoneName(*flags.Zone))
	require.NoError(t, err)

	testFunc := func(useRetainStorageClass bool) func(t *testing.T) {
		return func(t *testing.T) {
			pvcName := ""
			if useRetainStorageClass {
				pvcName = generatePVCName(testName + "-retain")
			} else {
				pvcName = generatePVCName(testName)
			}
			ns.ApplyPVC(pvcName, "1Gi", useRetainStorageClass)

			pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

			getPVC := func() *corev1.PersistentVolumeClaim {
				pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
				require.NoError(t, err)

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
			require.NoError(t, err)

			awaitExpectation(t, 0, func() int {
				pvcs, err := pvcClient.List(ns.CTX, metav1.ListOptions{})
				require.NoError(t, err)

				return len(pvcs.Items)
			})

			expectedVolumeName := ""
			if useRetainStorageClass {
				// The volume should be retained, hence a b/s volume with the same name as the pvc should be found.
				expectedVolumeName = pvName

				t.Cleanup(func() {
					// delete the retained volume after the test
					bsVolList, err := egoClient.ListBlockStorageVolumes(ns.CTX)
					require.NoError(t, err)

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
				require.NoError(t, err)

				bsVol, _ := bsVolList.FindBlockStorageVolume(pvName)
				return bsVol.Name
			})
		}
	}

	t.Run("storage-class-delete", testFunc(false))
	t.Run("storage-class-retain", testFunc(true))
}

func TestDeleteVolumeNotFound(t *testing.T) {
	testName := "del-vol-not-found"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	egoClient, err := util.CreateEgoscaleClient(ns.CTX, v3.ZoneName(*flags.Zone))
	require.NoError(t, err)

	pvcName := generatePVCName(testName)
	ns.ApplyPVC(pvcName, "1Gi", false)
	pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)
	getPVC := func() *corev1.PersistentVolumeClaim {
		pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
		require.NoError(t, err)

		return pvc
	}

	awaitExpectation(t, corev1.ClaimBound, func() corev1.PersistentVolumeClaimPhase {
		pvc := getPVC()

		return pvc.Status.Phase
	})

	pvc := getPVC()
	require.NotNil(t, pvc)
	pvName := getPVC().Spec.VolumeName

	bsVolList, err := egoClient.ListBlockStorageVolumes(ns.CTX)
	require.NoError(t, err)

	bsVol, err := bsVolList.FindBlockStorageVolume(pvName)
	require.NoError(t, err)

	op, err := egoClient.DeleteBlockStorageVolume(ns.CTX, bsVol.ID)
	require.NoError(t, err)

	_, err = egoClient.Wait(ns.CTX, op, v3.OperationStateSuccess)
	require.NoError(t, err)

	err = pvcClient.Delete(ns.CTX, pvcName, metav1.DeleteOptions{})
	require.NoError(t, err)

	awaitExpectation(t, 0, func() int {
		pvcs, err := pvcClient.List(ns.CTX, metav1.ListOptions{})
		require.NoError(t, err)

		return len(pvcs.Items)
	})

	_, err = egoClient.GetBlockStorageVolume(ns.CTX, bsVol.ID)
	assert.ErrorIs(t, err, v3.ErrNotFound)
}

func TestVolumeExpand(t *testing.T) {
	testName := "expand-vol"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	pvcName := generatePVCName(testName)
	ns.ApplyPVC(pvcName, "1Gi", false)
	ns.Apply(fmt.Sprintf(basicDeployment, pvcName))

	awaitExpectation(t, "Bound", func() any {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, pvcName, metav1.GetOptions{})
		require.NoError(t, err)
		return pvc.Status.Phase
	})

	// Currently, resizing a block storage volume requires detaching it from the Compute Instance.
	// To achieve this detachment, we delete the deployment,
	// allowing the CSI to unmount and detach the volume from the node.
	ns.Delete(fmt.Sprintf(basicDeployment, pvcName))

	awaitExpectation(t, 0, func() any {
		pods, err := ns.K.ClientSet.CoreV1().Pods(ns.Name).List(ns.CTX, metav1.ListOptions{})
		require.NoError(t, err)

		return len(pods.Items)
	})

	_, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Patch(
		ns.CTX,
		pvcName,
		types.MergePatchType,
		[]byte(`{"spec":{"resources":{"requests":{"storage":"5Gi"}}}}`),
		metav1.PatchOptions{},
	)
	require.NoError(t, err)

	// Re-apply deployment after block storage resize.
	// CSI will resize volume filesystem on applying
	ns.Apply(fmt.Sprintf(basicDeployment, pvcName))

	awaitExpectation(t, 0, func() any {
		pvc, err := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name).Get(ns.CTX, pvcName, metav1.GetOptions{})
		require.NoError(t, err)

		return pvc.Status.Capacity.Storage().CmpInt64(5 * 1024 * 1024 * 1024)
	})
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
      storage: 10Gi
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
	ns.ApplyPVC(pvcName, "1Gi", false)

	pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

	awaitExpectation(t, corev1.ClaimBound, func() corev1.PersistentVolumeClaimPhase {
		pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
		require.NoError(t, err)

		return pvc.Status.Phase
	})

	// create snapshot
	ns.Apply(fmt.Sprintf(basicSnapshot, pvcName))

	snapshotClient := ns.K.DynamicClient.Resource(getSnapshotCRDResource()).Namespace(ns.Name)

	awaitExpectation(t, true, func() bool {
		crdInstance, err := snapshotClient.Get(ns.CTX, "my-snap-1", metav1.GetOptions{})
		if !assert.NoError(t, err) {
			return false
		}

		status, ok := crdInstance.Object["status"].(map[string]any)
		if !ok {
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
		require.NoError(t, err)

		return pvc.Status.Phase
	})

	// delete snapshot
	err := snapshotClient.Delete(ns.CTX, "my-snap-1", metav1.DeleteOptions{})
	require.NoError(t, err)

	awaitExpectation(t, 0, func() int {
		snapshots, err := snapshotClient.List(ns.CTX, metav1.ListOptions{})
		require.NoError(t, err)

		if err != nil {
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

func TestDrainNode(t *testing.T) {
	testName := "drain-node"
	ns := k8s.CreateTestNamespace(t, cluster.Get().K8s, testName)

	pvcName := generatePVCName(testName)
	ns.ApplyPVC(pvcName, "1Gi", false)
	ns.Apply(fmt.Sprintf(basicDeployment, pvcName))

	pvcClient := ns.K.ClientSet.CoreV1().PersistentVolumeClaims(ns.Name)

	awaitExpectation(t, corev1.ClaimBound, func() corev1.PersistentVolumeClaimPhase {
		pvc, err := pvcClient.Get(ns.CTX, pvcName, metav1.GetOptions{})
		require.NoError(t, err)

		return pvc.Status.Phase
	})

	awaitExpectation(t, "Running", func() any {
		pods, err := ns.K.ClientSet.CoreV1().Pods(ns.Name).List(ns.CTX, metav1.ListOptions{})
		require.NoError(t, err)

		if len(pods.Items) < 1 {
			return nil
		}

		return pods.Items[0].Status.Phase
	})

	// find the node our pod is on
	pods, err := ns.K.ClientSet.CoreV1().Pods(ns.Name).List(ns.CTX, metav1.ListOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, pods.Items)
	nodeName := pods.Items[0].Spec.NodeName

	// Taint the node with the cluster autoscaler taint
	taintKey := "ToBeDeletedByClusterAutoscaler"
	addTaint(t, ns.K.ClientSet.CoreV1().Nodes(), nodeName, taintKey)
	t.Cleanup(func() {
		removeTaint(t, ns.K.ClientSet.CoreV1().Nodes(), nodeName, taintKey)
	})

	awaitExpectation(t, 0, func() any {
		attachments, err := ns.K.ClientSet.StorageV1().VolumeAttachments().List(t.Context(), metav1.ListOptions{})
		require.NoError(t, err)

		if len(attachments.Items) == 0 {
			return 0
		}
		count := 0
		for _, attachment := range attachments.Items {
			if attachment.Spec.NodeName == nodeName {
				count++
			}
		}
		return count
	})
}

func hasTaint(t *testing.T, node *corev1.Node, taintKey string) bool {
	t.Helper()
	for _, taint := range node.Spec.Taints {
		if taint.Key == taintKey {
			return true
		}
	}
	return false
}

func addTaint(t *testing.T, client v1.NodeInterface, nodeName string, taintKey string) {
	t.Helper()
	node, err := client.Get(t.Context(), nodeName, metav1.GetOptions{})
	require.NoError(t, err)
	if hasTaint(t, node, taintKey) {
		return
	}
	node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
		Key:    taintKey,
		Value:  fmt.Sprint(time.Now().Unix()),
		Effect: "NoExecute",
	})
	// TODO: if there's a conflict on the node we need to retry this
	// Should not happen in our test cluster, but leaving a note if this test ends up being flaky
	_, err = client.Update(t.Context(), node, metav1.UpdateOptions{})
	require.NoError(t, err)
}

func removeTaint(t *testing.T, client v1.NodeInterface, nodeName string, taintKey string) {
	t.Helper()
	// We want to clean up even if the test context expired
	node, err := client.Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		t.Log("failed to obtain node to remove taint from node", nodeName, err)
		return
	}
	for i, taint := range node.Spec.Taints {
		if taint.Key == taintKey {
			if i+1 == len(node.Spec.Taints) {
				node.Spec.Taints = node.Spec.Taints[:i]
			} else {
				node.Spec.Taints = append(node.Spec.Taints[:i], node.Spec.Taints[i+1:]...)
			}
		}
	}
	// We always want to execute this, even if the test context has expired!
	// TODO: if there's a conflict on the node we need to retry this
	// Should not happen in our test cluster, but leaving a note if this test ends up being flaky
	if _, err := client.Update(context.Background(), node, metav1.UpdateOptions{}); err != nil {
		t.Log("failed to remove taint from node", nodeName, err)
	}
}
