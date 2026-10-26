package nodes

// The RSS pair (FEAT-kfmq1z): RSS Read as an ordinary node — it fetches a
// feed over HTTP through the same policy-bound client every outbound request
// uses, and parses it with a small pure-Go RSS 2.0 / Atom reader — and the
// RSS Feed Trigger on the leased poll framework, with a GUID watermark so
// two replicas cannot emit one entry twice.
//
// The parameter keys are n8n's own (`url`, `options.customFields` on the
// read node; `feedUrl`, `pollTimes` on the trigger), so an imported node maps
// by type name alone. n8n's `options.ignoreSSL` is not carried: this server's
// outbound policy checks certificates on every request, and the importer
// says so rather than quietly dropping the flag.
import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	RssFeedReadExecutorID        = "core.rssFeedRead"
	RssFeedReadTriggerExecutorID = "core.rssFeedReadTrigger"

	RssFeedReadNodeType        = "kilasflow.rssFeedRead"
	RssFeedReadTriggerNodeType = "kilasflow.rssFeedReadTrigger"
	// RssFeedReadToolNodeType is the agent-tool form of the read node; it is
	// registered by the tool-form table.
	RssFeedReadToolNodeType = "kilasflow.rssFeedReadTool"
)

// rssPollSeen caps the GUID watermark's memory: a feed that turns over fast
// keeps the newest entries' identities and forgets the rest, which is the
// same bound the Gmail poller's seen-list works to.
const rssPollSeen = 200

// rssPollBatch caps how many entries one tick emits.
const rssPollBatch = 20

func rssFeedReadNode() node.Definition {
	return node.Definition{
		Type:        RssFeedReadNodeType,
		Version:     workflow.V(1),
		DisplayName: "RSS Read",
		Description: "Reads the entries of an RSS or Atom feed into items.",
		Category:    "Core",
		Group:       []node.NodeGroup{node.GroupInput},
		Icon:        &node.NodeIcon{Light: "rssFeed"},
		IconLight:   rssArtwork("feed"),
		IconColor:   "#ea580c",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "url", Label: "URL", Kind: node.PropertyString, Required: true,
				Description: "The feed's address. Supports expressions, so one node can read a feed per item.",
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "customFields", Label: "Custom Fields", Kind: node.PropertyString,
						Description: "Extra entry elements to include in the output, comma-separated: author, dc:date, whatever the feed carries.",
					},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     RssFeedReadExecutorID,
	}
}

func rssFeedReadTriggerNode() node.Definition {
	return node.Definition{
		Type:        RssFeedReadTriggerNodeType,
		Version:     workflow.V(1),
		DisplayName: "RSS Feed Trigger",
		Description: "Starts a workflow when a feed publishes a new entry. Polls with a leased cursor and a GUID watermark.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "rssFeedTrigger"},
		IconLight:   rssArtwork("feedTrigger"),
		IconColor:   "#ea580c",
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "feedUrl", Label: "Feed URL", Kind: node.PropertyString, Required: true,
				Default:     "https://blog.n8n.io/rss/",
				Description: "The feed to poll. Supports expressions.",
			},
			{Key: "pollTimes", Label: "Poll Times", Kind: node.PropertyCollection},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     RssFeedReadTriggerExecutorID,
	}
}

// RSSFeedReadExecutor fetches and parses one feed per item.
type RSSFeedReadExecutor struct {
	policy safehttp.Policy
	client *http.Client
}

// NewRSSFeedReadExecutor builds the node from the deployment's outbound policy.
func NewRSSFeedReadExecutor(policy safehttp.Policy) *RSSFeedReadExecutor {
	return &RSSFeedReadExecutor{policy: policy, client: safehttp.NewClient(policy)}
}

func (executor *RSSFeedReadExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{}}
	}
	output := []workflow.Item{}
	for index, item := range items {
		resolved, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		feed, err := fetchFeed(ctx, executor.policy, executor.client, textValue(resolved["url"], ""))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		entries := feedEntries(feed, customFieldList(mapValue(resolved["options"])["customFields"]))
		for _, entry := range entries {
			output = append(output, workflow.Item{JSON: entry, Paired: item.Paired})
		}
	}
	return workflow.NodeOutput{output}, nil
}

// RSSFeedTriggerExecutor emits the polled entry on a manual run.
type RSSFeedTriggerExecutor struct{}

func (RSSFeedTriggerExecutor) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	return workflow.NodeOutput{{item}}, nil
}

// RSSFeedPoller fetches new entries above the GUID watermark.
type RSSFeedPoller struct {
	policy safehttp.Policy
	client *http.Client
}

// NewRSSFeedPoller builds the trigger's poll handler.
func NewRSSFeedPoller(policy safehttp.Policy) *RSSFeedPoller {
	return &RSSFeedPoller{policy: policy, client: safehttp.NewClient(policy)}
}

// rssPollCursor is the leased watermark: the GUIDs already emitted. Dates are
// deliberately not used — n8n's trigger watermarks the newest entry date,
// which re-emits an older-but-newly-published entry and misses same-second
// ones; a feed entry's GUID or link is the identity the feed itself declares.
type rssPollCursor struct {
	Seen []string `json:"seen"`
}

