package client

import (
	"fmt"

	"github.com/exoscale/egoscale/v3/credentials"

	exov3 "github.com/exoscale/egoscale/v3"
)

func CreateEgoscaleClient() (*exov3.Client, error) {
	v3Client, err := exov3.NewClient(credentials.NewEnvCredentials(), exov3.ClientOptWithEndpoint(exov3.CHGva2))
	if err != nil {
		return nil, fmt.Errorf("error setting up egoscale client: %w", err)
	}

	return v3Client, nil
}
