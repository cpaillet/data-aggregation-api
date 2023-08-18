package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Registry struct {
	BuiltDevicesNumber *prometheus.GaugeVec
	lastBuildStatus    *prometheus.GaugeVec
	buildTotal         *prometheus.CounterVec
}

func NewRegistry() Registry {
	return Registry{
		BuiltDevicesNumber: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "built_devices_number",
				Help: "Number of devices built during last successful build",
			},
			[]string{},
		),

		lastBuildStatus: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "build_status",
				Help: "Last completed build status, 0=Failed, 1=Success",
			},
			[]string{},
		),

		buildTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "completed_build_total",
				Help: "Total number of completed build",
			},
			[]string{"success"},
		),
	}
}

// BuildSuccessful updates all Prometheus metrics related to build.
//
// `build_status` counter is set to 1.
// `completed_build_total` increases with success label set to true.
func (r *Registry) BuildSuccessful() {
	r.lastBuildStatus.WithLabelValues().Set(1)
	r.buildTotal.WithLabelValues("true").Inc()
}

// BuildFailed updates all Prometheus metrics related to build.
//
// `build_status` counter is set to 0.
// `completed_build_total` increases with success label set to false.
func (r *Registry) BuildFailed() {
	r.lastBuildStatus.WithLabelValues().Set(0)
	r.buildTotal.WithLabelValues("false").Inc()
}

// SetBuiltDevices updates the `built_devices` gauge.
func (r *Registry) SetBuiltDevices(count uint32) {
	r.BuiltDevicesNumber.WithLabelValues().Set(float64(count))
}