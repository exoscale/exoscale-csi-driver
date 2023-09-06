package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	v3 "github.com/exoscale/egoscale/v3"

	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
	kmount "k8s.io/mount-utils"
	kexec "k8s.io/utils/exec"
	kio "k8s.io/utils/io"
)

const (
	devDiskByID     = "/dev/disk/by-id"
	devDiskPrefix   = "virtio-"
	devDiskIDLength = 20

	defaultFSType = "ext4"

	procMountInfoMaxListTries             = 3
	procMountsExpectedNumFieldsPerLine    = 6
	procMountInfoExpectedAtLeastNumFields = 10
	procMountsPath                        = "/proc/mounts"
	procMountInfoPath                     = "/proc/self/mountinfo"
	expectedAtLeastNumFieldsPerMountInfo  = 10
)

type DiskUtils interface {
	// GetDevicePath returns the path for the specified volumeID
	GetDevicePath(volumeID string) (string, error)
	FormatAndMount(targetPath string, devicePath string, fsType string, mountOptions []string) error
	IsSharedMounted(targetPath string, devicePath string) (bool, error)
	GetMountInfo(targetPath string) (*mountInfo, error)
	IsBlockDevice(path string) (bool, error)
	MountToTarget(sourcePath, targetPath, fsType string, mountOptions []string) error
	Unmount(target string) error
	GetStatfs(path string) (*unix.Statfs_t, error)
	Resize(targetPath string, devicePath string) error
}

type diskUtils struct {
	kMounter *kmount.SafeFormatAndMount
}

func newDiskUtils() *diskUtils {
	return &diskUtils{
		kMounter: &kmount.SafeFormatAndMount{
			Interface: kmount.New(""),
			Exec:      kexec.New(),
		},
	}
}

// taken from https://github.com/kubernetes/kubernetes/blob/master/pkg/util/mount/mount_linux.go
// This represents a single line in /proc/<pid>/mountinfo.
type mountInfo struct {
	// Unique ID for the mount (maybe reused after umount).
	id int
	// The ID of the parent mount (or of self for the root of this mount namespace's mount tree).
	parentID int
	// The value of `st_dev` for files on this filesystem.
	majorMinor string
	// The pathname of the directory in the filesystem which forms the root of this mount.
	root string
	// Mount source, filesystem-specific information. e.g. device, tmpfs name.
	source string
	// Mount point, the pathname of the mount point.
	mountPoint string
	// Optional fieds, zero or more fields of the form "tag[:value]".
	optionalFields []string
	// The filesystem type in the form "type[.subtype]".
	fsType string
	// Per-mount options.
	mountOptions []string
	// Per-superblock options.
	superOptions []string
}

// taken from https://github.com/kubernetes/kubernetes/blob/master/pkg/util/mount/mount_linux.go
func (d *diskUtils) GetMountInfo(targetPath string) (*mountInfo, error) {
	content, err := kio.ConsistentRead(procMountInfoPath, procMountInfoMaxListTries)
	if err != nil {
		return &mountInfo{}, err
	}
	contentStr := string(content)

	for _, line := range strings.Split(contentStr, "\n") {
		if line == "" {
			// the last split() item is empty string following the last \n
			continue
		}
		// See `man proc` for authoritative description of format of the file.
		fields := strings.Fields(line)
		if len(fields) < expectedAtLeastNumFieldsPerMountInfo {
			return nil, fmt.Errorf("wrong number of fields in (expected at least %d, got %d): %s", expectedAtLeastNumFieldsPerMountInfo, len(fields), line)
		}
		if fields[4] != targetPath {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, err
		}
		parentID, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, err
		}
		info := &mountInfo{
			id:           id,
			parentID:     parentID,
			majorMinor:   fields[2],
			root:         fields[3],
			mountPoint:   fields[4],
			mountOptions: strings.Split(fields[5], ","),
		}
		// All fields until "-" are "optional fields".
		i := 6
		for ; i < len(fields) && fields[i] != "-"; i++ {
			info.optionalFields = append(info.optionalFields, fields[i])
		}
		// Parse the rest 3 fields.
		i++
		if len(fields)-i < 3 {
			return nil, fmt.Errorf("expect 3 fields in %s, got %d", line, len(fields)-i)
		}
		info.fsType = fields[i]
		info.source = fields[i+1]
		info.superOptions = strings.Split(fields[i+2], ",")
		return info, nil
	}
	return nil, nil
}

