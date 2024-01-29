package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"

	v3 "github.com/exoscale/egoscale/v3"
)

// TODO (pej) multizone: Add v3.URL back once url environment are fixed.
func exoscaleID(zoneName string, id v3.UUID) string {
	return fmt.Sprintf("%s/%s", zoneName, id)
}

// TODO (pej) multizone: Add v3.URL back once url environment are fixed.
func getExoscaleID(exoID string) (string, v3.UUID, error) {
	s := strings.Split(exoID, "/")
	if len(s) != 2 {
		return "", "", fmt.Errorf("malformed exoscale id")
	}

	id, err := v3.ParseUUID(s[1])
	if err != nil {
		return "", "", err
	}

	return s[0], id, nil
}

// TODO (pej) multizone: Add v3.URL back once url environment are fixed.
func newZoneTopology(zoneName string) []*csi.Topology {
	return []*csi.Topology{
		{
			Segments: map[string]string{ZoneTopologyKey: zoneName},
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
