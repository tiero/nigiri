package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/loader"
)

func GetServices(composeFile string) ([][]string, error) {

	composeBytes, err := os.ReadFile(composeFile)
	if err != nil {
		return nil, err
	}

	parsed, err := loader.ParseYAML(composeBytes)
	if err != nil {
		return nil, err
	}

	if _, ok := parsed["services"]; !ok {
		return nil, errors.New("missing services in compose")
	}

	serviceMap := parsed["services"].(map[string]interface{})

	var services [][]string
	for k, v := range serviceMap {
		m := v.(map[string]interface{})
		i := m["ports"].([]interface{})
		for _, j := range i {
			port := j.(string)
			exposedPorts := strings.Split(port, ":")
			endpoint := "localhost:" + exposedPorts[0]
			services = append(services, []string{k, endpoint})
		}

	}

	return services, nil
}

func GetPortsForService(composePath, serviceName string) ([]string, error) {
	cmd := exec.Command("docker-compose", "-f", composePath, "ps", "-q", serviceName)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get container ID: %w", err)
	}

	if len(output) == 0 {
		return nil, fmt.Errorf("no container found for service %s", serviceName)
	}

	containerId := strings.TrimSpace(string(output))
	fmt.Printf("Container ID for %s: %s\n", serviceName, containerId)

	// Get container status using docker ps with format=json
	cmd = exec.Command("docker", "ps", "--format", "{{json .}}", "--no-trunc", "--filter", fmt.Sprintf("id=%s", containerId))
	output, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get container status: %w", err)
	}

	fmt.Printf("Container status output: %s\n", string(output))

	var status containerStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return nil, fmt.Errorf("failed to parse container status: %w", err)
	}

	if status.Ports == "" {
		return nil, fmt.Errorf("no ports found for service %s", serviceName)
	}

	fmt.Printf("Container ports: %s\n", status.Ports)

	// Parse ports from the format: "0.0.0.0:18443->18443/tcp"
	var ports []string
	portMappings := strings.Split(status.Ports, ", ")
	for _, mapping := range portMappings {
		parts := strings.Split(mapping, "->")
		if len(parts) == 2 {
			hostPort := strings.Split(parts[0], ":")[1]
			ports = append(ports, hostPort)
		}
	}

	return ports, nil
}

type containerStatus struct {
	ID     string `json:"ID"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Names  string `json:"Names"`
	Ports  string `json:"Ports"`
}

// WaitForService waits for a Docker service to be ready by checking its status
func WaitForService(composePath string, serviceName string) error {
	timeout := time.After(5 * time.Minute)
	tick := time.Tick(1 * time.Second)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for service %s", serviceName)
		case <-tick:
			cmd := exec.Command("docker-compose", "-f", composePath, "ps", "-q", serviceName)
			output, err := cmd.Output()
			if err != nil {
				continue
			}

			if len(output) == 0 {
				continue
			}

			containerId := strings.TrimSpace(string(output))

			// Get container status using docker ps with format=json
			cmd = exec.Command("docker", "ps", "--format", "{{json .}}", "--no-trunc", "--filter", fmt.Sprintf("id=%s", containerId))
			output, err = cmd.Output()
			if err != nil {
				continue
			}

			var status containerStatus
			if err := json.Unmarshal(output, &status); err != nil {
				continue
			}

			if status.State == "running" {
				fmt.Printf("Service %s status: %s\n", serviceName, status.Status)
				fmt.Printf("Service status output: %s\n", string(output))
				return nil
			}
		}
	}
}
