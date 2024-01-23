package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"

	v3 "github.com/exoscale/egoscale/v3"
)

func exoscaleID(zone v3.URL, id v3.UUID) string {
	z, _ := zone.Zone()
	return fmt.Sprintf("%s/%s", z, id)
}

func getExoscaleID(exoID string) (v3.URL, v3.UUID, error) {
	s := strings.Split(exoID, "/")
	if len(s) != 2 {
		return "", "", fmt.Errorf("malformed exoscale id")
	}

	id, err := v3.ParseUUID(s[1])
	if err != nil {
		return "", "", err
	}

	zone, ok := v3.Zones[s[0]]
	if !ok {
		return "", "", fmt.Errorf("invalid zone name: %s", s[0])
	}

	return zone, id, nil
}

func newZoneTopology(zone v3.URL) []*csi.Topology {
	z, _ := zone.Zone()
	return []*csi.Topology{
		{
			Segments: map[string]string{ZoneTopologyKey: z},
		},
	}
}

func validateVolumeCapability(volumeCapability *csi.VolumeCapability) error {
	if volumeCapability == nil {
		return fmt.Errorf("volumeCapability is nil")
	}
	for _, accessMode := range supportedAccessModes {
		if accessMode.Mode == volumeCapability.GetAccessMode().GetMode() {
			return nil
		}
	}

	mount := volumeCapability.GetMount() != nil
	block := volumeCapability.GetBlock() != nil
	if mount && block {
		panic("TODO check mount && block")
	}

	return fmt.Errorf("access mode not supported")
}

func createMountPoint(path string, file bool) error {
	_, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	if file {
		dir := filepath.Dir(path)
		err := os.MkdirAll(dir, os.FileMode(0755))
		if err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_CREATE, os.FileMode(0644))
		if err != nil {
			return err
		}
		defer file.Close()
	} else {
		err := os.MkdirAll(path, os.FileMode(0755))
		if err != nil {
			return err
		}
	}
	return nil
}

func convertBytesToGibiBytes(nBytes int64) int64 {
	return nBytes / (1024 * 1024 * 1024)
}

func getNewVolumeSize(capacityRange *csi.CapacityRange) (int64, error) {
	if capacityRange == nil {
		return MinimalVolumeSizeBytes, nil
	}

	requiredBytes := capacityRange.GetRequiredBytes()
	requiredSet := requiredBytes > 0

	limitBytes := capacityRange.GetLimitBytes()
	limitSet := limitBytes > 0

	if !requiredSet && !limitSet {
		return MinimalVolumeSizeBytes, nil
	}

	if requiredSet && limitSet && limitBytes < requiredBytes {
		return 0, errLimitLessThanRequiredBytes
	}

	if requiredSet && !limitSet && requiredBytes < MinimalVolumeSizeBytes {
		return 0, errRequiredBytesLessThanMinimun
	}

	if limitSet && limitBytes < MinimalVolumeSizeBytes {
		return 0, errLimitLessThanMinimum
	}

	if requiredSet && requiredBytes > MaximumVolumeSizeBytes {
		return 0, errRequiredBytesGreaterThanMaximun
	}

	if !requiredSet && limitSet && limitBytes > MaximumVolumeSizeBytes {
		return 0, errLimitGreaterThanMaximum
	}

	if requiredSet && limitSet && requiredBytes == limitBytes {
		return requiredBytes, nil
	}

	if requiredSet {
		return requiredBytes, nil
	}

	if limitSet {
		return limitBytes, nil
	}

	return MinimalVolumeSizeBytes, nil
}
