package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultTopTenants is how many tenants the top-N mode keeps their own label
// for when the operator names no count.
const DefaultTopTenants = 10

// claimBuckets and nodeBuckets are the histogram bucket sets. Chosen for the
// scales the two latencies actually live on: a claim is usually milliseconds
// and is worrying past a few seconds, a node run is usually sub-second and is
// routinely minutes (a model call, a slow page).
var (
	claimBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
	nodeBuckets  = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
)

// tenantKey is the label key every tenant-scoped series uses.
const tenantKey = "tenant"

// Registry holds every series this process reports, and renders them in the
// Prometheus text exposition format.
//
// A *Registry is safe for concurrent use. The zero value is not usable: build
// one with NewRegistry, which also installs the fixed series.
type Registry struct {
	mu sync.Mutex

	tenantLabel string
	topTenants  int

	// tenantTotals counts observations per tenant, which is how the top-N
	// mode decides who keeps a label at render time without holding a
	// config-time snapshot that a burst could invalidate.
	tenantTotals map[string]uint64

	counters map[string]*counterSeries
	gauges   map[string]*gaugeSeries
	histos   map[string]*histogramSeries
}

// counterSeries is one counter metric: a value per label set.
type counterSeries struct {
	help   string
	labels []string
	values map[string]*counterValue
}

type counterValue struct {
	labelValues []string
	value       float64
}

// gaugeSeries is one gauge metric, its sample read at render time from a
// registered collector rather than kept running: everything a gauge here
// reports — queue depth, running executions, worker capacity — is a question
// about now, and the answer belongs to the moment of the scrape.
type gaugeSeries struct {
	help      string
	collector GaugeCollector
}

// histogramSeries is one histogram metric: buckets, sum and count per label
// set, with the buckets cumulative the way the format expects.
type histogramSeries struct {
	help    string
	labels  []string
	buckets []float64
	values  map[string]*histogramValue
}

type histogramValue struct {
	labelValues []string
	counts      []uint64 // cumulative per bucket
	sum         float64
	count       uint64
}

// GaugeCollector reads one gauge's value at scrape time. A collector that
// cannot answer returns an error and its sample is left out of that scrape —
// absent is honest, a zero would read as "no work queued" on a database the
// collector could not reach.
type GaugeCollector func(ctx context.Context) (float64, error)

// NewRegistry builds a registry carrying the fixed series. tenantLabel is one
// of the TenantLabel modes; an unknown value is Off. topTenants applies to
// the top mode and defaults when zero.
func NewRegistry(tenantLabel string, topTenants int) *Registry {
	if topTenants <= 0 {
		topTenants = DefaultTopTenants
	}
	registry := &Registry{
		tenantLabel:  tenantLabel,
		topTenants:   topTenants,
		tenantTotals: map[string]uint64{},
		counters:     map[string]*counterSeries{},
		gauges:       map[string]*gaugeSeries{},
		histos:       map[string]*histogramSeries{},
	}
	registry.fixedSeries()
	return registry
}

// fixedSeries installs the series this server reports. One place, so the
// exposition, the documentation page and any future client-library port all
// read the same list.
func (registry *Registry) fixedSeries() {
	registry.counter("kilasflow_executions_started_total",
		"Executions that began running, by trigger.", []string{"trigger", tenantKey})
	registry.counter("kilasflow_executions_finished_total",
		"Executions that reached a terminal state, by trigger and status.", []string{"trigger", "status", tenantKey})
	registry.histogram("kilasflow_execution_claim_seconds",
		"How long an execution sat queued before a worker claimed it.", []string{"trigger", tenantKey}, claimBuckets)
	registry.histogram("kilasflow_node_run_seconds",
		"How long one node ran, by node type.", []string{"node_type", tenantKey}, nodeBuckets)
	registry.counter("kilasflow_webhook_requests_total",
		"Inbound webhook requests, by outcome: accepted, filtered or refused.", []string{"outcome", tenantKey})
}

