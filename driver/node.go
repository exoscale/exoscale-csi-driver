package driver

import (
	"context"
	"os"
	"strings"

	v3 "github.com/exoscale/egoscale/v3"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

type nodeService struct {
	nodeID    v3.UUID
	zoneName  v3.ZoneName
	diskUtils *diskUtils
}

func newNodeService(meta *nodeMetadata) nodeService {
	return nodeService{
		nodeID:    meta.InstanceID,
		zoneName:  meta.zoneName,
		diskUtils: newDiskUtils(),
	}
}

// NodeStageVolume prepare the physical volume to be ready.
// format, mkfs...etc.
func (d *nodeService) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	klog.V(4).Infof("NodeStageVolume, %#v", req)
	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "stagingTargetPath not provided")
	}
	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability not provided")
	}

	if err := validateVolumeCapability(volumeCapability); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volume %s capability not supported", req.VolumeId)
	}

	_, volumeID, err := getExoscaleID(req.VolumeId)
	if err != nil {
		klog.Errorf("parse exoscale volume ID %s: %v", req.VolumeId, err)
		return nil, err
	}

	devicePath, err := d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume %s is not mounted on node", volumeID)
		}

		return nil, status.Errorf(codes.Internal, "get device path for volume %s: %s", volumeID, err.Error())
	}

	klog.V(4).Infof("volume %s has device path %s", volumeID, devicePath)

	// no need to mount if it's in block mode
	if _, ok := volumeCapability.GetAccessType().(*csi.VolumeCapability_Block); ok {
		return &csi.NodeStageVolumeResponse{}, nil
	}

	isMounted, err := d.diskUtils.IsSharedMounted(stagingTargetPath, devicePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "checking mount point of volume %s on path %s: %s", volumeID, stagingTargetPath, err.Error())
	}

	if isMounted {
		blockDevice, err := d.diskUtils.IsBlockDevice(stagingTargetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error checking stat for %s: %s", stagingTargetPath, err.Error())
		}
		if blockDevice {
			return nil, status.Errorf(codes.Unknown, "block device mounted as stagingTargetPath %s for volume %s", stagingTargetPath, volumeID)
		}
		klog.V(4).Infof("volume %s is already mounted on %s", volumeID, stagingTargetPath)
		return &csi.NodeStageVolumeResponse{}, nil
	}

	mountCap := volumeCapability.GetMount()
	if mountCap == nil {
		return nil, status.Error(codes.InvalidArgument, "mount volume capability is nil")
	}

	mountOptions := mountCap.GetMountFlags()
	fsType := mountCap.GetFsType()

	klog.V(4).Infof("Volume %s will be mounted on %s with type %s and options %s", volumeID, stagingTargetPath, fsType, strings.Join(mountOptions, ","))

	err = d.diskUtils.FormatAndMount(stagingTargetPath, devicePath, fsType, mountOptions)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "format and mount device from (%q) to (%q) with fstype (%q) and options (%q): %v",
			devicePath, stagingTargetPath, fsType, mountOptions, err)
	}
	klog.V(4).Infof("Volume %s has been mounted on %s with type %s and options %s", volumeID, stagingTargetPath, fsType, strings.Join(mountOptions, ","))

	return &csi.NodeStageVolumeResponse{}, nil
}

