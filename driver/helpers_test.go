package driver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/require"
)

func TestGetNewVolumeSize(t *testing.T) {
	var min int64 = convertGiBToBytes(MinimalVolumeSizeGiB)
	var max int64 = convertGiBToBytes(MaximumVolumeSizeGiB)
	testsBench := []struct {
		capRange *csi.CapacityRange
		res      int64
		err      error
	}{
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    0,
			},
			res: min,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    0,
			},
			res: min + 10,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    min + 10,
			},
			res: min + 10,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min - 10,
				LimitBytes:    0,
			},
			res: 0,
			err: errRequiredBytesLessThanMinimun,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    min - 10,
			},
			res: 0,
			err: errLimitLessThanMinimum,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 5,
			},
			res: 0,
			err: errLimitLessThanRequiredBytes,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 5,
			},
			res: 0,
			err: errLimitLessThanRequiredBytes,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: max + 10,
				LimitBytes:    0,
			},
			res: 0,
			err: errRequiredBytesGreaterThanMaximun,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    max + 10,
			},
			res: 0,
			err: errLimitGreaterThanMaximum,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 10,
			},
			res: min + 10,
			err: nil,
		},
		{
			capRange: &csi.CapacityRange{
				RequiredBytes: min + 10,
				LimitBytes:    min + 20,
			},
			res: min + 10,
			err: nil,
		},
	}

	for _, test := range testsBench {
		res, err := getNewVolumeSize(test.capRange)
		require.Equal(t, test.err, err)
		require.Equal(t, test.res, res)
	}
}

func TestCreateMountPoint(t *testing.T) {
	tmpDir := t.TempDir()

	testBench := []struct {
		name        string
		relPath     string
		file        bool
		preCreate   bool
		expectError bool
		isDir       bool
	}{
		{
			name:    "create directory mount point",
			relPath: "parent/dir",
			file:    false,
			isDir:   true,
		},
		{
			name:    "create file mount point",
			relPath: "parent/file.txt",
			file:    true,
			isDir:   false,
		},
		{
			name:      "existing path no error",
			relPath:   "parent",
			file:      false,
			preCreate: true,
			isDir:     true,
		},
	}

	for _, test := range testBench {
		t.Run(test.name, func(t *testing.T) {
			fullPath := filepath.Join(tmpDir, test.relPath)

			if test.preCreate {
				err := os.MkdirAll(fullPath, 0755)
				require.NoError(t, err)
			}

			err := createMountPoint(fullPath, test.file)

			if test.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			info, err := os.Stat(fullPath)
			require.NoError(t, err)
			require.Equal(t, test.isDir, info.IsDir())
		})
	}
}