// RegisterGauge adds one gauge read at scrape time. Registering a name twice
// is an error, because the second registration would silently win and the
// scraper would never know.
func (registry *Registry) RegisterGauge(name, help string, collector GaugeCollector) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.gauges[name]; exists {
		return fmt.Errorf("a gauge named %q is already registered", name)
	}
	registry.gauges[name] = &gaugeSeries{help: help, collector: collector}
	return nil
}

func (registry *Registry) counter(name, help string, labels []string) {
	registry.counters[name] = &counterSeries{help: help, labels: labels, values: map[string]*counterValue{}}
}

func (registry *Registry) histogram(name, help string, labels []string, buckets []float64) {
	registry.histos[name] = &histogramSeries{help: help, labels: labels, buckets: buckets, values: map[string]*histogramValue{}}
}

// labelKey renders the map key a label value set hashes to.
func labelKey(values []string) string {
	return strings.Join(values, "\x00")
}

// observeTenantLocked counts one observation for a tenant, which is what the
// top-N fold is decided on. Callers hold the lock.
func (registry *Registry) observeTenantLocked(tenant string) {
	if registry.tenantLabel != TenantLabelOff {
		registry.tenantTotals[tenant]++
	}
}

// labelValuesLocked places the tenant into the value slice at the position
// the series declares it, threading the other values through the rest. When
// the mode is off the tenant is dropped and the label set narrows to the
// rest. Callers hold the lock.
func (registry *Registry) labelValuesLocked(seriesLabels []string, tenant string, rest ...string) (values, labels []string) {
	if registry.tenantLabel == TenantLabelOff {
		kept := make([]string, 0, len(seriesLabels))
		for _, label := range seriesLabels {
			if label != tenantKey {
				kept = append(kept, label)
			}
		}
		return rest, kept
	}
	values = make([]string, 0, len(seriesLabels))
	restIndex := 0
	for _, label := range seriesLabels {
		if label == tenantKey {
			values = append(values, tenant)
			continue
		}
		values = append(values, rest[restIndex])
		restIndex++
	}
	return values, seriesLabels
}

// ExecutionClaimed observes a claim: the wait, on the claim histogram.
func (registry *Registry) ExecutionClaimed(tenant, trigger string, waited time.Duration) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.observeTenantLocked(tenant)
	series := registry.histos["kilasflow_execution_claim_seconds"]
	values, labels := registry.labelValuesLocked(series.labels, tenant, trigger)
	series.observe(values, labels, waited.Seconds())
}

// ExecutionStarted counts an execution that began running.
func (registry *Registry) ExecutionStarted(tenant, trigger string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.observeTenantLocked(tenant)
	series := registry.counters["kilasflow_executions_started_total"]
	values, labels := registry.labelValuesLocked(series.labels, tenant, trigger)
	series.add(values, labels, 1)
}

// ExecutionSettled counts a terminal execution, by trigger and status.
func (registry *Registry) ExecutionSettled(tenant, trigger, status string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.observeTenantLocked(tenant)
	series := registry.counters["kilasflow_executions_finished_total"]
	values, labels := registry.labelValuesLocked(series.labels, tenant, trigger, status)
	series.add(values, labels, 1)
}

// NodeRun observes one node run's duration by node type.
func (registry *Registry) NodeRun(tenant, nodeType string, took time.Duration) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.observeTenantLocked(tenant)
	series := registry.histos["kilasflow_node_run_seconds"]
	values, labels := registry.labelValuesLocked(series.labels, tenant, nodeType)
	series.observe(values, labels, took.Seconds())
}

// WebhookRequest counts one inbound webhook request by outcome.
func (registry *Registry) WebhookRequest(tenant, outcome string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.observeTenantLocked(tenant)
	series := registry.counters["kilasflow_webhook_requests_total"]
	values, labels := registry.labelValuesLocked(series.labels, tenant, outcome)
	series.add(values, labels, 1)
}

func (series *counterSeries) add(values, labels []string, delta float64) {
	key := labelKey(values)
	value, found := series.values[key]
	if !found {
		series.values[key] = &counterValue{labelValues: values, value: delta}
		return
	}
	value.value += delta
	_ = labels
}

