package driver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/klog/v2"

	v3 "github.com/exoscale/egoscale/v3"
)

var (
	// controllerCapabilities represents the capabilities of the Exoscale Block Volumes
	controllerCapabilities = []csi.ControllerServiceCapability_RPC_Type{
		// This capability indicates the driver supports dynamic volume provisioning and deleting.
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		// This capability indicates the driver implements ControllerPublishVolume and ControllerUnpublishVolume.
		// Operations that correspond to the Kubernetes volume attach/detach operations.
		// This may, for example, result in a "volume attach" operation against the
		// Google Cloud control plane to attach the specified volume to the specified node
		// for the Google Cloud PD CSI Driver.
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		// Currently the only way to consume a snapshot is to create
		// a volume from it. Therefore plugins supporting
		// CREATE_DELETE_SNAPSHOT MUST support creating volume from
		// snapshot.
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,

		// Indicates the SP supports the
		// ListVolumesResponse.entry.published_node_ids field and the
		// ControllerGetVolumeResponse.published_node_ids field.
		// The SP MUST also support PUBLISH_UNPUBLISH_VOLUME.
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES_PUBLISHED_NODES,

		// Indicates the SP supports the ControllerGetVolume RPC.
		// This enables COs to, for example, fetch per volume
		// condition after a volume is provisioned.
		csi.ControllerServiceCapability_RPC_GET_VOLUME,
		// Indicates the SP supports the SINGLE_NODE_SINGLE_WRITER and/or
		// SINGLE_NODE_MULTI_WRITER access modes.
		// These access modes are intended to replace the
		// SINGLE_NODE_WRITER access mode to clarify the number of writers
		// for a volume on a single node. Plugins MUST accept and allow
		// use of the SINGLE_NODE_WRITER access mode when either
		// SINGLE_NODE_SINGLE_WRITER and/or SINGLE_NODE_MULTI_WRITER are
		// supported, in order to permit older COs to continue working.
		csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}

	// supportedAccessModes represents the supported access modes for the Exoscale Block Volumes
	supportedAccessModes = []csi.VolumeCapability_AccessMode{
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
		{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		},
	}

	exoscaleVolumeID   = DriverName + "/volume-id"
	exoscaleVolumeName = DriverName + "/volume-name"
	exoscaleVolumeZone = DriverName + "/volume-zone"
)

const (
	DefaultVolumeSizeGiB = 100
	MinimalVolumeSizeGiB = 10
	MaximumVolumeSizeGiB = 10000
)

type controllerService struct {
	client   *v3.Client
	zoneName v3.ZoneName
}

func newControllerService(client *v3.Client, nodeMeta *nodeMetadata) controllerService {
	return controllerService{
		client:   client,
		zoneName: nodeMeta.zoneName,
	}
}

