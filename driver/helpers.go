package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"

	v3 "github.com/exoscale/egoscale/v3"
)

func exoscaleID(zoneName v3.ZoneName, id v3.UUID) string {
	return fmt.Sprintf("%s/%s", zoneName, id)
}

func getExoscaleID(exoID string) (v3.ZoneName, v3.UUID, error) {
	s := strings.Split(exoID, "/")
	if len(s) != 2 {
		return "", "", fmt.Errorf("malformed exoscale id")
	}

	id, err := v3.ParseUUID(s[1])
	if err != nil {
		return "", "", err
	}

	return v3.ZoneName(s[0]), id, nil
}

func newZoneTopology(zoneName v3.ZoneName) []*csi.Topology {
	return []*csi.Topology{
		{
			Segments: map[string]string{ZoneTopologyKey: string(zoneName)},
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

func getRequiredZone(requirements *csi.TopologyRequirement, defaultZone v3.ZoneName) (v3.ZoneName, error) {
	if requirements == nil {
		return defaultZone, nil
	}

	if requirements.GetRequisite() == nil {
		return defaultZone, nil
	}

	// Since volume can only be handle by one zone
	// and volumes/nodes are announced with only one zone accessible topology,
	// TopologyRequirement will always ask for one zone at a time.

	if len(requirements.GetRequisite()) != 1 {
		return "", fmt.Errorf("topology requisite must always be equal to one zone")
	}

	required := requirements.GetRequisite()[0]

	if len(required.Segments) != 1 {
		return "", fmt.Errorf("topology requisite segments must always be equal to one zone")
	}

	zone, ok := required.Segments[ZoneTopologyKey]
	if !ok {
		return "", fmt.Errorf("zone topology key %s not found", ZoneTopologyKey)
	}

	return v3.ZoneName(zone), nil
}
