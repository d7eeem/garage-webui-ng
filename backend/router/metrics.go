package router

import (
	"khairul169/garage-webui/utils"
	"net/http"
	"strconv"
	"strings"
)

type Metrics struct{}

// curatedMetrics are the Prometheus metric family names surfaced to the UI.
var curatedMetrics = []string{
	"api_s3_request_counter",
	"api_s3_error_counter",
	"block_bytes_read",
	"block_bytes_written",
}

// parsePromMetrics sums each wanted metric family across all its label sets,
// producing one number per family. Prometheus text format: comment lines start
// with '#'; data lines are `name{labels} value` or `name value`.
func parsePromMetrics(body []byte, want []string) map[string]float64 {
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	out := make(map[string]float64, len(want))
	for _, w := range want {
		out[w] = 0 // ensure every requested key is present, even if absent upstream
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i >= 0 {
			name = line[:i]
		}
		if !wantSet[name] {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			out[name] += v
		}
	}
	return out
}

// GET /metrics — curated, JSON-shaped subset of Garage's Prometheus metrics.
func (m *Metrics) Get(w http.ResponseWriter, r *http.Request) {
	body, err := utils.Garage.FetchMetrics()
	if err != nil {
		utils.ResponseError(w, err)
		return
	}
	utils.ResponseSuccess(w, parsePromMetrics(body, curatedMetrics))
}