// CreateVolume creates a new volume from CreateVolumeRequest with blockstorage ProvisionVolume.
// This function is idempotent.
func (d *controllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.V(4).Infof("CreateVolume")

	zoneName, err := getRequiredZone(req.GetAccessibilityRequirements(), d.zoneName)
	if err != nil {
		klog.Errorf("create block storage volume get required zone: %v", err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("create volume: new client zone: %v", err)
		return nil, err
	}

	resp, err := client.ListBlockStorageVolumes(ctx)
	if err != nil {
		klog.Errorf("create block storage volume list: %v", err)
		return nil, err
	}

	// Make the call idempotent since CreateBlockStorageVolume is not.
	for _, v := range resp.BlockStorageVolumes {
		if v.Name == req.Name {
			return &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					VolumeId:           exoscaleID(zoneName, v.ID),
					CapacityBytes:      convertGiBToBytes(v.Size),
					AccessibleTopology: newZoneTopology(zoneName),
				},
			}, nil
		}
	}

	// create the volume from a snapshot if a snapshot ID was provided.
	var snapshotTarget *v3.BlockStorageSnapshotTarget
	if req.GetVolumeContentSource() != nil {
		if _, ok := req.GetVolumeContentSource().GetType().(*csi.VolumeContentSource_Snapshot); !ok {
			return nil, status.Error(codes.InvalidArgument, "unsupported volumeContentSource type")
		}

		srcSnapshot := req.GetVolumeContentSource().GetSnapshot()
		if srcSnapshot == nil {
			return nil, status.Error(codes.Internal, "error retrieving snapshot from the volumeContentSource")
		}
		_, snapshotID, err := getExoscaleID(srcSnapshot.SnapshotId)
		if err != nil {
			klog.Errorf("create volume from snapshot: %v", err)
			return nil, err
		}

		snapshot, err := client.GetBlockStorageSnapshot(ctx, snapshotID)
		if err != nil {
			if errors.Is(err, v3.ErrNotFound) {
				klog.Errorf("create volume get snapshot not found: %v", err)
				return nil, status.Errorf(codes.NotFound, "snapshot %s not found", snapshotID)
			}
			klog.Errorf("create volume get snapshot: %v", err)

			return nil, err
		}

		snapshotTarget = &v3.BlockStorageSnapshotTarget{
			ID: snapshot.ID,
		}

		klog.Infof("creating volume from snapshot %q", snapshotTarget.ID.String())
	}

	var sizeInGiB int64 = DefaultVolumeSizeGiB
	if req.GetCapacityRange() != nil {
		requiredBytes := req.GetCapacityRange().RequiredBytes
		if requiredBytes%GiB != 0 {
			msg := fmt.Sprintf("requested size in bytes cannot be exactly converted to GiB: %d", requiredBytes)

			klog.Error(msg)

			return nil, fmt.Errorf(msg)
		}

		sizeInGiB = convertBytesToGiB(requiredBytes)
	}

	request := v3.CreateBlockStorageVolumeRequest{
		Name:                 req.Name,
		Size:                 sizeInGiB,
		BlockStorageSnapshot: snapshotTarget,
	}

	if err := client.Validate(request); err != nil {
		klog.Errorf("create block storage volume validation: %v", err)
		return nil, err
	}

	op, err := client.CreateBlockStorageVolume(ctx, request)
	if err != nil {
		klog.Errorf("create block storage volume: %v", err)
		return nil, err
	}

	opDone, err := client.Wait(ctx, op, v3.OperationStateSuccess)
	if err != nil {
		return nil, err
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           exoscaleID(zoneName, opDone.Reference.ID),
			CapacityBytes:      convertGiBToBytes(sizeInGiB),
			AccessibleTopology: newZoneTopology(zoneName),
			ContentSource:      req.GetVolumeContentSource(),
		},
	}, nil
}

// DeleteVolume detach and deprovision a volume.
// This operation MUST be idempotent.
func (d *controllerService) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.V(4).Infof("DeleteVolume")

	zoneName, volumeID, err := getExoscaleID(req.VolumeId)
	if err != nil {
		klog.Errorf("parse exoscale volume ID %s: %v", req.VolumeId, err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("delete volume: new client zone: %v", err)
		return nil, err
	}

	op, err := client.DeleteBlockStorageVolume(ctx, volumeID)
	if err != nil {
		if errors.Is(err, v3.ErrNotFound) {
			return &csi.DeleteVolumeResponse{}, nil
		}
		klog.Errorf("destroy block storage volume %s: %v", volumeID, err)
		return nil, err
	}

	_, err = client.Wait(ctx, op, v3.OperationStateSuccess)
	if err != nil {
		klog.Errorf("wait destroy block storage volume %s: %v", volumeID, err)
		return nil, err
	}

	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume should call ProvisionAndAttachVolume.
// This operation MUST be idempotent.
// Exoscale Attach
func (d *controllerService) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerPublishVolume")

	zoneName, instanceID, err := getExoscaleID(req.NodeId)
	if err != nil {
		klog.Errorf("parse node ID %s: %v", req.NodeId, err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("publish volume: new client zone: %v", err)
		return nil, err
	}

	_, volumeID, err := getExoscaleID(req.VolumeId)
	if err != nil {
		klog.Errorf("parse exoscale volume ID %s: %v", req.VolumeId, err)
		return nil, err
	}

	volume, err := client.GetBlockStorageVolume(ctx, volumeID)
	if err != nil {
		if errors.Is(err, v3.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
		}

		return nil, err
	}

	// PublishVolume idempotent
	if volume.Instance != nil {
		if volume.Instance.ID == instanceID {
			return &csi.ControllerPublishVolumeResponse{
				PublishContext: map[string]string{
					exoscaleVolumeName: volume.Name,
					exoscaleVolumeID:   volume.ID.String(),
					exoscaleVolumeZone: string(zoneName),
				},
			}, nil
		}
	}

	op, err := client.AttachBlockStorageVolumeToInstance(ctx, volumeID, v3.AttachBlockStorageVolumeToInstanceRequest{
		Instance: &v3.InstanceTarget{
			ID: instanceID,
		},
	})
	if err != nil {
		klog.Errorf("attach block storage volume %s to instance %s: %v", volumeID, instanceID, err)
		return nil, err
	}

	_, err = client.Wait(ctx, op, v3.OperationStateSuccess)
	if err != nil {
		klog.Errorf("wait attach block storage volume %s to instance %s: %v", volumeID, instanceID, err)
		return nil, err
	}

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			exoscaleVolumeName: volume.Name,
			exoscaleVolumeID:   volume.ID.String(),
			exoscaleVolumeZone: string(zoneName),
		},
	}, nil
}