func (pollerHandler *RSSFeedPoller) Poll(ctx context.Context, claim poller.Claim) ([]workflow.Item, string, error) {
	feed, err := fetchFeed(ctx, pollerHandler.policy, pollerHandler.client, textValue(claim.Node.Parameters["feedUrl"], ""))
	if err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	entries := feedEntries(feed, customFieldList(mapValue(claim.Node.Parameters["options"])["customFields"]))
	var cursor rssPollCursor
	if strings.TrimSpace(claim.Cursor.Cursor) != "" {
		_ = json.Unmarshal([]byte(claim.Cursor.Cursor), &cursor)
	}
	seen := map[string]bool{}
	for _, guid := range cursor.Seen {
		seen[guid] = true
	}
	// No baseline yet: everything the feed holds becomes the baseline, so
	// activation emits only what is published afterwards.
	if len(cursor.Seen) == 0 {
		next := rssPollCursor{}
		for _, entry := range entries {
			next.Seen = append(next.Seen, entryGUID(entry))
		}
		next.cap()
		encoded, _ := json.Marshal(next)
		return nil, string(encoded), nil
	}

	items := []workflow.Item{}
	next := rssPollCursor{Seen: append([]string{}, cursor.Seen...)}
	for _, entry := range entries {
		guid := entryGUID(entry)
		if guid == "" || seen[guid] {
			continue
		}
		if len(items) < rssPollBatch {
			items = append(items, workflow.Item{JSON: entry})
		}
		next.Seen = append(next.Seen, guid)
		seen[guid] = true
	}
	next.cap()
	encoded, _ := json.Marshal(next)
	return items, string(encoded), nil
}

// entryGUID is a feed entry's own identity: its GUID on RSS, its id on Atom,
// and its link when the feed declares neither.
func entryGUID(entry map[string]any) string {
	for _, key := range []string{"guid", "id", "link"} {
		if text, isText := entry[key].(string); isText && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func (cursor *rssPollCursor) cap() {
	if len(cursor.Seen) > rssPollSeen {
		cursor.Seen = cursor.Seen[len(cursor.Seen)-rssPollSeen:]
	}
}

// customFieldList splits n8n's comma-separated custom field list.
func customFieldList(value any) []string {
	fields := []string{}
	for _, entry := range strings.Split(textValue(value, ""), ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	return fields
}

// fetchFeed makes the one request both nodes run: the feed's own accept
// headers, through the policy-bound client, with the body read under the
// deployment's size cap.
func fetchFeed(ctx context.Context, policy safehttp.Policy, client *http.Client, rawURL string) (any, error) {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return nil, fmt.Errorf("the feed URL is empty")
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("the feed URL %q is not a valid URL", target)
	}
	if err := policy.CheckURL(parsed); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("the feed request could not be built")
	}
	// n8n's reader sends these; feeds that vary their answer by client see
	// the same client here.
	request.Header.Set("User-Agent", "rss-parser")
	request.Header.Set("Accept", "application/rss+xml, application/rdf+xml;q=0.8, application/atom+xml;q=0.6, application/xml;q=0.4, text/xml;q=0.4")
	response, err := client.Do(request)
	if err != nil {
		return nil, safehttp.RedactError(err)
	}
	defer response.Body.Close()
	body, truncated, err := policy.ReadBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read the feed: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("the feed exceeds the configured size limit")
	}
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("the feed answered %d", response.StatusCode)
	}
	feed, err := parseFeed(body)
	if err != nil {
		return nil, err
	}
	return feed, nil
}

// --- The feed reader ---------------------------------------------------------

// rssDocument and atomDocument are the two shapes a feed has. The parser
// reads whichever the document is and answers one list of entry maps shaped
// the way n8n's rss-parser shapes them, so a workflow written against either
// feed reads the same items.
type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	GUID    string `xml:"guid"`
	// dc:creator and content:encoded arrive under their namespace, whose
	// local names are creator and encoded.
	Creator     string    `xml:"creator"`
	Content     string    `xml:"encoded"`
	Description string    `xml:"description"`
	Categories  []string  `xml:"category"`
	Extras      []xmlNode `xml:",any"`
}

