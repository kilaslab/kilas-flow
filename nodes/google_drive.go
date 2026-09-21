package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	GoogleDriveExecutorID        = "core.googleDrive"
	GoogleDriveTriggerExecutorID = "core.googleDriveTrigger"
	GoogleDriveNodeType          = "kilasflow.googleDrive"
	GoogleDriveTriggerNodeType   = "kilasflow.googleDriveTrigger"
	GoogleDriveCredentialType    = "googleDriveOAuth2Api"
)

func googleDriveNode() node.Definition {
	return node.Definition{
		Type:        GoogleDriveNodeType,
		Version:     workflow.V(1),
		DisplayName: "Google Drive",
		Description: "Searches, downloads, moves and manages files in Google Drive. Shared drives are not supported in this slice.",
		Category:    "Files",
		Group:       []node.NodeGroup{node.GroupOutput},
		Icon:        &node.NodeIcon{Light: "builtin:folder"},
		IconColor:   "#4285f4",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: GoogleDriveCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Default: "file",
				Options: []node.PropertyOption{
					{Label: "File", Value: "file"},
					{Label: "Folder", Value: "folder"},
					{Label: "File or Folder", Value: "fileFolder"},
				},
			},
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Default: "search",
				Options: []node.PropertyOption{
					{Label: "Search", Value: "search"},
					{Label: "Download", Value: "download"},
					{Label: "Move", Value: "move"},
					{Label: "Copy", Value: "copy"},
					{Label: "Create", Value: "create"},
					{Label: "Create From Text", Value: "createFromText"},
					{Label: "Delete", Value: "delete"},
					{Label: "Share", Value: "share"},
					{Label: "Update", Value: "update"},
					{Label: "Upload", Value: "upload"},
				},
			},
			{Key: "fileId", Label: "File ID", Kind: node.PropertyString},
			{Key: "folderId", Label: "Folder ID", Kind: node.PropertyString},
			{Key: "queryString", Label: "Query", Kind: node.PropertyString},
			{Key: "name", Label: "Name", Kind: node.PropertyString},
			{Key: "content", Label: "Content", Kind: node.PropertyString},
			{Key: "binaryPropertyName", Label: "Binary property", Kind: node.PropertyString, Default: "data"},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     GoogleDriveExecutorID,
	}
}

func googleDriveTrigger() node.Definition {
	return node.Definition{
		Type:        GoogleDriveTriggerNodeType,
		Version:     workflow.V(1),
		DisplayName: "Google Drive Trigger",
		Description: "Starts a workflow when files in a Drive folder are created or updated. Polls with a leased cursor; Shared drives are not supported.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:folder"},
		IconColor:   "#4285f4",
		Subtitle:    "{{ $parameter.event }}",
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: GoogleDriveCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "event", Label: "Event", Kind: node.PropertyOptions, Default: "fileUpdated",
				Options: []node.PropertyOption{
					{Label: "File Created", Value: "fileCreated"},
					{Label: "File Updated", Value: "fileUpdated"},
				},
			},
			{Key: "folderToWatch", Label: "Folder to watch", Kind: node.PropertyString},
			{Key: "pollTimes", Label: "Poll Times", Kind: node.PropertyCollection},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     GoogleDriveTriggerExecutorID,
	}
}

// GoogleDriveExecutor runs Drive file and folder operations.
type GoogleDriveExecutor struct {
	google *GoogleClient
}

// NewGoogleDriveExecutor builds the Drive action node.
func NewGoogleDriveExecutor(policy safehttp.Policy) *GoogleDriveExecutor {
	return &GoogleDriveExecutor{google: NewGoogleClient(policy)}
}

