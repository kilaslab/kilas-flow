package nodes

import (
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// PollHandlers is the poll handler for each trigger type ExtractPolls
// recognises. It is the one list main wires into the poll service, so adding a
// polling trigger means adding it here and in ExtractPolls, and a test holds
// the two together.
func PollHandlers(policy safehttp.Policy) map[string]poller.Handler {
	return map[string]poller.Handler{
		GmailTriggerNodeType:        NewGmailPoller(policy),
		GoogleDriveTriggerNodeType:  NewGoogleDrivePoller(policy),
		GoogleSheetsTriggerNodeType: NewGoogleSheetsPoller(policy),
	}
}

// ExtractPolls turns a document's Gmail, Drive and Sheets triggers into the
// leased cursor rows an activation stores.
func ExtractPolls(document workflow.Document) []repository.PollTrigger {
	triggers := make([]repository.PollTrigger, 0, 1)
	for _, node := range document.Nodes {
		if node.Disabled {
			continue
		}
		switch node.Type {
		case GmailTriggerNodeType, GoogleDriveTriggerNodeType, GoogleSheetsTriggerNodeType:
		default:
			continue
		}
		triggers = append(triggers, repository.PollTrigger{
			NodeID: node.ID, NodeType: node.Type, Interval: PollInterval(node.Parameters),
		})
	}
	return triggers
}

// PollInterval reads n8n's pollTimes collection. Missing or unrecognised
// values default to one minute so a trigger still ticks.
func PollInterval(parameters map[string]any) time.Duration {
	raw, _ := parameters["pollTimes"].(map[string]any)
	items, _ := raw["item"].([]any)
	if len(items) == 0 {
		return time.Minute
	}
	first, _ := items[0].(map[string]any)
	switch strings.ToLower(strings.TrimSpace(textValue(first["mode"], ""))) {
	case "everyhour":
		return time.Hour
	case "everyx", "everyxminutes":
		value := int(numberValue(first["value"]))
		if value <= 0 {
			value = 1
		}
		unit := strings.ToLower(strings.TrimSpace(textValue(first["unit"], "minutes")))
		switch unit {
		case "hours", "hour":
			return time.Duration(value) * time.Hour
		case "seconds", "second":
			if value < 15 {
				value = 15
			}
			return time.Duration(value) * time.Second
		default:
			return time.Duration(value) * time.Minute
		}
	case "everyweekday", "everyday", "everymonth", "cron":
		return time.Minute
	default:
		return time.Minute
	}
}
