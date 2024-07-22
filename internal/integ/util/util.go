package util

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/exoscale/egoscale/v3/credentials"

	exov3 "github.com/exoscale/egoscale/v3"
)

const (
	APIKeyEnvVar    = "EXOSCALE_API_KEY"
	APISecretEnvVar = "EXOSCALE_API_SECRET"
)

func GetRepoRootDir() string {
	path, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		panic(fmt.Errorf("failed to get git repo root dir; you should run this test in a git repo: %w", err))
	}

	return strings.TrimSpace(string(path)) + "/"
}

func CreateEgoscaleClient(ctx context.Context, zone exov3.ZoneName) (*exov3.Client, error) {
	v3Client, err := exov3.NewClient(credentials.NewEnvCredentials())
	if err != nil {
		return nil, fmt.Errorf("error setting up egoscale client: %w", err)
	}

	endpoint, err := v3Client.GetZoneAPIEndpoint(ctx, zone)
	if err != nil {
		return nil, fmt.Errorf("error setting up egoscale client zone: %w", err)
	}

	return v3Client.WithEndpoint(endpoint), nil
}