// Specific fs cleanup or close like luks close...etc.
func (d *nodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	klog.V(4).Infof("NodeUnstageVolume")
	_, volumeID, err := getExoscaleID(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "stagingTargetPath not provided")
	}

	_, err = d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if os.IsNotExist(err) {
			// Volume not found ignore and return success.
			return &csi.NodeUnstageVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume %s: %s", volumeID, err.Error())
	}

	if _, err := os.Stat(stagingTargetPath); os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "volume %s not found on node", volumeID)
	}

	isMounted, err := d.diskUtils.IsSharedMounted(stagingTargetPath, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking if target is mounted: %s", err.Error())
	}

	if isMounted {
		klog.V(4).Infof("Volume with ID %s is mounted on %s, umounting it", volumeID, stagingTargetPath)
		err = d.diskUtils.Unmount(stagingTargetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error unmounting target path: %s", err.Error())
		}
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// Mounting volume in right path...etc.
func (d *nodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) { // nolint:gocyclo
	klog.V(4).Infof("NodePublishVolume")
	_, volumeID, err := getExoscaleID(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "targetPath not provided")
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volumeCapability not provided")
	}

	err = validateVolumeCapability(volumeCapability)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if stagingTargetPath == "" {
		return nil, status.Error(codes.FailedPrecondition, "stagingTargetPath not provided")
	}

	devicePath, err := d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "volume %s not found: %s", volumeID, err.Error())
	}

	isMounted, err := d.diskUtils.IsSharedMounted(targetPath, devicePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking mount point of volume %s on path %s: %v", volumeID, stagingTargetPath, err)
	}

	if isMounted {
		blockDevice, err := d.diskUtils.IsBlockDevice(targetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error checking stat for %s: %s", targetPath, err.Error())
		}
		if blockDevice && volumeCapability.GetMount() != nil || !blockDevice && volumeCapability.GetBlock() != nil {
			return nil, status.Error(codes.AlreadyExists, "cannot change volumeCapability type")
		}

		if volumeCapability.GetBlock() != nil {
			// unix specific, will error if not unix
			fd, err := unix.Openat(unix.AT_FDCWD, devicePath, unix.O_RDONLY, uint32(0))
			defer unix.Close(fd)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "error opening block device %s: %s", devicePath, err.Error())
			}
			ro, err := unix.IoctlGetInt(fd, unix.BLKROGET)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "error getting BLKROGET for block device %s: %s", devicePath, err.Error())
			}

			if (ro == 1) == req.GetReadonly() {
				klog.V(4).Infof("Volume %s is already mounted as a raw device on %s", volumeID, targetPath)
				return &csi.NodePublishVolumeResponse{}, nil
			}
			return nil, status.Errorf(codes.AlreadyExists, "volume %s does not match the given mount mode for the request", volumeID)
		}

		mountInfo, err := d.diskUtils.GetMountInfo(targetPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "error getting mount information of path %s: %s", targetPath, err.Error())
		}

		isReadOnly := false
		if mountInfo != nil {
			for _, opt := range mountInfo.mountOptions {
				if opt == "rw" {
					break
				} else if opt == "ro" {
					isReadOnly = true
					break
				}
			}
		}

		if isReadOnly != req.GetReadonly() {
			return nil, status.Errorf(codes.AlreadyExists, "volume with ID %s does not match the given mount mode for the request", volumeID)
		}

		klog.V(4).Infof("Volume %s is already mounted on %s", volumeID, stagingTargetPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	var sourcePath string
	var fsType string
	var mountOptions []string
	mount := volumeCapability.GetMount()
	if mount == nil {
		if volumeCapability.GetBlock() != nil {
			sourcePath = devicePath
			if req.GetReadonly() {
				fd, err := unix.Openat(unix.AT_FDCWD, devicePath, unix.O_RDONLY, uint32(0))
				if err != nil {
					return nil, status.Errorf(codes.Internal, "error opening block device %s: %s", devicePath, err.Error())
				}
				err = unix.IoctlSetPointerInt(fd, unix.BLKROSET, 1)
				unix.Close(fd)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "error setting BLKROSET for block device %s: %s", devicePath, err.Error())
				}
			}
		}
	} else {
		sourcePath = stagingTargetPath
		fsType = mount.GetFsType()
		mountOptions = mount.GetMountFlags()
	}

	mountOptions = append(mountOptions, "bind")

	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	err = createMountPoint(targetPath, volumeCapability.GetBlock() != nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error creating mount point %s for volume with ID %s", targetPath, volumeID)
	}

	err = d.diskUtils.MountToTarget(sourcePath, targetPath, fsType, mountOptions)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error mounting source %s to target %s with fs of type %s : %s", sourcePath, targetPath, fsType, err.Error())
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

