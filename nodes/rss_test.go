package nodes_test

// FEAT-kfmq1z: the RSS pair. The read node fetches a real feed over HTTP —
// these tests serve one from a loopback server — and parses both the RSS 2.0
// and the Atom shape into the items n8n's reader produces. The trigger is
// proven through its poll handler: the GUID watermark decides what is new.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

const rssSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>KilasFlow Blog</title>
    <link>https://blog.example.test</link>
    <description>Notes</description>
    <item>
      <title>First post</title>
      <link>https://blog.example.test/first</link>
      <pubDate>Mon, 26 Oct 2026 08:00:00 +0000</pubDate>
      <guid isPermaLink="false">urn:uuid:1</guid>
      <dc:creator>Ada</dc:creator>
      <category>engineering</category>
      <description>A short teaser</description>
      <content:encoded><![CDATA[<p>The <b>full</b> story of the first post.</p>]]></content:encoded>
    </item>
    <item>
      <title>Second post</title>
      <link>https://blog.example.test/second</link>
      <pubDate>Tue, 27 Oct 2026 08:00:00 +0000</pubDate>
      <guid isPermaLink="false">urn:uuid:2</guid>
      <dc:creator>Grace</dc:creator>
      <category>engineering</category><category>releases</category>
      <description>Another teaser</description>
      <content:encoded><![CDATA[<p>The second post, with more words than a snippet can hold and more words and more words and more words and more words and more words.</p>]]></content:encoded>
    </item>
  </channel>
</rss>`

const atomSample = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>KilasFlow Atom</title>
  <entry>
    <id>urn:uuid:atom-1</id>
    <title>An atom entry</title>
    <link rel="alternate" href="https://blog.example.test/atom-1"/>
    <published>2026-10-26T09:00:00Z</published>
    <updated>2026-10-26T09:30:00Z</updated>
    <author><name>Ada</name></author>
    <summary>Atom summary</summary>
    <content type="html">&lt;p&gt;Atom content&lt;/p&gt;</content>
  </entry>
</feed>`

func feedServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func rssReadIR(t *testing.T, parameters map[string]any) workflow.IRNode {
	t.Helper()
	definition, found := aiRegistry(t).Lookup(nodes.RssFeedReadNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.RssFeedReadNodeType)
	}
	return workflow.IRNode{
		ID: "rss", Name: "RSS Read", Type: nodes.RssFeedReadNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
}

func runRssRead(t *testing.T, parameters map[string]any) (workflow.NodeOutput, error) {
	t.Helper()
	executor := nodes.NewRSSFeedReadExecutor(localPolicy())
	return executor.Execute(context.Background(), rssReadIR(t, parameters), workflow.NodeInput{}, engine.Request{})
}