// ControllerUnpublishVolume call blockstoraqge DetachAndDeprovisionVolume
// This operation MUST be idempotent.
// Exoscale Detach
func (d *controllerService) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	klog.V(4).Infof("ControllerUnpublishVolume")

	zoneName, volumeID, err := getExoscaleID(req.VolumeId)
	if err != nil {
		klog.Errorf("parse exoscale volume ID %s: %v", req.VolumeId, err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("unpublish volume: new client zone: %v", err)
		return nil, err
	}

	op, err := client.DetachBlockStorageVolume(ctx, volumeID)
	if err != nil {
		if errors.Is(err, v3.ErrNotFound) ||
			(errors.Is(err, v3.ErrInvalidRequest) && strings.Contains(err.Error(), "Volume not attached")) {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		klog.Errorf("detach block storage volume %s: %v", volumeID, err)
		return nil, err
	}

	_, err = client.Wait(ctx, op, v3.OperationStateSuccess)
	if err != nil {
		klog.Errorf("wait detach block storage volume %s: %v", volumeID, err)
		return nil, err
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities check if a pre-provisioned volume has all the capabilities
// that the CO wants.
// Get the volume info and check if it's match the CO needs.
// This operation MUST be idempotent.
func (d *controllerService) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	klog.V(4).Infof("ValidateVolumeCapabilities")

	zoneName, volumeID, err := getExoscaleID(req.VolumeId)
	if err != nil {
		klog.Errorf("parse exoscale ID %s: %v", req.VolumeId, err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("validate volume capabilities: new client zone: %v", err)
		return nil, err
	}

	_, err = client.GetBlockStorageVolume(ctx, volumeID)
	if err != nil {
		klog.Errorf("get block storage volume %s: %v", volumeID, err)
		return nil, err
	}

	volumeCapabilities := req.GetVolumeCapabilities()
	if volumeCapabilities == nil {
		klog.Errorf("volume capabilities %s not provided: %v", volumeID, err)
		return nil, status.Error(codes.InvalidArgument, "volumeCapabilities is not provided")
	}

	// TODO validate and return the right mode.
	// Since the only supported mode is one volume per instance,
	// let's use SINGLE_NODE_WRITER by default.

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessMode: &supportedAccessModes[0],
				},
			},
		},
	}, nil
}

