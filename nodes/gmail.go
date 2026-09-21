package nodes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	GmailExecutorID        = "core.gmail"
	GmailTriggerExecutorID = "core.gmailTrigger"
	GmailNodeType          = "kilasflow.gmail"
	GmailTriggerNodeType   = "kilasflow.gmailTrigger"
	GmailCredentialType    = "gmailOAuth2"
)

func gmailNode() node.Definition {
	return node.Definition{
		Type:        GmailNodeType,
		Version:     workflow.V(1),
		DisplayName: "Gmail",
		Description: "Gets and sends Gmail messages. Drafts, labels, replies and attachments are not in this slice.",
		Category:    "Communication",
		Group:       []node.NodeGroup{node.GroupOutput},
		Icon:        &node.NodeIcon{Light: "builtin:mail"},
		IconColor:   "#ea4335",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: GmailCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Default: "message",
				Options: []node.PropertyOption{{Label: "Message", Value: "message"}},
			},
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Default: "get",
				Options: []node.PropertyOption{
					{Label: "Get", Value: "get"},
					{Label: "Get Many", Value: "getAll"},
					{Label: "Send", Value: "send"},
				},
			},
			{Key: "messageId", Label: "Message ID", Kind: node.PropertyString},
			{Key: "simple", Label: "Simplify", Kind: node.PropertyBoolean, Default: true},
			{Key: "to", Label: "To", Kind: node.PropertyString},
			{Key: "subject", Label: "Subject", Kind: node.PropertyString},
			{Key: "message", Label: "Message", Kind: node.PropertyString},
			{Key: "q", Label: "Search", Kind: node.PropertyString},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     GmailExecutorID,
	}
}

func gmailTrigger() node.Definition {
	return node.Definition{
		Type:        GmailTriggerNodeType,
		Version:     workflow.V(1),
		DisplayName: "Gmail Trigger",
		Description: "Starts a workflow when Gmail receives a message. Polls users.messages with a leased cursor so two replicas cannot emit the same email twice.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:mail"},
		IconColor:   "#ea4335",
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: GmailCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{Key: "simple", Label: "Simplify", Kind: node.PropertyBoolean, Default: true},
			{Key: "filters", Label: "Filters", Kind: node.PropertyCollection},
			{Key: "q", Label: "Search", Kind: node.PropertyString},
			{Key: "pollTimes", Label: "Poll Times", Kind: node.PropertyCollection},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     GmailTriggerExecutorID,
	}
}

// GmailExecutor runs Get / Get Many / Send.
type GmailExecutor struct {
	google *GoogleClient
}

// NewGmailExecutor builds the Gmail action node.
func NewGmailExecutor(policy safehttp.Policy) *GmailExecutor {
	return &GmailExecutor{google: NewGoogleClient(policy)}
}

func (executor *GmailExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{}}
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		resolved, err := resolveGoogleIR(ir, item, input, request, index)
		if err != nil {
			return nil, err
		}
		produced, err := executor.executeItem(ctx, resolved, item, request)
		if err != nil {
			return nil, err
		}
		output = append(output, produced...)
	}
	return workflow.NodeOutput{output}, nil
}

func (executor *GmailExecutor) executeItem(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	operation := strings.ToLower(strings.TrimSpace(textValue(ir.Parameters["operation"], "get")))
	switch operation {
	case "get":
		return executor.get(ctx, ir, item, request)
	case "getall", "getmany":
		return executor.list(ctx, ir, item, request)
	case "send":
		return executor.send(ctx, ir, item, request)
	default:
		return nil, fmt.Errorf("node %q: unsupported gmail operation %q", ir.Name, operation)
	}
}

func (executor *GmailExecutor) get(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	messageID := locatorValue(ir.Parameters["messageId"])
	if messageID == "" {
		messageID = locatorValue(item.JSON["id"])
	}
	if messageID == "" {
		return nil, fmt.Errorf("node %q: messageId is required", ir.Name)
	}
	payload, err := executor.fetchMessage(ctx, ir, request, messageID)
	if err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: payload, Paired: item.Paired}}, nil
}