func (executor *GoogleDriveExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
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

func (executor *GoogleDriveExecutor) executeItem(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	operation := strings.ToLower(strings.TrimSpace(textValue(ir.Parameters["operation"], "search")))
	resource := strings.ToLower(strings.TrimSpace(textValue(ir.Parameters["resource"], "file")))
	switch {
	case operation == "search" || operation == "list" || resource == "filefolder" && operation == "":
		return executor.search(ctx, ir, item, request)
	case operation == "download":
		return executor.download(ctx, ir, item, request)
	case operation == "move":
		return executor.move(ctx, ir, item, request)
	case operation == "copy":
		return executor.copy(ctx, ir, item, request)
	case operation == "delete":
		return executor.delete(ctx, ir, item, request)
	case operation == "create" && resource == "folder":
		return executor.createFolder(ctx, ir, item, request)
	case operation == "create" || operation == "createfromtext":
		return executor.createFromText(ctx, ir, item, request)
	case operation == "upload":
		return executor.upload(ctx, ir, item, request)
	case operation == "share":
		return executor.share(ctx, ir, item, request)
	case operation == "update":
		return executor.update(ctx, ir, item, request)
	default:
		return nil, fmt.Errorf("node %q: unsupported google drive operation %q", ir.Name, operation)
	}
}

func (executor *GoogleDriveExecutor) search(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	query := strings.TrimSpace(textValue(ir.Parameters["queryString"], textValue(ir.Parameters["query"], "")))
	if query == "" {
		if folder := driveFolderID(ir, item); folder != "" {
			query = fmt.Sprintf("'%s' in parents and trashed = false", escapeDriveQueryValue(folder))
		} else {
			query = "trashed = false"
		}
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("fields", "files(id,name,mimeType,parents,modifiedTime,size,webViewLink)")
	params.Set("pageSize", "100")
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodGet, driveAPIRoot+"/files", params, nil, &payload); err != nil {
		return nil, err
	}
	items := make([]workflow.Item, 0, len(payload.Files))
	for _, file := range payload.Files {
		items = append(items, workflow.Item{JSON: file, Paired: item.Paired})
	}
	if len(items) == 0 {
		return []workflow.Item{{JSON: map[string]any{}, Paired: item.Paired}}, nil
	}
	return items, nil
}

func (executor *GoogleDriveExecutor) download(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	fileID := driveFileID(ir, item)
	if fileID == "" {
		return nil, fmt.Errorf("node %q: fileId is required to download", ir.Name)
	}
	var meta map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodGet, driveAPIRoot+"/files/"+url.PathEscape(fileID), url.Values{"fields": []string{"id,name,mimeType,size"}}, nil, &meta); err != nil {
		return nil, err
	}
	mime := textValue(meta["mimeType"], "")
	downloadURL := driveAPIRoot + "/files/" + url.PathEscape(fileID)
	query := url.Values{}
	if strings.HasPrefix(mime, "application/vnd.google-apps.") {
		downloadURL += "/export"
		query.Set("mimeType", "application/pdf")
	} else {
		query.Set("alt", "media")
	}
	contents, response, err := executor.google.do(ctx, request, ir, GoogleDriveCredentialType, http.MethodGet, downloadURL, query, nil)
	if err != nil {
		return nil, err
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured", ir.Name)
	}
	name := textValue(meta["name"], fileID)
	media := mime
	if response != nil && response.Header.Get("Content-Type") != "" {
		media = response.Header.Get("Content-Type")
	}
	reference, err := request.Binaries.Put(name, media, bytes.NewReader(contents))
	if err != nil {
		return nil, fmt.Errorf("node %q: store downloaded file: %w", ir.Name, err)
	}
	property := textValue(ir.Parameters["binaryPropertyName"], "data")
	if property == "" {
		property = "data"
	}
	out := item
	if out.JSON == nil {
		out.JSON = map[string]any{}
	}
	for key, value := range meta {
		out.JSON[key] = value
	}
	if out.Binary == nil {
		out.Binary = map[string]workflow.BinaryRef{}
	}
	out.Binary[property] = reference
	return []workflow.Item{out}, nil
}

func (executor *GoogleDriveExecutor) move(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	fileID := driveFileID(ir, item)
	if fileID == "" {
		return nil, fmt.Errorf("node %q: fileId is required to move", ir.Name)
	}
	destination := locatorValue(ir.Parameters["folderId"])
	if destination == "" {
		destination = locatorValue(item.JSON["folderId"])
	}
	if destination == "" {
		return nil, fmt.Errorf("node %q: folderId is required to move", ir.Name)
	}
	parents := stringList(item.JSON["parents"])
	query := url.Values{"addParents": []string{destination}, "fields": []string{"id,name,parents"}}
	if len(parents) > 0 {
		query.Set("removeParents", strings.Join(parents, ","))
	}
	var payload map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodPatch, driveAPIRoot+"/files/"+url.PathEscape(fileID), query, map[string]any{}, &payload); err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: payload, Paired: item.Paired}}, nil
}

func (executor *GoogleDriveExecutor) copy(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	fileID := driveFileID(ir, item)
	if fileID == "" {
		return nil, fmt.Errorf("node %q: fileId is required to copy", ir.Name)
	}
	body := map[string]any{}
	if name := strings.TrimSpace(textValue(ir.Parameters["name"], "")); name != "" {
		body["name"] = name
	}
	if folder := driveFolderID(ir, item); folder != "" {
		body["parents"] = []string{folder}
	}
	var payload map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodPost, driveAPIRoot+"/files/"+url.PathEscape(fileID)+"/copy", nil, body, &payload); err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: payload, Paired: item.Paired}}, nil
}