func TestTheRssNodesAreTheirOwnTypes(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	read, found := registry.Get(nodes.RssFeedReadNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.RssFeedReadNodeType)
	}
	if len(read.Inputs) != 1 || read.Inputs[0].Kind != workflow.ConnectionMain {
		t.Errorf("read inputs = %#v, want one main input", read.Inputs)
	}
	trigger, found := registry.Get(nodes.RssFeedReadTriggerNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.RssFeedReadTriggerNodeType)
	}
	if len(trigger.Inputs) != 0 {
		t.Errorf("trigger inputs = %#v, want none", trigger.Inputs)
	}
	if trigger.Parameters[0].Key != "feedUrl" || trigger.Parameters[0].Default != "https://blog.n8n.io/rss/" {
		t.Errorf("trigger feedUrl = %#v, want n8n's own default", trigger.Parameters[0])
	}
	// The tool form is registered beside its base, with the tool's own naming.
	tool, found := registry.Get(nodes.RssFeedReadToolNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.RssFeedReadToolNodeType)
	}
	if tool.ToolFormOf != nodes.RssFeedReadNodeType {
		t.Errorf("tool form of = %q, want %q", tool.ToolFormOf, nodes.RssFeedReadNodeType)
	}
	if err := tool.Validate(workflow.Node{Parameters: map[string]any{"toolDescription": "read the feed"}}); err != nil {
		t.Errorf("Validate() = %v, want a described tool accepted", err)
	}
	if err := tool.Validate(workflow.Node{Parameters: map[string]any{}}); err == nil || !strings.Contains(err.Error(), "toolDescription is required") {
		t.Errorf("Validate() = %v, want the missing description named", err)
	}
	// The leased poll framework lists exactly what ExtractPolls recognises.
	if _, ok := nodes.PollHandlers(localPolicy())[nodes.RssFeedReadTriggerNodeType]; !ok {
		t.Error("PollHandlers has no RSS handler")
	}
	document := workflow.Document{Nodes: []workflow.Node{{ID: "rss", Type: nodes.RssFeedReadTriggerNodeType, Parameters: map[string]any{}}}}
	if polls := nodes.ExtractPolls(document); len(polls) != 1 || polls[0].NodeType != nodes.RssFeedReadTriggerNodeType {
		t.Fatalf("ExtractPolls() = %#v, want the RSS trigger", polls)
	}
}

