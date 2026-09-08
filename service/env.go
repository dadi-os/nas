package main

import (
	"fmt"
	"os"
)

// requireEnv returns the value of key or an error with CodeConfigMissing semantics.
func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}
