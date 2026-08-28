package cli

import (
	"github.com/gualask/linqode/internal/compose"
	"github.com/gualask/linqode/internal/host"
	"github.com/gualask/linqode/internal/logs"
	"github.com/gualask/linqode/internal/operations"
)

type statusDocument struct {
	SchemaVersion int              `json:"schema_version"`
	Host          string           `json:"host"`
	Services      []servicePayload `json:"services"`
}

type servicePayload struct {
	Service   string `json:"service"`
	Container string `json:"container"`
	State     string `json:"state"`
	Health    string `json:"health"`
	Restarts  *int   `json:"restarts"`
	ExitCode  int    `json:"exit_code"`
	Status    string `json:"status"`
	Ports     string `json:"ports"`
}

type statsDocument struct {
	SchemaVersion int                     `json:"schema_version"`
	Host          string                  `json:"host"`
	HostMetrics   hostMetricsPayload      `json:"host_metrics"`
	Containers    []containerStatsPayload `json:"containers"`
}

type hostStatsEvent struct {
	SchemaVersion int                `json:"schema_version"`
	Type          string             `json:"type"`
	Stats         hostMetricsPayload `json:"stats"`
}

type hostMetricsPayload struct {
	Load1                float64 `json:"load_1"`
	Load5                float64 `json:"load_5"`
	Load15               float64 `json:"load_15"`
	CPUs                 int     `json:"cpus"`
	UptimeSeconds        int64   `json:"uptime_seconds"`
	MemoryTotalBytes     uint64  `json:"memory_total_bytes"`
	MemoryAvailableBytes uint64  `json:"memory_available_bytes"`
	MemoryUsedBytes      uint64  `json:"memory_used_bytes"`
	DiskTotalBytes       uint64  `json:"disk_total_bytes"`
	DiskUsedBytes        uint64  `json:"disk_used_bytes"`
}

type containerStatsEvent struct {
	SchemaVersion int                   `json:"schema_version"`
	Type          string                `json:"type"`
	Stats         containerStatsPayload `json:"stats"`
}

type containerStatsPayload struct {
	Container     string `json:"container"`
	ID            string `json:"id"`
	CPUPercent    string `json:"cpu_percent"`
	MemoryUsage   string `json:"memory_usage"`
	MemoryPercent string `json:"memory_percent"`
	NetworkIO     string `json:"network_io"`
	BlockIO       string `json:"block_io"`
	PIDs          string `json:"pids"`
}

type textEvent struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	Data          string `json:"data"`
}

type logEvent struct {
	SchemaVersion int               `json:"schema_version"`
	Type          string            `json:"type"`
	Line          string            `json:"line"`
	Record        map[string]string `json:"record,omitempty"`
}

type exitEvent struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	Code          int    `json:"code"`
}

func servicePayloads(services []compose.Service) []servicePayload {
	result := make([]servicePayload, 0, len(services))
	for _, service := range services {
		result = append(result, servicePayload{
			Service:   service.Service,
			Container: service.Name,
			State:     service.State,
			Health:    service.Health,
			Restarts:  service.Restarts,
			ExitCode:  service.ExitCode,
			Status:    service.Status,
			Ports:     service.PortsSummary(),
		})
	}
	return result
}

func projectHostMetrics(metrics host.Metrics) hostMetricsPayload {
	return hostMetricsPayload{
		Load1:                metrics.Load1,
		Load5:                metrics.Load5,
		Load15:               metrics.Load15,
		CPUs:                 metrics.CPUs,
		UptimeSeconds:        int64(metrics.Uptime.Seconds()),
		MemoryTotalBytes:     metrics.MemTotalKB * 1024,
		MemoryAvailableBytes: metrics.MemAvailableKB * 1024,
		MemoryUsedBytes:      metrics.MemUsedKB() * 1024,
		DiskTotalBytes:       metrics.DiskTotalKB * 1024,
		DiskUsedBytes:        metrics.DiskUsedKB * 1024,
	}
}

func containerPayloads(containers []compose.ContainerStats) []containerStatsPayload {
	result := make([]containerStatsPayload, 0, len(containers))
	for _, container := range containers {
		result = append(result, containerPayload(container))
	}
	return result
}

func containerPayload(container compose.ContainerStats) containerStatsPayload {
	return containerStatsPayload{
		Container:     container.Name,
		ID:            container.ID,
		CPUPercent:    container.CPUPerc,
		MemoryUsage:   container.MemUsage,
		MemoryPercent: container.MemPerc,
		NetworkIO:     container.NetIO,
		BlockIO:       container.BlockIO,
		PIDs:          container.PIDs,
	}
}

func statsEventPayload(event operations.Event) (any, bool) {
	switch event.Kind {
	case operations.EventStats:
		return containerStatsEvent{
			SchemaVersion: schemaVersion,
			Type:          "container_stats",
			Stats:         containerPayload(event.Stats),
		}, true
	case operations.EventStderr:
		return textEvent{SchemaVersion: schemaVersion, Type: "stderr", Data: event.Text}, true
	default:
		return nil, false
	}
}

func logEventPayload(event operations.Event) (any, bool) {
	switch event.Kind {
	case operations.EventLog:
		payload := logEvent{SchemaVersion: schemaVersion, Type: "log", Line: event.Text}
		if record := logs.ParseRecord(event.Text); record != nil {
			payload.Record = make(map[string]string, len(record.Fields()))
			for _, field := range record.Fields() {
				payload.Record[field.Key] = field.Value
			}
		}
		return payload, true
	case operations.EventStderr:
		return textEvent{SchemaVersion: schemaVersion, Type: "stderr", Data: event.Text}, true
	default:
		return nil, false
	}
}
