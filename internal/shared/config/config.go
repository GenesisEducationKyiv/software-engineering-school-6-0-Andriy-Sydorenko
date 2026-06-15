package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Validator interface {
	Validate() error
}

func ValidateAll(vs ...Validator) error {
	errs := make([]error, 0, len(vs))
	for _, v := range vs {
		errs = append(errs, v.Validate())
	}
	return errors.Join(errs...)
}

func GetEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("config: invalid %s %q: %v", key, v, err))
	}
	return d
}

func GetEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("config: invalid %s %q: %v", key, v, err))
	}
	return n
}