func (executor *GmailExecutor) list(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	params := url.Values{}
	if q := gmailQuery(ir); q != "" {
		params.Set("q", q)
	}
	params.Set("maxResults", "50")
	var listed struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := executor.google.json(ctx, request, ir, GmailCredentialType, http.MethodGet, gmailAPIRoot+"/users/me/messages", params, nil, &listed); err != nil {
		return nil, err
	}
	items := make([]workflow.Item, 0, len(listed.Messages))
	for _, message := range listed.Messages {
		payload, err := executor.fetchMessage(ctx, ir, request, message.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, workflow.Item{JSON: payload, Paired: item.Paired})
	}
	if len(items) == 0 {
		return []workflow.Item{{JSON: map[string]any{}, Paired: item.Paired}}, nil
	}
	return items, nil
}

func (executor *GmailExecutor) send(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	to := strings.TrimSpace(textValue(ir.Parameters["to"], textValue(item.JSON["to"], "")))
	subject := strings.TrimSpace(textValue(ir.Parameters["subject"], textValue(item.JSON["subject"], "")))
	body := textValue(ir.Parameters["message"], textValue(item.JSON["message"], textValue(item.JSON["text"], "")))
	if to == "" {
		return nil, fmt.Errorf("node %q: to is required to send", ir.Name)
	}
	raw := "To: " + to + "\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body
	encoded := base64.RawURLEncoding.EncodeToString([]byte(raw))
	var payload map[string]any
	if err := executor.google.json(ctx, request, ir, GmailCredentialType, http.MethodPost, gmailAPIRoot+"/users/me/messages/send", nil, map[string]any{"raw": encoded}, &payload); err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: payload, Paired: item.Paired}}, nil
}

func (executor *GmailExecutor) fetchMessage(ctx context.Context, ir workflow.IRNode, request engine.Request, messageID string) (map[string]any, error) {
	return fetchGmailMessage(ctx, executor.google, ir, request, messageID)
}

func fetchGmailMessage(ctx context.Context, google *GoogleClient, ir workflow.IRNode, request engine.Request, messageID string) (map[string]any, error) {
	query := url.Values{}
	query.Set("format", "full")
	var payload map[string]any
	if err := google.json(ctx, request, ir, GmailCredentialType, http.MethodGet, gmailAPIRoot+"/users/me/messages/"+url.PathEscape(messageID), query, nil, &payload); err != nil {
		return nil, err
	}
	if gmailSimple(ir.Parameters) {
		return simplifyGmailMessage(payload), nil
	}
	return payload, nil
}

func gmailSimple(parameters map[string]any) bool {
	raw, ok := parameters["simple"]
	if !ok || raw == nil {
		return true
	}
	if typed, isBool := raw.(bool); isBool {
		return typed
	}
	return boolValue(raw)
}

func simplifyGmailMessage(raw map[string]any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	simplified := map[string]any{
		"id":           raw["id"],
		"threadId":     raw["threadId"],
		"labelIds":     raw["labelIds"],
		"snippet":      raw["snippet"],
		"historyId":    raw["historyId"],
		"sizeEstimate": raw["sizeEstimate"],
		"internalDate": raw["internalDate"],
	}
	payload, _ := raw["payload"].(map[string]any)
	headers := gmailHeaderMap(payload)
	simplified["headers"] = headers
	if subject := strings.TrimSpace(textValue(headers["subject"], "")); subject != "" {
		simplified["subject"] = subject
	}
	text, htmlBody := gmailBodies(payload)
	if strings.TrimSpace(text) == "" {
		text = stripGmailHTML(htmlBody)
	}
	if strings.TrimSpace(text) == "" {
		text = strings.TrimSpace(textValue(raw["snippet"], ""))
	}
	simplified["text"] = text
	if htmlBody != "" {
		simplified["html"] = htmlBody
	}
	return simplified
}

func gmailHeaderMap(payload map[string]any) map[string]any {
	headers := map[string]any{}
	if payload == nil {
		return headers
	}
	list, _ := payload["headers"].([]any)
	for _, entry := range list {
		row, _ := entry.(map[string]any)
		name := strings.ToLower(strings.TrimSpace(textValue(row["name"], "")))
		if name == "" {
			continue
		}
		headers[name] = textValue(row["value"], "")
	}
	return headers
}