func (d *diskUtils) GetDevicePath(volumeID v3.UUID) (string, error) {
	devDiskID := volumeID.String()[:devDiskIDLength]
	devicePath := path.Join(devDiskByID, devDiskPrefix+devDiskID)
	realDevicePath, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		return "", err
	}

	deviceInfo, err := os.Stat(realDevicePath)
	if err != nil {
		return "", err
	}

	deviceMode := deviceInfo.Mode()
	if os.ModeDevice != deviceMode&os.ModeDevice || os.ModeCharDevice == deviceMode&os.ModeCharDevice {
		return "", fmt.Errorf("device path is not device")
	}

	return devicePath, nil
}

func (d *diskUtils) FormatAndMount(targetPath string, devicePath string, fsType string, mountOptions []string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	klog.V(4).Infof("Attempting to mount %s on %s with type %s", devicePath, targetPath, fsType)

	if err := d.kMounter.FormatAndMount(devicePath, targetPath, fsType, mountOptions); err != nil {
		return fmt.Errorf("failed to optionnaly format and mount: %w", err)
	}

	return nil
}

func (d *diskUtils) IsSharedMounted(targetPath string, devicePath string) (bool, error) {
	if targetPath == "" {
		return false, fmt.Errorf("target path empty")
	}

	mountInfo, err := d.GetMountInfo(targetPath)
	if err != nil {
		return false, err
	}

	if mountInfo == nil {
		return false, nil
	}

	sharedMounted := false
	for _, optionalField := range mountInfo.optionalFields {
		tag := strings.Split(optionalField, ":")
		if len(tag) != 0 && tag[0] == "shared" {
			sharedMounted = true
		}
	}
	if !sharedMounted {
		return false, fmt.Errorf("target not shared mounter")
	}

	if devicePath != "" && mountInfo.source != devicePath {
		return false, fmt.Errorf("target not mounter on right device")
	}

	return true, nil
}

func (d *diskUtils) IsBlockDevice(path string) (bool, error) {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}

	deviceInfo, err := os.Stat(realPath)
	if err != nil {
		return false, err
	}

	deviceMode := deviceInfo.Mode()
	if os.ModeDevice != deviceMode&os.ModeDevice || os.ModeCharDevice == deviceMode&os.ModeCharDevice {
		return false, nil
	}

	return true, nil

}

func (d *diskUtils) MountToTarget(sourcePath, targetPath, fsType string, mountOptions []string) error {
	if fsType == "" {
		fsType = defaultFSType
	}

	if err := d.kMounter.Mount(sourcePath, targetPath, fsType, mountOptions); err != nil {
		return err
	}

	return nil
}

func (d *diskUtils) Unmount(target string) error {
	return kmount.CleanupMountPoint(target, d.kMounter, true)
}

func (d *diskUtils) GetStatfs(path string) (*unix.Statfs_t, error) {
	fs := &unix.Statfs_t{}
	err := unix.Statfs(path, fs)
	return fs, err
}

func (d *diskUtils) Resize(targetPath string, devicePath string) error {
	mountInfo, err := d.GetMountInfo(targetPath)
	if err != nil {
		return err
	}

	klog.V(4).Infof("resizing filesystem %s on %s", mountInfo.fsType, devicePath)

	switch mountInfo.fsType {
	case "ext3", "ext4":
		resize2fsPath, err := exec.LookPath("resize2fs")
		if err != nil {
			return err
		}
		resize2fsArgs := []string{devicePath}
		return exec.Command(resize2fsPath, resize2fsArgs...).Run()
	case "xfs":
		xfsGrowfsPath, err := exec.LookPath("xfs_growfs")
		if err != nil {
			return err
		}
		xfsGrowfsArgs := []string{"-d", targetPath}
		return exec.Command(xfsGrowfsPath, xfsGrowfsArgs...).Run()
	case "btrfs":
		btrfsPath, err := exec.LookPath("btrfs")
		if err != nil {
			return err
		}
		btrfsArgs := []string{"filesystem", "resize", "-max", targetPath}
		return exec.Command(btrfsPath, btrfsArgs...).Run()
	}

	return fmt.Errorf("filesystem %s does not support resizing", mountInfo.fsType)
}