func (executor *GoogleDriveExecutor) delete(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	fileID := driveFileID(ir, item)
	if fileID == "" {
		fileID = driveFolderID(ir, item)
	}
	if fileID == "" {
		return nil, fmt.Errorf("node %q: fileId is required to delete", ir.Name)
	}
	if _, _, err := executor.google.do(ctx, request, ir, GoogleDriveCredentialType, http.MethodDelete, driveAPIRoot+"/files/"+url.PathEscape(fileID), nil, nil); err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: map[string]any{"id": fileID, "deleted": true}, Paired: item.Paired}}, nil
}

func (executor *GoogleDriveExecutor) createFolder(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	name := strings.TrimSpace(textValue(ir.Parameters["name"], textValue(item.JSON["name"], "Untitled folder")))
	body := map[string]any{"name": name, "mimeType": "application/vnd.google-apps.folder"}
	if folder := driveFolderID(ir, item); folder != "" {
		body["parents"] = []string{folder}
	}
	var payload map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodPost, driveAPIRoot+"/files", nil, body, &payload); err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: payload, Paired: item.Paired}}, nil
}

func (executor *GoogleDriveExecutor) createFromText(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	name := strings.TrimSpace(textValue(ir.Parameters["name"], textValue(item.JSON["name"], "Untitled.txt")))
	content := textValue(ir.Parameters["content"], textValue(item.JSON["content"], textValue(item.JSON["text"], "")))
	body := map[string]any{"name": name, "mimeType": "text/plain"}
	if folder := driveFolderID(ir, item); folder != "" {
		body["parents"] = []string{folder}
	}
	var created map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodPost, driveAPIRoot+"/files", nil, body, &created); err != nil {
		return nil, err
	}
	fileID := textValue(created["id"], "")
	if fileID != "" && content != "" {
		if _, _, err := executor.google.do(ctx, request, ir, GoogleDriveCredentialType, http.MethodPatch, "https://www.googleapis.com/upload/drive/v3/files/"+url.PathEscape(fileID)+"?uploadType=media", nil, []byte(content)); err != nil {
			return nil, err
		}
	}
	return []workflow.Item{{JSON: created, Paired: item.Paired}}, nil
}

func (executor *GoogleDriveExecutor) upload(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	property := textValue(ir.Parameters["binaryPropertyName"], "data")
	if property == "" {
		property = "data"
	}
	if item.Binary == nil || item.Binary[property].ID == "" {
		return nil, fmt.Errorf("node %q: incoming item has no binary property %q", ir.Name, property)
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured", ir.Name)
	}
	contents, readErr := func() ([]byte, error) {
		body, _, err := request.Binaries.Get(item.Binary[property].ID)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return io.ReadAll(body)
	}()
	if readErr != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, readErr)
	}
	name := strings.TrimSpace(textValue(ir.Parameters["name"], item.Binary[property].FileName))
	if name == "" {
		name = "upload.bin"
	}
	meta := map[string]any{"name": name}
	if folder := driveFolderID(ir, item); folder != "" {
		meta["parents"] = []string{folder}
	}
	var created map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodPost, driveAPIRoot+"/files", nil, meta, &created); err != nil {
		return nil, err
	}
	if fileID := textValue(created["id"], ""); fileID != "" {
		if _, _, err := executor.google.do(ctx, request, ir, GoogleDriveCredentialType, http.MethodPatch, "https://www.googleapis.com/upload/drive/v3/files/"+url.PathEscape(fileID)+"?uploadType=media", nil, contents); err != nil {
			return nil, err
		}
	}
	return []workflow.Item{{JSON: created, Paired: item.Paired}}, nil
}

func (executor *GoogleDriveExecutor) share(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	fileID := driveFileID(ir, item)
	if fileID == "" {
		fileID = driveFolderID(ir, item)
	}
	if fileID == "" {
		return nil, fmt.Errorf("node %q: fileId is required to share", ir.Name)
	}
	permissions, _ := ir.Parameters["permissions"].(map[string]any)
	role := textValue(permissions["role"], textValue(ir.Parameters["role"], "reader"))
	kind := textValue(permissions["type"], textValue(ir.Parameters["type"], "user"))
	email := textValue(permissions["emailAddress"], textValue(ir.Parameters["emailAddress"], ""))
	body := map[string]any{"role": role, "type": kind}
	if email != "" {
		body["emailAddress"] = email
	}
	var payload map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodPost, driveAPIRoot+"/files/"+url.PathEscape(fileID)+"/permissions", nil, body, &payload); err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: payload, Paired: item.Paired}}, nil
}