// ListVolumes returns the list of requested volumes.
func (d *controllerService) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	klog.V(4).Infof("ListVolumes")
	var numberResults int
	var err error

	startingToken := req.GetStartingToken()
	if startingToken != "" {
		numberResults, err = strconv.Atoi(startingToken)
		if err != nil {
			return nil, status.Error(codes.Aborted, "invalid startingToken")
		}
	}

	zones, err := d.client.ListZones(ctx)
	if err != nil {
		klog.Errorf("create block storage volume list zones: %v", err)
		return nil, err
	}

	volumesEntries := []*csi.ListVolumesResponse_Entry{}
	for _, zone := range zones.Zones {
		client := d.client.WithEndpoint(zone.APIEndpoint)

		volumesResp, err := client.ListBlockStorageVolumes(ctx)
		if err != nil {
			// TODO: remove it when Block Storage is available in all zone.
			if strings.Contains(err.Error(), "Availability of the block storage volumes") {
				continue
			}
			klog.Errorf("list block storage volumes: %v", err)
			return nil, err
		}

		for _, v := range volumesResp.BlockStorageVolumes {
			var instancesID []string
			if v.Instance != nil && v.Instance.ID != "" {
				instancesID = append(instancesID, exoscaleID(zone.Name, v.Instance.ID))
			}

			volumesEntries = append(volumesEntries, &csi.ListVolumesResponse_Entry{
				Volume: &csi.Volume{
					VolumeId:           exoscaleID(zone.Name, v.ID),
					CapacityBytes:      convertGiBToBytes(v.Size),
					AccessibleTopology: newZoneTopology(zone.Name),
				},
				Status: &csi.ListVolumesResponse_VolumeStatus{
					PublishedNodeIds: instancesID,
				},
			})
		}
	}

	// Since MaxEntries is not optional,
	// To be compatible with the CO we fake a pagination here.
	nextPage := ""
	maxEntries := req.GetMaxEntries()
	if maxEntries == 0 {
		if numberResults != 0 {
			volumesEntries = volumesEntries[numberResults:]
		}
	} else {
		if int(maxEntries) > (len(volumesEntries) - numberResults) {
			volumesEntries = volumesEntries[numberResults:]
		} else {
			volumesEntries = volumesEntries[numberResults : numberResults+int(maxEntries)]
			nextPage = strconv.Itoa(numberResults + int(maxEntries))
		}
	}

	return &csi.ListVolumesResponse{
		Entries:   volumesEntries,
		NextToken: nextPage,
	}, nil
}

// GetCapacity returns the capacity of the "storage pool" from which the controller provisions volumes.
func (d *controllerService) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	klog.V(4).Infof("GetCapacity is not yet implemented")
	return nil, status.Error(codes.Unimplemented, "GetCapacity is not yet implemented")
}

// ControllerGetCapabilities returns  the supported capabilities of controller service provided by the Plugin.
func (d *controllerService) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.V(4).Infof("ControllerGetCapabilities")

	var capabilities []*csi.ControllerServiceCapability // nolint:prealloc
	for _, capability := range controllerCapabilities {
		capabilities = append(capabilities, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: capability,
				},
			},
		})
	}
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: capabilities}, nil
}

