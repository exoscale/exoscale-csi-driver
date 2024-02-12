package driver

import (
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/require"
)

func TestGetNewVolumeSize(t *testing.T) {
	var min int64 = MinimalVolumeSizeBytes
	var max int64 = MaximumVolumeSizeBytes
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