// Unmounting volume.
func (d *nodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.V(4).Infof("NodeUnpublishVolume")
	targetPath := req.GetTargetPath()
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "targetPath not provided")
	}

	err := d.diskUtils.Unmount(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error unmounting target path: %s", err.Error())
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats returns the volume capacity statistics available for the volume
func (d *nodeService) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(4).Infof("NodeGetVolumeStats")
	_, volumeID, err := getExoscaleID(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	volumePath := req.GetVolumePath()
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volumePath not provided")
	}

	stagingPath := req.GetStagingTargetPath()
	if stagingPath != "" {
		volumePath = stagingPath
	}

	isMounted, err := d.diskUtils.IsSharedMounted(volumePath, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking mount point of path %s for volume %s: %s", volumePath, volumeID, err.Error())
	}

	if !isMounted {
		return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
	}

	_, err = d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume with ID %s not found", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "error getting device path for volume with ID %s: %s", volumeID, err.Error())
	}

	fs, err := d.diskUtils.GetStatfs(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error doing stat on %s: %s", volumePath, err.Error())
	}

	totalBytes := fs.Blocks * uint64(fs.Bsize)
	availableBytes := fs.Bfree * uint64(fs.Bsize)
	usedBytes := totalBytes - availableBytes

	totalInodes := fs.Files
	freeInodes := fs.Ffree
	usedInodes := totalInodes - freeInodes

	diskUsage := &csi.VolumeUsage{
		Unit:      csi.VolumeUsage_BYTES,
		Total:     int64(totalBytes),
		Available: int64(availableBytes),
		Used:      int64(usedBytes),
	}

	inodesUsage := &csi.VolumeUsage{
		Unit:      csi.VolumeUsage_INODES,
		Total:     int64(totalInodes),
		Available: int64(freeInodes),
		Used:      int64(usedInodes),
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			diskUsage,
			inodesUsage,
		},
	}, nil
}

// NodeGetCapabilities allows the CO to check the supported capabilities of node service provided by the Plugin.
func (d *nodeService) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(4).Infof("NodeGetCapabilities")
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
					},
				},
			},
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
					},
				},
			},
		},
	}, nil
}

// NodeGetInfo returns inqformation about node's volumes
func (d *nodeService) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	klog.V(4).Infof("NodeGetInfo")
	return &csi.NodeGetInfoResponse{
		// Store the zone and the instanceID to let the CSI controller know the zone of the node.
		NodeId: exoscaleID(d.zoneName, d.nodeID),
		// newZoneTopology returns always len(1).
		AccessibleTopology: newZoneTopology(d.zoneName)[0],
	}, nil
}

// NodeExpandVolume expands the given volume, mkfs, resize...etc
// not supported yet at Exoscale Public API yet.
func (d *nodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	klog.V(4).Infof("NodeExpandVolume")
	_, volumeID, err := getExoscaleID(req.GetVolumeId())
	if err != nil {
		return nil, err
	}

	volumePath := req.GetVolumePath()
	if volumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volumePath not provided")
	}

	devicePath, err := d.diskUtils.GetDevicePath(volumeID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume %s is not mounted on node", volumeID)
		}
		return nil, status.Errorf(codes.Internal, "failed to get device path for volume %s: %v", volumeID, err)
	}

	isBlock, err := d.diskUtils.IsBlockDevice(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error checking stat for %s: %s", devicePath, err.Error())
	}

	volumeCapability := req.GetVolumeCapability()
	if volumeCapability != nil {
		err = validateVolumeCapability(volumeCapability)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "volumeCapability not supported: %s", err)
		}

		if _, ok := volumeCapability.GetAccessType().(*csi.VolumeCapability_Block); ok {
			isBlock = true
		}
	}

	// no need to resize if it's in block mode
	if isBlock {
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	klog.V(4).Infof("resizing volume %s mounted on %s", volumeID, volumePath)

	if err = d.diskUtils.Resize(volumePath, devicePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resize volume %s mounted on %s: %v", volumeID, volumePath, err)
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}