type atomDocument struct {
	XMLName xml.Name    `xml:"feed"`
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID        string     `xml:"id"`
	Title     string     `xml:"title"`
	Updated   string     `xml:"updated"`
	Published string     `xml:"published"`
	Links     []atomLink `xml:"link"`
	Author    *struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Content struct {
		Type string `xml:"type,attr"`
		Body string `xml:",chardata"`
	} `xml:"content"`
	Summary    string    `xml:"summary"`
	Categories []string  `xml:"category"`
	Extras     []xmlNode `xml:",any"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

// xmlNode is one unmatched element, kept so a custom field the feed carries
// and the standard shape does not name can still be read.
type xmlNode struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

// parseFeed decodes one feed document.
func parseFeed(body []byte) (any, error) {
	trimmed := bytes.TrimSpace(body)
	var rss rssDocument
	if err := xml.Unmarshal(trimmed, &rss); err == nil && rss.Channel.Title != "" || len(rss.Channel.Items) > 0 {
		return rss, nil
	}
	var atom atomDocument
	if err := xml.Unmarshal(trimmed, &atom); err == nil && (atom.Title != "" || len(atom.Entries) > 0) {
		return atom, nil
	}
	return nil, fmt.Errorf("this document is neither an RSS nor an Atom feed")
}

// feedEntries renders one feed's entries in n8n's reader's shape: title,
// link, pubDate, the parsed isoDate, creator, content, a plain-text
// contentSnippet, guid and categories, plus the custom fields asked for.
func feedEntries(feed any, customFields []string) []map[string]any {
	entries := []map[string]any{}
	switch document := feed.(type) {
	case rssDocument:
		for _, item := range document.Channel.Items {
			entry := map[string]any{
				"title":          item.Title,
				"link":           item.Link,
				"pubDate":        item.PubDate,
				"creator":        item.Creator,
				"content":        item.Content,
				"guid":           item.GUID,
				"categories":     toAnySlice(item.Categories),
				"contentSnippet": contentSnippet(firstNonEmpty(item.Content, item.Description)),
			}
			if item.Content == "" && item.Description != "" {
				entry["content"] = item.Description
			}
			if iso := isoDateOf(item.PubDate); iso != "" {
				entry["isoDate"] = iso
			}
			carryCustomFields(entry, item.Extras, customFields)
			entries = append(entries, entry)
		}
	case atomDocument:
		for _, item := range document.Entries {
			link := ""
			for _, candidate := range item.Links {
				if candidate.Rel == "" || candidate.Rel == "alternate" {
					link = candidate.Href
					break
				}
			}
			published := firstNonEmpty(item.Published, item.Updated)
			entry := map[string]any{
				"title":          item.Title,
				"link":           link,
				"pubDate":        published,
				"id":             item.ID,
				"guid":           item.ID,
				"creator":        atomAuthor(item),
				"content":        item.Content.Body,
				"categories":     toAnySlice(item.Categories),
				"contentSnippet": contentSnippet(firstNonEmpty(item.Content.Body, item.Summary)),
			}
			if iso := isoDateOf(published); iso != "" {
				entry["isoDate"] = iso
			}
			carryCustomFields(entry, item.Extras, customFields)
			entries = append(entries, entry)
		}
	}
	return entries
}

func atomAuthor(item atomEntry) string {
	if item.Author != nil {
		return item.Author.Name
	}
	return ""
}

// carryCustomFields copies the extra elements the author asked for into the
// entry, under the element's own name — the behaviour n8n's customFields
// option buys, on the fields this parser already read.
func carryCustomFields(entry map[string]any, extras []xmlNode, customFields []string) {
	if len(customFields) == 0 {
		return
	}
	wanted := map[string]bool{}
	for _, field := range customFields {
		wanted[strings.ToLower(strings.TrimSpace(field))] = true
	}
	for _, extra := range extras {
		name := strings.ToLower(extra.XMLName.Local)
		if wanted[name] && strings.TrimSpace(extra.Value) != "" {
			entry[name] = extra.Value
		}
	}
}

func toAnySlice(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var htmlTag = regexp.MustCompile(`(?s)<[^>]*>`)

// contentSnippet is the plain-text lead of an HTML body, the way rss-parser
// writes it: tags stripped, entities unescaped, whitespace folded, 120
// characters at most.
func contentSnippet(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	text := strings.Join(strings.Fields(htmlTag.ReplaceAllString(body, " ")), " ")
	text = html.UnescapeString(text)
	runes := []rune(text)
	if len(runes) > 120 {
		text = string(runes[:120])
	}
	return strings.TrimSpace(text)
}

// feedDateLayouts are the date spellings feeds actually write.
var feedDateLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02",
}

// isoDateOf parses a feed date into RFC 3339, or leaves it out when the feed
// wrote something none of the layouts read.
func isoDateOf(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, layout := range feedDateLayouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// rssArtwork is the small monochrome mark each RSS node ships as its own
// artwork — the classic broadcast arcs — served by the icon route without an
// editor change, because no shipped glyph is free.
func rssArtwork(key string) *node.IconAsset {
	marks := map[string]string{
		// The broadcast arcs: a dot and two quarters.
		"feed": `<path d="M4 11a9 9 0 0 1 9 9"/><path d="M4 4a16 16 0 0 1 16 16"/><circle cx="5" cy="19" r="2"/>`,
		// The same arcs with the clock hand the trigger adds.
		"feedTrigger": `<path d="M4 11a9 9 0 0 1 9 9"/><path d="M4 4a16 16 0 0 1 16 16"/><circle cx="5" cy="19" r="2"/><path d="M13 15l3-3"/>`,
	}
	body, drawn := marks[key]
	if !drawn {
		return nil
	}
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes:     []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`),
	}
}