func (series *histogramSeries) observe(values, labels []string, seconds float64) {
	key := labelKey(values)
	value, found := series.values[key]
	if !found {
		value = &histogramValue{labelValues: values, counts: make([]uint64, len(series.buckets))}
		series.values[key] = value
	}
	for index, bound := range series.buckets {
		if seconds <= bound {
			value.counts[index]++
		}
	}
	value.sum += seconds
	value.count++
	_ = labels
}

// Render writes the registry in the Prometheus text exposition format
// (version 0.0.4). Families appear in name order, their samples in a stable
// order of the label values; a gauge whose collector cannot answer is left
// out of that scrape.
//
// In the top-N tenant mode the fold happens here: the tenants with the most
// observations keep their label, every other series is merged into
// tenant="other", so a scrape's output is bounded however many tenants the
// deployment serves.
func (registry *Registry) Render(ctx context.Context) string {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	names := make([]string, 0, len(registry.counters)+len(registry.gauges)+len(registry.histos))
	for name := range registry.counters {
		names = append(names, name)
	}
	for name := range registry.gauges {
		names = append(names, name)
	}
	for name := range registry.histos {
		names = append(names, name)
	}
	sort.Strings(names)

	var out strings.Builder
	allowed := registry.allowedTenantsLocked()
	for _, name := range names {
		if counter, isCounter := registry.counters[name]; isCounter {
			renderCounter(&out, name, counter, registry.effectiveLabelsLocked(counter.labels), allowed)
			continue
		}
		if gauge, isGauge := registry.gauges[name]; isGauge {
			value, err := gauge.collector(ctx)
			if err != nil {
				continue
			}
			writeFamily(&out, name, gauge.help, "gauge", []sample{{value: value}})
			continue
		}
		histogram := registry.histos[name]
		renderHistogram(&out, name, histogram, registry.effectiveLabelsLocked(histogram.labels), allowed)
	}
	return out.String()
}

// effectiveLabelsLocked is the label set a series' stored values render
// against: the declared set, without the tenant label when the mode is off —
// the same narrowing labelValuesLocked applied when the values were recorded.
// Callers hold the lock.
func (registry *Registry) effectiveLabelsLocked(seriesLabels []string) []string {
	if registry.tenantLabel == TenantLabelOff {
		kept := make([]string, 0, len(seriesLabels))
		for _, label := range seriesLabels {
			if label != tenantKey {
				kept = append(kept, label)
			}
		}
		return kept
	}
	return seriesLabels
}

// allowedTenantsLocked names the tenants that keep their own label in the
// top-N mode, most observations first and ties broken by id so two scrapes of
// the same state render the same set. A nil map is no fold. Callers hold the
// lock.
func (registry *Registry) allowedTenantsLocked() map[string]bool {
	if registry.tenantLabel != TenantLabelTop {
		return nil
	}
	type counted struct {
		tenant string
		total  uint64
	}
	all := make([]counted, 0, len(registry.tenantTotals))
	for tenant, total := range registry.tenantTotals {
		all = append(all, counted{tenant: tenant, total: total})
	}
	sort.Slice(all, func(left, right int) bool {
		if all[left].total != all[right].total {
			return all[left].total > all[right].total
		}
		return all[left].tenant < all[right].tenant
	})
	allowed := make(map[string]bool, registry.topTenants)
	for _, entry := range all {
		if len(allowed) >= registry.topTenants {
			break
		}
		allowed[entry.tenant] = true
	}
	return allowed
}

// sample is one line of a family. name is the series-name suffix the text
// format gives histograms: empty, or _bucket, _sum, _count.
type sample struct {
	suffix string
	labels []string
	values []string
	value  float64
}

