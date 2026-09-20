package scheduler

import (
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// WorkflowTimezoneSetting is the document setting naming the zone a workflow's
// schedules are read in.
//
// It lives on the workflow rather than on the node because that is where n8n
// keeps it and because it is one answer for the whole workflow: a document
// whose two triggers disagreed about what "09:00" meant would be a puzzle
// nobody could read off the canvas.
const WorkflowTimezoneSetting = "timezone"

// Extract turns a document's Schedule Triggers into the rows an activation
// stores, one per interval.
//
// The node types are passed in rather than imported so this package stays free
// of the node catalogue — the same reason the repository takes an extractor
// instead of knowing any node type itself.
//
// An interval that does not validate is skipped rather than failing the whole
// extraction. The compiler already refuses to activate a workflow whose trigger
// is invalid, so reaching here with a bad interval means something else let it
// through, and dropping one row is a smaller failure than a workflow that will
// not activate for a reason nothing states.
func Extract(scheduleNodeTypes ...string) repository.ScheduleExtractor {
	types := make(map[string]struct{}, len(scheduleNodeTypes))
	for _, nodeType := range scheduleNodeTypes {
		types[nodeType] = struct{}{}
	}
	return func(document workflow.Document) []repository.ScheduleTrigger {
		zone := documentTimezone(document)
		triggers := make([]repository.ScheduleTrigger, 0, 1)
		for _, node := range document.Nodes {
			if _, wanted := types[node.Type]; !wanted {
				continue
			}
			for index, interval := range NodeIntervals(node.Parameters) {
				if err := interval.Validate(); err != nil {
					continue
				}
				spec, err := interval.Cron()
				if err != nil {
					continue
				}
				triggers = append(triggers, repository.ScheduleTrigger{
					NodeID: node.ID, IntervalIndex: index, Cron: spec, Timezone: zone,
				})
			}
		}
		return triggers
	}
}

// NodeIntervals reads a Schedule Trigger's intervals, accepting the shape a
// workflow saved before the rule existed still carries.
//
// A node stored under the old surface has one `cron` string and no rule at all.
// Reading it as a single custom interval keeps every such workflow running
// through the change, which matters more here than anywhere else in the
// product: a schedule that stops firing does so silently.
func NodeIntervals(parameters map[string]any) []Interval {
	if intervals := ReadRule(parameters["rule"]); len(intervals) > 0 {
		return intervals
	}
	legacy := strings.TrimSpace(text(parameters["cron"]))
	if legacy == "" {
		return nil
	}
	return []Interval{{Field: FieldCronExpression, Expression: legacy}}
}

func documentTimezone(document workflow.Document) string {
	zone, _ := document.Settings[WorkflowTimezoneSetting].(string)
	return strings.TrimSpace(zone)
}

// DefaultTimezone resolves the schedules of an extractor's documents into the
// instance's own zone.
//
// n8n keeps the zone on the workflow (settings.timezone) and resolves the
// literal DEFAULT — which is what most exports carry, and what a workflow with
// no zone at all means — to the instance's GENERIC_TIMEZONE. This installation
// had no instance zone, so every imported schedule ran in UTC: an import of
// "every day at 09:00" fired at 16:00 in Jakarta, silently, for as long as
// nobody compared the two clocks.
//
// Wrapping rather than changing the extractor keeps the stored row honest: it
// records the zone the schedule is actually evaluated in, which is what the
// schedules API and the editor show, instead of leaving the resolution to
// every reader.
//
// An empty or unresolvable instance zone resolves to UTC, which is what an
// unnamed zone already meant. An unresolvable zone is deliberately not written
// into the row either: robfig's parser refuses a `TZ=` naming a zone it cannot
// load, so an operator's typo would stop the schedule firing altogether. A
// wrong label is a far smaller failure than a schedule that never runs.
func DefaultTimezone(extract repository.ScheduleExtractor, zone string) repository.ScheduleExtractor {
	resolved := strings.TrimSpace(zone)
	if resolved != "" {
		if _, err := time.LoadLocation(resolved); err != nil {
			resolved = ""
		}
	}
	return func(document workflow.Document) []repository.ScheduleTrigger {
		triggers := extract(document)
		for index := range triggers {
			if namesNoZone(triggers[index].Timezone) {
				triggers[index].Timezone = resolved
			}
		}
		return triggers
	}
}

// namesNoZone reports a stored zone that leaves the schedule in UTC: absent,
// or n8n's DEFAULT sentinel meaning "whatever the instance uses".
func namesNoZone(zone string) bool {
	trimmed := strings.TrimSpace(zone)
	return trimmed == "" || strings.EqualFold(trimmed, "DEFAULT")
}
