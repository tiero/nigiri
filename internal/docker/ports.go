package docker

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type DockerCompose struct {
	Services map[string]Service `yaml:"services"`
}

type Service struct {
	Command []string `yaml:"command"`
	Ports   []string `yaml:"ports"`
}

func GetServicePort(composePath, serviceName string) (int, error) {
	data, err := os.ReadFile(composePath)
	if err != nil {
		return 0, fmt.Errorf("failed to read compose file: %w", err)
	}

	var config DockerCompose
	if err := yaml.Unmarshal(data, &config); err != nil {
		return 0, fmt.Errorf("failed to parse compose file: %w", err)
	}

	service, ok := config.Services[serviceName]
	if !ok {
		return 0, fmt.Errorf("service %s not found", serviceName)
	}

	// Find the first port mapping
	if len(service.Ports) == 0 {
		return 0, fmt.Errorf("no ports defined for service %s", serviceName)
	}

	// Parse port mapping in the format "host:container"
	parts := strings.Split(service.Ports[0], ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid port mapping format: %s", service.Ports[0])
	}

	hostPort, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid host port: %w", err)
	}

	return hostPort, nil
}

func GetElectrsPort(composePath, serviceName string) (int, error) {
	return GetServicePort(composePath, serviceName)
}

func indexOf(slice []string, item string) int {
	for i, s := range slice {
		if s == item {
			return i
		}
	}
	return -1
}
