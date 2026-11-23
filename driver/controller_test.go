package driver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	v3 "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestControllerPublishVolumeAlreadyOnNode(t *testing.T) {
	const zone = "ch-gva-2"
	volumeID := v3.UUID("2bd8a3a9-2b84-4e57-8a8b-7f1c5d2a3b10")
	instanceID := v3.UUID("8c0e7b9e-5c4e-4b1a-9a43-2f6f1d0e9a11")

	tests := []struct {
		state    v3.BlockStorageVolumeState
		wantCode codes.Code
	}{
		{v3.BlockStorageVolumeStateAttached, codes.OK},
		{v3.BlockStorageVolumeStateAttaching, codes.Aborted},
		{v3.BlockStorageVolumeStateDetaching, codes.Aborted},
		{v3.BlockStorageVolumeStateError, codes.Aborted},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			var attachCalls atomic.Int32
			mux := http.NewServeMux()
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			mux.HandleFunc("/zone", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"zones":[{"name":%q,"api-endpoint":%q}]}`, zone, srv.URL)
			})
			mux.HandleFunc("/block-storage/", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ":attach") {
					attachCalls.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				fmt.Fprintf(w, `{"id":%q,"name":"vol","state":%q,"instance":{"id":%q}}`, volumeID, tt.state, instanceID)
			})

			client, err := v3.NewClient(credentials.NewStaticCredentials("EXOkey", "secret"), v3.ClientOptWithEndpoint(v3.Endpoint(srv.URL)))
			require.NoError(t, err)
			d := &controllerService{client: client}

			resp, err := d.ControllerPublishVolume(context.Background(), &csi.ControllerPublishVolumeRequest{
				VolumeId: exoscaleID(zone, volumeID),
				NodeId:   exoscaleID(zone, instanceID),
			})

			require.Equal(t, tt.wantCode, status.Code(err), "error: %v", err)
			if tt.wantCode == codes.OK {
				require.Equal(t, volumeID.String(), resp.PublishContext[exoscaleVolumeID])
			}
			require.Zero(t, attachCalls.Load(), "attach must not be called for a volume already on the node")
		})
	}
}

func TestControllerExpandVolume(t *testing.T) {
	const zone = "ch-gva-2"
	volumeID := v3.UUID("2bd8a3a9-2b84-4e57-8a8b-7f1c5d2a3b10")
	instanceID := v3.UUID("8c0e7b9e-5c4e-4b1a-9a43-2f6f1d0e9a11")

	tests := []struct {
		name       string
		sizeGiB    int64
		attached   bool
		wantCode   codes.Code
		wantResize bool
	}{
		{"smaller detached volume is resized", 1, false, codes.OK, true},
		{"smaller attached volume must be detached first", 1, true, codes.FailedPrecondition, false},
		{"already resized detached volume", 5, false, codes.OK, false},
		{"already resized volume attached again", 5, true, codes.OK, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resizeCalls atomic.Int32
			mux := http.NewServeMux()
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			mux.HandleFunc("/zone", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"zones":[{"name":%q,"api-endpoint":%q}]}`, zone, srv.URL)
			})
			mux.HandleFunc("/block-storage/", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ":resize-volume") {
					resizeCalls.Add(1)
					fmt.Fprintf(w, `{"id":%q,"name":"vol","size":5}`, volumeID)
					return
				}
				instance := ""
				if tt.attached {
					instance = fmt.Sprintf(`,"instance":{"id":%q}`, instanceID)
				}
				fmt.Fprintf(w, `{"id":%q,"name":"vol","size":%d%s}`, volumeID, tt.sizeGiB, instance)
			})

			client, err := v3.NewClient(credentials.NewStaticCredentials("EXOkey", "secret"), v3.ClientOptWithEndpoint(v3.Endpoint(srv.URL)))
			require.NoError(t, err)
			d := &controllerService{client: client}

			resp, err := d.ControllerExpandVolume(context.Background(), &csi.ControllerExpandVolumeRequest{
				VolumeId:      exoscaleID(zone, volumeID),
				CapacityRange: &csi.CapacityRange{RequiredBytes: 5 * GiB},
			})

			require.Equal(t, tt.wantCode, status.Code(err), "error: %v", err)
			if tt.wantCode == codes.OK {
				require.EqualValues(t, 5*GiB, resp.CapacityBytes)
				require.True(t, resp.NodeExpansionRequired)
			}
			require.Equal(t, tt.wantResize, resizeCalls.Load() == 1, "resize calls: %d", resizeCalls.Load())
		})
	}
}