func TestRSSReadParsesAnRssFeedIntoN8nShapedItems(t *testing.T) {
	t.Parallel()

	server := feedServer(t, rssSample)
	output, err := runRssRead(t, map[string]any{"url": server.URL, "options": map[string]any{"customFields": "category-extra"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 2 {
		t.Fatalf("output = %#v, want the feed's two entries", output)
	}
	first := output[0][0].JSON
	if first["title"] != "First post" || first["link"] != "https://blog.example.test/first" {
		t.Errorf("first = %#v, want title and link", first)
	}
	if first["guid"] != "urn:uuid:1" {
		t.Errorf("guid = %#v, want the feed's own identity", first["guid"])
	}
	if first["creator"] != "Ada" {
		t.Errorf("creator = %#v, want dc:creator read", first["creator"])
	}
	if !strings.Contains(first["content"].(string), "full") {
		t.Errorf("content = %#v, want content:encoded read", first["content"])
	}
	if !strings.HasPrefix(first["isoDate"].(string), "2026-10-26T08:00:00") {
		t.Errorf("isoDate = %#v, want the parsed date", first["isoDate"])
	}
	snippet, _ := first["contentSnippet"].(string)
	if !strings.Contains(snippet, "full story") || strings.Contains(snippet, "<") {
		t.Errorf("contentSnippet = %q, want the stripped lead", snippet)
	}
	if categories, _ := first["categories"].([]any); len(categories) != 1 || categories[0] != "engineering" {
		t.Errorf("categories = %#v, want the item's one category", first["categories"])
	}
}

func TestRSSReadTruncatesTheSnippetAndParsesAtom(t *testing.T) {
	t.Parallel()

	server := feedServer(t, atomSample)
	output, err := runRssRead(t, map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want the one atom entry", output)
	}
	entry := output[0][0].JSON
	if entry["guid"] != "urn:uuid:atom-1" || entry["id"] != "urn:uuid:atom-1" {
		t.Errorf("identity = %#v, want the atom id under both keys", entry)
	}
	if entry["link"] != "https://blog.example.test/atom-1" {
		t.Errorf("link = %#v, want the alternate link", entry["link"])
	}
	if entry["creator"] != "Ada" {
		t.Errorf("creator = %#v, want the author name", entry["creator"])
	}
	if !strings.Contains(entry["content"].(string), "Atom content") {
		t.Errorf("content = %#v, want the html content read", entry["content"])
	}
	if !strings.HasPrefix(entry["pubDate"].(string), "2026-10-26T09:00:00") {
		t.Errorf("pubDate = %#v, want the published date", entry["pubDate"])
	}

	rss := feedServer(t, rssSample)
	long, err := runRssRead(t, map[string]any{"url": rss.URL})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	snippet, _ := long[0][1].JSON["contentSnippet"].(string)
	if len([]rune(snippet)) != 120 {
		t.Errorf("snippet = %d runes, want the 120-character cut", len([]rune(snippet)))
	}
}

func TestRSSReadRefusesADocumentThatIsNoFeed(t *testing.T) {
	t.Parallel()

	server := feedServer(t, `{"ok": true}`)
	_, err := runRssRead(t, map[string]any{"url": server.URL})
	if err == nil || !strings.Contains(err.Error(), "neither an RSS nor an Atom feed") {
		t.Fatalf("Execute() error = %v, want the non-feed named", err)
	}
}

func TestTheRssPollerWatermarksOnGUIDs(t *testing.T) {
	t.Parallel()

	current := rssSample
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(current))
	}))
	t.Cleanup(server.Close)

	pollerHandler := nodes.NewRSSFeedPoller(localPolicy())
	claim := poller.Claim{
		Node: workflow.Node{
			ID: "rss", Name: "RSS Feed Trigger", Type: nodes.RssFeedReadTriggerNodeType,
			Parameters: map[string]any{"feedUrl": server.URL},
		},
		Request: engine.Request{},
	}

	// First poll: the baseline. The two entries the feed holds are marked and
	// nothing is emitted — activation must not fire for old posts.
	items, cursor, err := pollerHandler.Poll(context.Background(), claim)
	if err != nil {
		t.Fatalf("baseline Poll() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("baseline emitted %d items, want none", len(items))
	}
	if !strings.Contains(cursor, "urn:uuid:1") || !strings.Contains(cursor, "urn:uuid:2") {
		t.Fatalf("cursor = %q, want both GUIDs watermarked", cursor)
	}

	// A new entry appears at the top of the feed; the next poll emits exactly
	// it, and only it.
	current = strings.Replace(rssSample, "    <item>\n      <title>First post</title>",
		`    <item>
      <title>Newest post</title>
      <link>https://blog.example.test/newest</link>
      <pubDate>Wed, 28 Oct 2026 08:00:00 +0000</pubDate>
      <guid isPermaLink="false">urn:uuid:new</guid>
      <description>Fresh</description>
    </item>
    <item>
      <title>First post</title>`, 1)
	claim.Cursor = repository.PollCursor{Cursor: cursor}
	items, next, err := pollerHandler.Poll(context.Background(), claim)
	if err != nil {
		t.Fatalf("second Poll() error = %v", err)
	}
	if len(items) != 1 || items[0].JSON["guid"] != "urn:uuid:new" {
		t.Fatalf("items = %#v, want exactly the new entry", items)
	}
	if !strings.Contains(next, "urn:uuid:new") {
		t.Fatalf("cursor = %q, want the new GUID watermarked", next)
	}

	// Nothing new: no items, cursor held.
	claim.Cursor = repository.PollCursor{Cursor: next}
	items, held, err := pollerHandler.Poll(context.Background(), claim)
	if err != nil {
		t.Fatalf("third Poll() error = %v", err)
	}
	if len(items) != 0 || held != next {
		t.Fatalf("items = %d, cursor = %q, want none and the cursor held", len(items), held)
	}
}

func TestTheRssTriggerEmitsItsInputOnAManualRun(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.RssFeedReadTriggerNodeType, workflow.V(1))
	ir := workflow.IRNode{ID: "rss", Name: "RSS Feed Trigger", Type: nodes.RssFeedReadTriggerNodeType, Definition: definition}
	request := engine.Request{Input: workflow.Item{JSON: map[string]any{"chatInput": "hello"}}}
	output, err := runExecutor(t, executors, nodes.RssFeedReadTriggerExecutorID, ir, workflow.NodeInput{}, request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || output[0][0].JSON["chatInput"] != "hello" {
		t.Fatalf("output = %#v, want the trigger's input item", output)
	}
}
