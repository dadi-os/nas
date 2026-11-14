package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// requireEnv returns the value of key or an error with CodeConfigMissing semantics.
func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

// requireSeconds returns the positive whole number of seconds in key as a duration, or an
// error with CodeConfigMissing semantics.
func requireSeconds(key string) (time.Duration, error) {
	raw, err := requireEnv(key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive whole number of seconds", key)
	}
	return time.Duration(n) * time.Second, nil
}

// requireInt returns the positive whole number in key, or an error with CodeConfigMissing
// semantics.
func requireInt(key string) (int, error) {
	raw, err := requireEnv(key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive whole number", key)
	}
	return n, nil
}