func (executor *GoogleDriveExecutor) update(ctx context.Context, ir workflow.IRNode, item workflow.Item, request engine.Request) ([]workflow.Item, error) {
	fileID := driveFileID(ir, item)
	if fileID == "" {
		return nil, fmt.Errorf("node %q: fileId is required to update", ir.Name)
	}
	body := map[string]any{}
	if name := strings.TrimSpace(textValue(ir.Parameters["name"], "")); name != "" {
		body["name"] = name
	}
	var payload map[string]any
	if err := executor.google.json(ctx, request, ir, GoogleDriveCredentialType, http.MethodPatch, driveAPIRoot+"/files/"+url.PathEscape(fileID), nil, body, &payload); err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: payload, Paired: item.Paired}}, nil
}

func driveFileID(ir workflow.IRNode, item workflow.Item) string {
	if id := locatorValue(ir.Parameters["fileId"]); id != "" {
		return id
	}
	return locatorValue(item.JSON["id"])
}

func driveFolderID(ir workflow.IRNode, item workflow.Item) string {
	if id := locatorValue(ir.Parameters["folderId"]); id != "" {
		return id
	}
	if id := locatorValue(ir.Parameters["folderToWatch"]); id != "" {
		return id
	}
	return locatorValue(item.JSON["folderId"])
}

// GoogleDriveTriggerExecutor emits the polled Drive change on a manual run.
type GoogleDriveTriggerExecutor struct{}

func (GoogleDriveTriggerExecutor) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	return workflow.NodeOutput{{item}}, nil
}

// GoogleDrivePoller fetches new files in a watched folder.
type GoogleDrivePoller struct {
	google *GoogleClient
}

// NewGoogleDrivePoller builds the Drive trigger poll handler.
func NewGoogleDrivePoller(policy safehttp.Policy) *GoogleDrivePoller {
	return &GoogleDrivePoller{google: NewGoogleClient(policy)}
}

type drivePollCursor struct {
	PageToken    string `json:"pageToken"`
	ModifiedTime string `json:"modifiedTime"`
}

func (pollerHandler *GoogleDrivePoller) Poll(ctx context.Context, claim poller.Claim) ([]workflow.Item, string, error) {
	ir := irFromNode(claim.Node)
	var cursor drivePollCursor
	if strings.TrimSpace(claim.Cursor.Cursor) != "" {
		_ = json.Unmarshal([]byte(claim.Cursor.Cursor), &cursor)
	}
	folder := locatorValue(claim.Node.Parameters["folderToWatch"])
	if folder == "" {
		folder = locatorValue(claim.Node.Parameters["folderId"])
	}
	event := strings.ToLower(strings.TrimSpace(textValue(claim.Node.Parameters["event"], "fileUpdated")))
	query := "trashed = false"
	if folder != "" {
		query = fmt.Sprintf("'%s' in parents and trashed = false", escapeDriveQueryValue(folder))
	}
	if cursor.ModifiedTime != "" {
		if event == "filecreated" {
			query += fmt.Sprintf(" and createdTime > '%s'", cursor.ModifiedTime)
		} else {
			query += fmt.Sprintf(" and modifiedTime > '%s'", cursor.ModifiedTime)
		}
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("fields", "files(id,name,mimeType,parents,modifiedTime,createdTime,size)")
	params.Set("orderBy", "modifiedTime")
	params.Set("pageSize", "50")
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := pollerHandler.google.json(ctx, claim.Request, ir, GoogleDriveCredentialType, http.MethodGet, driveAPIRoot+"/files", params, nil, &payload); err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	if cursor.ModifiedTime == "" {
		cursor.ModifiedTime = claim.Now.UTC().Format(time.RFC3339)
		encoded, _ := json.Marshal(cursor)
		return nil, string(encoded), nil
	}
	items := make([]workflow.Item, 0, len(payload.Files))
	latest := cursor.ModifiedTime
	for _, file := range payload.Files {
		modified := textValue(file["modifiedTime"], textValue(file["createdTime"], ""))
		if modified > latest {
			latest = modified
		}
		items = append(items, workflow.Item{JSON: file})
	}
	cursor.ModifiedTime = latest
	encoded, _ := json.Marshal(cursor)
	return items, string(encoded), nil
}
