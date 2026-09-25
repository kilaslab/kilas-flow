package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// TelegramFileClient resolves and downloads a bot's files.
//
// It is a small type rather than a set of free functions because the two calls
// are a pair — getFile returns a path that is only valid for about an hour, and
// the download has to happen against the same token — and because the egress
// policy has to reach both.
type TelegramFileClient struct {
	policy safehttp.Policy
	client *http.Client
}

// NewTelegramFileClient builds the client the trigger downloads through.
func NewTelegramFileClient(policy safehttp.Policy) *TelegramFileClient {
	return &TelegramFileClient{policy: policy, client: safehttp.NewClient(policy)}
}

// TelegramTriggerExecutor emits the update, with any downloaded file attached.
//
// The file client is a field rather than a package-level variable: an executor
// is registered once per composition, two of them can legitimately exist in one
// process (a test, an embedded host), and a global would make the second one
// silently reconfigure the first.
type TelegramTriggerExecutor struct {
	files *TelegramFileClient
}

// NewTelegramTriggerExecutor builds it. A nil client leaves downloads
// unavailable and says so rather than pretending.
func NewTelegramTriggerExecutor(files *TelegramFileClient) *TelegramTriggerExecutor {
	return &TelegramTriggerExecutor{files: files}
}

// Execute emits the update on the node's one output.
func (executor *TelegramTriggerExecutor) Execute(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	if err := executor.download(ctx, ir, &item, request); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{{item}}, nil
}

// download attaches the update's photo or file, when asked to.
func (executor *TelegramTriggerExecutor) download(ctx context.Context, ir workflow.IRNode, item *workflow.Item, request engine.Request) error {
	additional, _ := ir.Parameters["additionalFields"].(map[string]any)
	if wanted, _ := additional["download"].(bool); !wanted {
		return nil
	}
	size, _ := additional["imageSize"].(string)

	fileID, fileName, found := telegramFileID(item.JSON, size)
	if !found {
		// Most updates carry no file. That is the ordinary case, not a problem.
		return nil
	}
	if request.Binaries == nil {
		return fmt.Errorf("node %q: %w", ir.Name, binary.ErrNotConfigured)
	}
	if executor.files == nil {
		return fmt.Errorf("node %q: this deployment cannot download Telegram files", ir.Name)
	}
	resolved, _, _, err := request.ResolveNodeCredential(ctx, ir)
	if err != nil {
		return err
	}
	token := resolved.Fields["accessToken"]
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("node %q: this trigger needs a Telegram credential to download files", ir.Name)
	}

	// The token rides in every download URL's path, so the credential's domain
	// bound is checked against the host it is about to be sent to, and its
	// redirect scope rides on the context each request is built from — the
	// same two checks the engine applies to a node's own request.
	baseURL := TelegramBaseURL(resolved.Fields["baseUrl"])
	if base, parseErr := url.Parse(baseURL); parseErr == nil && !resolved.AllowsHost(base.Host) {
		return fmt.Errorf("node %q: credential %q is not allowed for host %q", ir.Name, resolved.Name, base.Hostname())
	}
	ctx = safehttp.WithCredentialScope(ctx, resolved.RedirectScope())
	contents, remoteName, err := executor.files.Fetch(ctx, baseURL, token, fileID)
	if err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if fileName == "" {
		fileName = remoteName
	}
	reference, err := request.Binaries.Put(fileName, "", bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("node %q: store the downloaded file: %w", ir.Name, err)
	}
	if item.Binary == nil {
		item.Binary = map[string]workflow.BinaryRef{}
	}
	item.Binary["data"] = reference
	return nil
}

// Fetch resolves a file id and downloads the file.
func (client *TelegramFileClient) Fetch(ctx context.Context, baseURL, token, fileID string) ([]byte, string, error) {
	remotePath, err := client.filePath(ctx, baseURL, token, fileID)
	if err != nil {
		return nil, "", err
	}
	// The download URL is built here from the credential's own base and the
	// path the API returned, so a hostile `file_path` can only ever move within
	// that host.
	target, err := telegramEndpoint(baseURL, "/file/bot"+token+"/"+strings.TrimPrefix(remotePath, "/"))
	if err != nil {
		return nil, "", err
	}
	contents, err := client.get(ctx, target)
	if err != nil {
		return nil, "", err
	}
	return contents, path.Base(remotePath), nil
}

func (client *TelegramFileClient) filePath(ctx context.Context, baseURL, token, fileID string) (string, error) {
	target, err := telegramEndpoint(baseURL, "/bot"+token+"/getFile")
	if err != nil {
		return "", err
	}
	target.RawQuery = url.Values{"file_id": {fileID}}.Encode()
	contents, err := client.get(ctx, target)
	if err != nil {
		return "", err
	}
	var answer struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(contents, &answer); err != nil {
		return "", fmt.Errorf("getFile returned something that is not JSON: %w", err)
	}
	if !answer.OK || answer.Result.FilePath == "" {
		reason := answer.Description
		if reason == "" {
			reason = "the Bot API returned no file path"
		}
		return "", fmt.Errorf("getFile failed: %s", reason)
	}
	return answer.Result.FilePath, nil
}

func (client *TelegramFileClient) get(ctx context.Context, target *url.URL) ([]byte, error) {
	if err := client.policy.CheckURL(target); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.client.Do(request)
	if err != nil {
		// A Bot API URL carries the bot token in its path, and the transport
		// error prints the URL: only the scheme and host go on.
		return nil, safehttp.RedactError(err)
	}
	defer response.Body.Close()
	contents, truncated, err := client.policy.ReadBody(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		// The remote body is not echoed: a Bot API error repeats the URL, and
		// the URL contains the token.
		return nil, fmt.Errorf("the Telegram API answered %d", response.StatusCode)
	}
	if truncated {
		return nil, fmt.Errorf("the file exceeds the configured size limit")
	}
	return contents, nil
}

// telegramFileID finds the file an update carries, and its name.
//
// A photo arrives as an array of sizes rather than one file, which is why the
// Image Size option exists at all; everything else is a single object with a
// `file_id`. The order below is the order the Bot API documents on Message.
func telegramFileID(update map[string]any, size string) (fileID, fileName string, found bool) {
	for _, container := range []string{"message", "edited_message", "channel_post", "edited_channel_post", "business_message", "edited_business_message"} {
		message, ok := update[container].(map[string]any)
		if !ok {
			continue
		}
		if photos, ok := message["photo"].([]any); ok && len(photos) > 0 {
			chosen, ok := photos[telegramPhotoIndex(size, len(photos))].(map[string]any)
			if !ok {
				continue
			}
			id, _ := chosen["file_id"].(string)
			return id, "", id != ""
		}
		for _, field := range []string{"document", "audio", "video", "voice", "video_note", "animation", "sticker"} {
			attachment, ok := message[field].(map[string]any)
			if !ok {
				continue
			}
			id, _ := attachment["file_id"].(string)
			name, _ := attachment["file_name"].(string)
			if id != "" {
				return id, name, true
			}
		}
	}
	return "", "", false
}

// telegramPhotoIndex maps the Image Size option onto one of the sizes Telegram
// actually sent, which is not always four.
func telegramPhotoIndex(size string, available int) int {
	index := available - 1
	switch size {
	case "small":
		index = 0
	case "medium":
		index = 1
	case "large":
		index = 2
	case "extraLarge":
		index = 3
	case "max", "":
		index = available - 1
	}
	if index >= available {
		index = available - 1
	}
	if index < 0 {
		index = 0
	}
	return index
}