func gmailBodies(part map[string]any) (text, html string) {
	if part == nil {
		return "", ""
	}
	mime := strings.ToLower(strings.TrimSpace(textValue(part["mimeType"], "")))
	body, _ := part["body"].(map[string]any)
	decoded := decodeGmailBody(textValue(body["data"], ""))
	switch mime {
	case "text/plain":
		text = decoded
	case "text/html":
		html = decoded
	}
	parts, _ := part["parts"].([]any)
	for _, child := range parts {
		childPart, _ := child.(map[string]any)
		childText, childHTML := gmailBodies(childPart)
		if text == "" {
			text = childText
		}
		if html == "" {
			html = childHTML
		}
	}
	return text, html
}

func decodeGmailBody(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(data)
	}
	if err != nil {
		return ""
	}
	return string(decoded)
}

var gmailHTMLTag = regexp.MustCompile(`(?s)<[^>]*>`)

func stripGmailHTML(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	stripped := gmailHTMLTag.ReplaceAllString(raw, " ")
	stripped = html.UnescapeString(stripped)
	return strings.Join(strings.Fields(stripped), " ")
}

func gmailQuery(ir workflow.IRNode) string {
	if q := strings.TrimSpace(textValue(ir.Parameters["q"], "")); q != "" {
		return q
	}
	filters, _ := ir.Parameters["filters"].(map[string]any)
	if filters == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if q := strings.TrimSpace(textValue(filters["q"], textValue(filters["search"], ""))); q != "" {
		parts = append(parts, q)
	}
	if sender := strings.TrimSpace(textValue(filters["sender"], "")); sender != "" {
		parts = append(parts, "from:"+sender)
	}
	if label := strings.TrimSpace(textValue(filters["labelIds"], textValue(filters["label"], ""))); label != "" {
		parts = append(parts, "label:"+label)
	}
	if read := strings.ToLower(strings.TrimSpace(textValue(filters["readStatus"], ""))); read == "unread" {
		parts = append(parts, "is:unread")
	} else if read == "read" {
		parts = append(parts, "is:read")
	}
	return strings.Join(parts, " ")
}

// GmailTriggerExecutor emits the polled message on a manual run.
type GmailTriggerExecutor struct{}

func (GmailTriggerExecutor) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	return workflow.NodeOutput{{item}}, nil
}

// GmailPoller lists new Gmail messages since the last leased cursor.
type GmailPoller struct {
	google *GoogleClient
}

// NewGmailPoller builds the Gmail trigger poll handler.
func NewGmailPoller(policy safehttp.Policy) *GmailPoller {
	return &GmailPoller{google: NewGoogleClient(policy)}
}

type gmailPollCursor struct {
	HistoryID string   `json:"historyId"`
	Seen      []string `json:"seen"`
}

func (pollerHandler *GmailPoller) Poll(ctx context.Context, claim poller.Claim) ([]workflow.Item, string, error) {
	ir := irFromNode(claim.Node)
	if strings.TrimSpace(claim.Cursor.Cursor) == "" {
		return pollerHandler.bootstrap(ctx, claim, ir)
	}
	var cursor gmailPollCursor
	_ = json.Unmarshal([]byte(claim.Cursor.Cursor), &cursor)
	if strings.TrimSpace(cursor.HistoryID) != "" {
		items, next, err := pollerHandler.pollHistory(ctx, claim, ir, cursor)
		if err == nil {
			return items, next, nil
		}
		if !googleAPIStatus(err, 404) {
			return nil, claim.Cursor.Cursor, err
		}
	}
	return pollerHandler.pollListed(ctx, claim, ir, cursor)
}

func (pollerHandler *GmailPoller) bootstrap(ctx context.Context, claim poller.Claim, ir workflow.IRNode) ([]workflow.Item, string, error) {
	listed, err := pollerHandler.listMessages(ctx, claim, ir)
	if err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	nextSeen := make([]string, 0, len(listed))
	for _, message := range listed {
		nextSeen = append(nextSeen, message.ID)
	}
	encoded, _ := json.Marshal(gmailPollCursor{HistoryID: pollerHandler.profileHistoryID(ctx, claim, ir), Seen: nextSeen})
	return nil, string(encoded), nil
}

