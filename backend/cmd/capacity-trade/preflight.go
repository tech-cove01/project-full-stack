package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type containerPreflight struct {
	Version    int    `json:"version"`
	CheckedAt  int64  `json:"checked_at_unix_ms"`
	Project    string `json:"project"`
	Containers []struct {
		ID      string            `json:"id"`
		Name    string            `json:"name"`
		Service string            `json:"service"`
		Running bool              `json:"running"`
		Labels  map[string]string `json:"labels"`
		Ports   []struct {
			Target   string `json:"target"`
			HostIP   string `json:"host_ip"`
			HostPort string `json:"host_port"`
		} `json:"ports"`
	} `json:"containers"`
}

func verifyExternalPreflight(dir string) error {
	path, err := temporaryPoolPath(os.Getenv("CAP_PREFLIGHT_FILE"))
	if err != nil {
		return errors.New("explicit preflight must be a new OS-temporary manifest")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("external Docker preflight unavailable")
	}
	defer file.Close()
	var manifest containerPreflight
	decoder := json.NewDecoder(io.LimitReader(file, 32768))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil {
		return errors.New("invalid external Docker preflight schema")
	}
	if validatePreflight(manifest, time.Now().UnixMilli()) != nil {
		return errors.New("external Docker preflight stale or dedicated ownership guard failed")
	}
	return writeJSON(filepath.Join(dir, "external-container-preflight.json"), manifest)
}

func validatePreflight(m containerPreflight, now int64) error {
	if m.Version != 1 || m.Project != project || m.CheckedAt > now || now-m.CheckedAt > 15000 || len(m.Containers) != 2 {
		return errors.New("invalid preflight identity or age")
	}
	seen := map[string]bool{}
	for _, container := range m.Containers {
		target, hostPort := "", ""
		switch container.Service {
		case "mysql":
			target, hostPort = "3306/tcp", "3450"
		case "redis":
			target, hostPort = "6379/tcp", "6550"
		default:
			return errors.New("unexpected preflight service")
		}
		if seen[container.Service] || !container.Running || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(container.ID) || container.Name != "/"+project+"-"+container.Service+"-1" || container.Labels["com.docker.compose.project"] != project || container.Labels["com.docker.compose.service"] != container.Service {
			return errors.New("preflight service ownership mismatch")
		}
		seen[container.Service] = true
		bound := false
		for _, binding := range container.Ports {
			if binding.Target == target && binding.HostIP == "127.0.0.1" && binding.HostPort == hostPort {
				bound = true
			}
		}
		if !bound {
			return errors.New("preflight lacks exact loopback binding")
		}
	}
	return nil
}
