package util

import (
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

func CreateEgoscaleClient() (*exov3.Client, error) {
	v3Client, err := exov3.NewClient(credentials.NewEnvCredentials(), exov3.ClientOptWithEndpoint(exov3.CHGva2))
	if err != nil {
		return nil, fmt.Errorf("error setting up egoscale client: %w", err)
	}

	return v3Client, nil
}