func (pollerHandler *GmailPoller) pollHistory(ctx context.Context, claim poller.Claim, ir workflow.IRNode, cursor gmailPollCursor) ([]workflow.Item, string, error) {
	params := url.Values{}
	params.Set("startHistoryId", cursor.HistoryID)
	params.Add("historyTypes", "messageAdded")
	contents, _, err := pollerHandler.google.do(ctx, claim.Request, ir, GmailCredentialType, http.MethodGet, gmailAPIRoot+"/users/me/history", params, nil)
	if err != nil {
		return nil, "", err
	}
	var payload map[string]any
	if len(bytes.TrimSpace(contents)) > 0 {
		if err := json.Unmarshal(contents, &payload); err != nil {
			return nil, "", fmt.Errorf("node %q: decode google json: %w", ir.Name, err)
		}
	}
	seen := map[string]struct{}{}
	for _, id := range cursor.Seen {
		seen[id] = struct{}{}
	}
	items := make([]workflow.Item, 0)
	nextSeen := append([]string{}, cursor.Seen...)
	history, _ := payload["history"].([]any)
	for _, entry := range history {
		row, _ := entry.(map[string]any)
		added, _ := row["messagesAdded"].([]any)
		for _, addedEntry := range added {
			addedRow, _ := addedEntry.(map[string]any)
			message, _ := addedRow["message"].(map[string]any)
			id := strings.TrimSpace(textValue(message["id"], ""))
			if id == "" {
				continue
			}
			if _, already := seen[id]; already {
				continue
			}
			items = append(items, workflow.Item{JSON: map[string]any{
				"id": id, "threadId": textValue(message["threadId"], ""),
			}})
			nextSeen = append(nextSeen, id)
			seen[id] = struct{}{}
		}
	}
	if len(nextSeen) > 200 {
		nextSeen = nextSeen[len(nextSeen)-200:]
	}
	historyID := strings.TrimSpace(textValue(payload["historyId"], cursor.HistoryID))
	encoded, _ := json.Marshal(gmailPollCursor{HistoryID: historyID, Seen: nextSeen})
	return items, string(encoded), nil
}

func (pollerHandler *GmailPoller) pollListed(ctx context.Context, claim poller.Claim, ir workflow.IRNode, cursor gmailPollCursor) ([]workflow.Item, string, error) {
	listed, err := pollerHandler.listMessages(ctx, claim, ir)
	if err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	seen := map[string]struct{}{}
	for _, id := range cursor.Seen {
		seen[id] = struct{}{}
	}
	items := make([]workflow.Item, 0, len(listed))
	nextSeen := append([]string{}, cursor.Seen...)
	for _, message := range listed {
		if _, already := seen[message.ID]; already {
			continue
		}
		items = append(items, workflow.Item{JSON: map[string]any{"id": message.ID, "threadId": message.ThreadID}})
		nextSeen = append(nextSeen, message.ID)
		seen[message.ID] = struct{}{}
	}
	if len(nextSeen) > 200 {
		nextSeen = nextSeen[len(nextSeen)-200:]
	}
	encoded, _ := json.Marshal(gmailPollCursor{HistoryID: cursor.HistoryID, Seen: nextSeen})
	return items, string(encoded), nil
}

type gmailListedMessage struct {
	ID       string
	ThreadID string
}

func (pollerHandler *GmailPoller) listMessages(ctx context.Context, claim poller.Claim, ir workflow.IRNode) ([]gmailListedMessage, error) {
	params := url.Values{}
	if q := gmailQuery(ir); q != "" {
		params.Set("q", q)
	}
	params.Set("maxResults", "25")
	var listed struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := pollerHandler.google.json(ctx, claim.Request, ir, GmailCredentialType, http.MethodGet, gmailAPIRoot+"/users/me/messages", params, nil, &listed); err != nil {
		return nil, err
	}
	out := make([]gmailListedMessage, 0, len(listed.Messages))
	for _, message := range listed.Messages {
		out = append(out, gmailListedMessage{ID: message.ID, ThreadID: message.ThreadID})
	}
	return out, nil
}

func (pollerHandler *GmailPoller) profileHistoryID(ctx context.Context, claim poller.Claim, ir workflow.IRNode) string {
	var profile map[string]any
	if err := pollerHandler.google.json(ctx, claim.Request, ir, GmailCredentialType, http.MethodGet, gmailAPIRoot+"/users/me/profile", nil, nil, &profile); err != nil {
		return ""
	}
	return strings.TrimSpace(textValue(profile["historyId"], ""))
}

func googleAPIStatus(err error, status int) bool {
	if err == nil {
		return false
	}
	needle := fmt.Sprintf("google api status %d", status)
	return strings.Contains(err.Error(), needle)
}