// CreateSnapshot call blockstorage SnapshotVolume.
func (d *controllerService) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	klog.V(4).Infof("CreateSnapshot")

	zoneName, volumeID, err := getExoscaleID(req.SourceVolumeId)
	if err != nil {
		klog.Errorf("parse exoscale ID %s: %v", req.SourceVolumeId, err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("create snapshot: new client zone: %v", err)
		return nil, err
	}

	volume, err := client.GetBlockStorageVolume(ctx, volumeID)
	if err != nil {
		klog.Errorf("create snapshot get volume %s: %v", volumeID, err)
	}

	for _, s := range volume.BlockStorageSnapshots {
		snapshot, err := client.GetBlockStorageSnapshot(ctx, s.ID)
		if err != nil {
			klog.Errorf("create snapshot get snapshot %s: %v", s.ID, err)
		}

		if snapshot.Name == req.Name {
			return &csi.CreateSnapshotResponse{
				Snapshot: &csi.Snapshot{
					SnapshotId:     exoscaleID(zoneName, snapshot.ID),
					SourceVolumeId: exoscaleID(zoneName, volume.ID),
					CreationTime:   timestamppb.New(snapshot.CreatedAT),
					ReadyToUse:     true,
					// We leave the optional SizeBytes field unset as the size of a block storage snapshot is the size of the difference to the volume or previous snapshots, k8s however expects the size to be the size of the restored volume.
				},
			}, nil
		}
	}

	op, err := client.CreateBlockStorageSnapshot(ctx, volume.ID, v3.CreateBlockStorageSnapshotRequest{
		Name: req.Name,
	})
	if err != nil {
		klog.Errorf("create block storage volume %s snapshot: %v", volume.ID, err)
		return nil, err
	}
	op, err = client.Wait(ctx, op, v3.OperationStateSuccess)
	if err != nil {
		klog.Errorf("wait create block storage volume %s snapshot: %v", volume.ID, err)
		return nil, err
	}

	if op.Reference == nil {
		klog.Errorf("operation reference is nil")
		return nil, fmt.Errorf("operation reference: %v not found", op.ID)
	}

	snapshot, err := client.GetBlockStorageSnapshot(ctx, op.Reference.ID)
	if err != nil {
		klog.Errorf("get block storage volume snapshot %s: %v", op.Reference.ID, err)
		return nil, err
	}

	klog.Infof("successfully created snapshot %q of size %d GiB from volume %q", snapshot.ID, volume.Size, volume.ID)

	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     exoscaleID(zoneName, snapshot.ID),
			SourceVolumeId: exoscaleID(zoneName, volume.ID),
			CreationTime:   timestamppb.New(snapshot.CreatedAT),
			ReadyToUse:     true,
			// We leave the optional SizeBytes field unset as the size of a block storage snapshot is the size of the difference to the volume or previous snapshots, k8s however expects the size to be the size of the restored volume.
		},
	}, nil
}

// DeleteSnapshot destroys a block storage volume snapshot.
func (d *controllerService) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	klog.V(4).Infof("DeleteSnapshot")

	zoneName, snapshotID, err := getExoscaleID(req.SnapshotId)
	if err != nil {
		klog.Errorf("parse exoscale snapshot ID %s: %v", req.SnapshotId, err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("delete snapshot: new client zone: %v", err)
		return nil, err
	}

	op, err := client.DeleteBlockStorageSnapshot(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, v3.ErrNotFound) {
			return &csi.DeleteSnapshotResponse{}, nil
		}
		return nil, err
	}

	if _, err := client.Wait(ctx, op, v3.OperationStateSuccess); err != nil {
		return nil, err
	}

	return &csi.DeleteSnapshotResponse{}, nil
}

// ListSnapshots lists block storage volume snapshot.
func (d *controllerService) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(4).Infof("ListSnapshots")
	var numberResults int
	var err error

	startingToken := req.GetStartingToken()
	if startingToken != "" {
		numberResults, err = strconv.Atoi(startingToken)
		if err != nil {
			return nil, status.Error(codes.Aborted, "invalid startingToken")
		}
	}

	zones, err := d.client.ListZones(ctx)
	if err != nil {
		klog.Errorf("create block storage volume list zones: %v", err)
		return nil, err
	}

	snapshotsEntries := []*csi.ListSnapshotsResponse_Entry{}
	for _, zone := range zones.Zones {
		client := d.client.WithEndpoint(zone.APIEndpoint)

		snapResp, err := client.ListBlockStorageSnapshots(ctx)
		if err != nil {
			// TODO: remove it when Block Storage is available in all zone.
			if strings.Contains(err.Error(), "Availability of the block storage volumes") {
				continue
			}
			klog.Errorf("list block storage snapshot: %v", err)
			return nil, err
		}

		for _, s := range snapResp.BlockStorageSnapshots {
			snapshotsEntries = append(snapshotsEntries, &csi.ListSnapshotsResponse_Entry{
				Snapshot: &csi.Snapshot{
					SourceVolumeId: exoscaleID(zone.Name, s.BlockStorageVolume.ID),
					SnapshotId:     exoscaleID(zone.Name, s.ID),
					CreationTime:   timestamppb.New(s.CreatedAT),
					ReadyToUse:     true,
					// We leave the optional SizeBytes field unset as the size of a block storage snapshot is the size of the difference to the volume or previous snapshots, k8s however expects the size to be the size of the restored volume.
				},
			})
		}
	}

	// Since MaxEntries is not optional,
	// To be compatible with the CO we fake a pagination here.
	nextPage := ""
	maxEntries := req.GetMaxEntries()
	if maxEntries == 0 {
		if numberResults != 0 {
			snapshotsEntries = snapshotsEntries[numberResults:]
		}
	} else {
		if int(maxEntries) > (len(snapshotsEntries) - numberResults) {
			snapshotsEntries = snapshotsEntries[numberResults:]
		} else {
			snapshotsEntries = snapshotsEntries[numberResults : numberResults+int(maxEntries)]
			nextPage = strconv.Itoa(numberResults + int(maxEntries))
		}
	}

	return &csi.ListSnapshotsResponse{
		Entries:   snapshotsEntries,
		NextToken: nextPage,
	}, nil
}

