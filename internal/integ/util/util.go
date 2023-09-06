package util

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	APIKeyEnvVar    = "EXOSCALE_API_KEY"
	APISecretEnvVar = "EXOSCALE_API_SECRET"

	// TODO(sauterp) remove once it is certain that we use a public registry
	RegistryUsernameEnvVar = "REGISTRY_USERNAME"
	RegistryPasswordEnvVar = "REGISTRY_PASSWORD"
)

func GetRepoRootDir() string {
	path, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		panic(fmt.Errorf("failed to get git repo root dir; you should run this test in a git repo: %w", err))
	}

	return strings.TrimSpace(string(path)) + "/"
}