func renderCounter(out *strings.Builder, name string, series *counterSeries, labels []string, allowed map[string]bool) {
	merged := map[string]*counterValue{}
	for _, value := range series.values {
		folded := foldTenant(value.labelValues, labels, allowed)
		key := labelKey(folded)
		if existing, found := merged[key]; found {
			existing.value += value.value
			continue
		}
		merged[key] = &counterValue{labelValues: folded, value: value.value}
	}
	samples := make([]sample, 0, len(merged))
	for _, value := range merged {
		samples = append(samples, sample{labels: labels, values: value.labelValues, value: value.value})
	}
	writeFamily(out, name, series.help, "counter", samples)
}

func renderHistogram(out *strings.Builder, name string, series *histogramSeries, labels []string, allowed map[string]bool) {
	merged := map[string]*histogramValue{}
	for _, value := range series.values {
		folded := foldTenant(value.labelValues, labels, allowed)
		key := labelKey(folded)
		existing, found := merged[key]
		if !found {
			merged[key] = &histogramValue{
				labelValues: folded,
				counts:      append([]uint64(nil), value.counts...),
				sum:         value.sum, count: value.count,
			}
			continue
		}
		for index := range existing.counts {
			existing.counts[index] += value.counts[index]
		}
		existing.sum += value.sum
		existing.count += value.count
	}
	samples := make([]sample, 0, len(merged)*(len(series.buckets)+3))
	for _, value := range merged {
		for index, bound := range series.buckets {
			samples = append(samples, sample{
				suffix: "_bucket",
				labels: append(append([]string(nil), labels...), "le"),
				values: append(append([]string(nil), value.labelValues...), formatBound(bound)),
				value:  float64(value.counts[index]),
			})
		}
		// The format requires the +Inf bucket, whose value is the count:
		// every observation is at or below infinity.
		samples = append(samples,
			sample{
				suffix: "_bucket",
				labels: append(append([]string(nil), labels...), "le"),
				values: append(append([]string(nil), value.labelValues...), "+Inf"),
				value:  float64(value.count),
			},
			sample{suffix: "_sum", labels: labels, values: value.labelValues, value: value.sum},
			sample{suffix: "_count", labels: labels, values: value.labelValues, value: float64(value.count)},
		)
	}
	sort.SliceStable(samples, func(left, right int) bool {
		return strings.Join(samples[left].values, "\x00") < strings.Join(samples[right].values, "\x00")
	})
	writeFamily(out, name, series.help, "histogram", samples)
}

// foldTenant replaces the tenant value of a label set with TenantOther when
// the fold is active and this tenant is not among the allowed. A nil allowed
// map is no fold.
func foldTenant(values, labels []string, allowed map[string]bool) []string {
	if allowed == nil {
		return values
	}
	folded := make([]string, len(values))
	for index, label := range labels {
		if label == tenantKey && !allowed[values[index]] {
			folded[index] = TenantOther
			continue
		}
		folded[index] = values[index]
	}
	return folded
}

func writeFamily(out *strings.Builder, name, help, typ string, samples []sample) {
	if len(samples) == 0 {
		return
	}
	if help != "" {
		fmt.Fprintf(out, "# HELP %s %s\n", name, escapeText(help))
	}
	fmt.Fprintf(out, "# TYPE %s %s\n", name, typ)
	for _, one := range samples {
		fmt.Fprintf(out, "%s%s%s %s\n", name, one.suffix, renderLabels(one.labels, one.values), formatValue(one.value))
	}
}

func renderLabels(labels, values []string) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for index, label := range labels {
		// The value is escaped by hand rather than through %q: Go's quoting
		// escapes more than the text format defines (a tab as \t, for one),
		// and a scraper reading a value the format never promised would read
		// back something other than what was recorded.
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", label, escapeText(values[index])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// escapeText escapes what the text format gives meaning to in a label value
// or a HELP line: a backslash, a double quote and a newline.
func escapeText(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}

// formatValue renders a sample value: Go's shortest float representation is
// a legal text-format value, with a trailing .0 trimmed the way scrapers
// usually write integers.
func formatValue(value float64) string {
	return strings.TrimSuffix(fmt.Sprintf("%v", value), ".0")
}

// formatBound renders a bucket bound, matching the value formatting.
func formatBound(bound float64) string {
	return formatValue(bound)
}