// ControllerExpandVolume resizes Block Storage volume.
func (d *controllerService) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	klog.V(4).Infof("ControllerExpandVolume")
	zoneName, volumeID, err := getExoscaleID(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("expand volume: new client zone: %v", err)
		return nil, err
	}

	_, err = client.GetBlockStorageVolume(ctx, volumeID)
	if err != nil {
		if errors.Is(err, v3.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
		}

		return nil, err
	}

	nodeExpansionRequired := true
	volumeCapability := req.GetVolumeCapability()
	if volumeCapability != nil {
		err := validateVolumeCapability(volumeCapability)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "volumeCapabilities not supported: %s", err)
		}

		if _, ok := volumeCapability.GetAccessType().(*csi.VolumeCapability_Block); ok {
			nodeExpansionRequired = false
		}
	}

	newSizeInBytes, err := getNewVolumeSize(req.GetCapacityRange())
	if err != nil {
		return nil, status.Errorf(codes.OutOfRange, "invalid capacity range: %v", err)
	}

	if newSizeInBytes%GiB != 0 {
		msg := fmt.Sprintf("requested size in bytes cannot be exactly converted to GiB: %d", newSizeInBytes)

		klog.Error(msg)

		return nil, fmt.Errorf(msg)
	}

	sizeInGiB := convertBytesToGiB(newSizeInBytes)

	_, err = client.ResizeBlockStorageVolume(ctx, volumeID, v3.ResizeBlockStorageVolumeRequest{
		Size: sizeInGiB,
	})
	if err != nil {
		return nil, err
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         newSizeInBytes,
		NodeExpansionRequired: nodeExpansionRequired,
	}, nil
}

// ControllerGetVolume gets a volume and  return it.
func (d *controllerService) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	zoneName, volumeID, err := getExoscaleID(req.VolumeId)
	if err != nil {
		klog.Errorf("parse exoscale ID %s: %v", req.VolumeId, err)
		return nil, err
	}

	client, err := newClientZone(ctx, d.client, zoneName)
	if err != nil {
		klog.Errorf("expand volume: new client zone: %v", err)
		return nil, err
	}

	volume, err := client.GetBlockStorageVolume(ctx, volumeID)
	if err != nil {
		if errors.Is(err, v3.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
		}

		klog.Errorf("get block storage volume controller %s: %v", volumeID, err)
		return nil, err
	}

	var instancesID []string
	if volume.Instance != nil && volume.Instance.ID != "" {
		instancesID = append(instancesID, exoscaleID(zoneName, volume.Instance.ID))
	}

	return &csi.ControllerGetVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      exoscaleID(zoneName, volume.ID),
			CapacityBytes: convertGiBToBytes(volume.Size),
		},
		Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
			PublishedNodeIds: instancesID,
		},
	}, nil
}

func newClientZone(ctx context.Context, c *v3.Client, z v3.ZoneName) (*v3.Client, error) {
	endpoint, err := c.GetZoneAPIEndpoint(ctx, z)
	if err != nil {
		return nil, fmt.Errorf("get zone api endpoint: %w", err)
	}

	return c.WithEndpoint(endpoint), nil
}
