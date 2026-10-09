package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// CredentialUser is one workflow that attaches a credential: in its draft (the
// latest revision, which is what the next activation publishes) or in the
// version it last published. Either breaks if the credential goes.
type CredentialUser struct {
	WorkflowID string
	Name       string
	Active     bool
	// Nodes are the names of the nodes that attach it, across the draft and
	// the published version, sorted.
	Nodes []string
}

// CredentialUsers lists the tenant's workflows that attach a credential,
// ordered by name. Deleted workflows and revisions that are neither the draft
// nor the published version are not users: nothing would run them again
// without a save or a publish that names a credential anew.
//
// It reads the two candidate revisions of every workflow in one query and
// decodes only the definitions whose bytes contain the id, so it costs one
// scan of the tenant's current definitions — acceptable for a single read or
// delete, and the reason it is never run for a listing. It is plain SQL over
// the definition blob rather than a JSON or LIKE predicate, so it means the
// same thing on every driver.
func (store *GORMCredentialStore) CredentialUsers(ctx context.Context, tenant TenantScope, credentialID string) ([]CredentialUser, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	credentialID = strings.TrimSpace(credentialID)
	if credentialID == "" {
		return []CredentialUser{}, nil
	}
	type candidate struct {
		WorkflowID string
		Name       string
		Active     bool
		Definition []byte
	}
	versions := store.db.NamingStrategy.TableName("workflow_versions")
	workflows := store.db.NamingStrategy.TableName("workflows")
	statement := fmt.Sprintf(
		"SELECT w.id AS workflow_id, w.name AS name, w.active AS active, v.definition AS definition"+
			" FROM %s v JOIN %s w ON w.id = v.workflow_id AND w.tenant_id = v.tenant_id"+
			" WHERE v.tenant_id = ? AND w.deleted_at IS NULL"+
			" AND (v.revision = w.latest_revision OR v.id = w.active_version_id)",
		versions, workflows)
	// Streamed one row at a time, and only a definition that contains the id
	// is decoded: a tenant's definitions are never all held in memory at once.
	// Nothing else is queried while the cursor is open, which matters on
	// SQLite's single connection.
	rows, err := store.db.WithContext(ctx).Raw(statement, tenant.ID).Rows()
	if err != nil {
		return nil, fmt.Errorf("find the workflows using credential: %w", err)
	}
	defer rows.Close()

	needle := []byte(credentialID)
	byWorkflow := map[string]*CredentialUser{}
	nodeSets := map[string]map[string]struct{}{}
	for rows.Next() {
		var row candidate
		if err := store.db.ScanRows(rows, &row); err != nil {
			return nil, fmt.Errorf("read a workflow definition: %w", err)
		}
		if !bytes.Contains(row.Definition, needle) {
			continue
		}
		var document workflow.Document
		if err := json.Unmarshal(row.Definition, &document); err != nil {
			return nil, fmt.Errorf("decode workflow %q to find its credentials: %w", row.WorkflowID, err)
		}
		for _, node := range document.Nodes {
			if !attaches(node, credentialID) {
				continue
			}
			user, seen := byWorkflow[row.WorkflowID]
			if !seen {
				user = &CredentialUser{WorkflowID: row.WorkflowID, Name: row.Name, Active: row.Active}
				byWorkflow[row.WorkflowID] = user
				nodeSets[row.WorkflowID] = map[string]struct{}{}
			}
			label := strings.TrimSpace(node.Name)
			if label == "" {
				label = node.ID
			}
			nodeSets[row.WorkflowID][label] = struct{}{}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find the workflows using credential: %w", err)
	}

	users := make([]CredentialUser, 0, len(byWorkflow))
	for workflowID, user := range byWorkflow {
		for label := range nodeSets[workflowID] {
			user.Nodes = append(user.Nodes, label)
		}
		sort.Strings(user.Nodes)
		users = append(users, *user)
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].Name != users[j].Name {
			return users[i].Name < users[j].Name
		}
		return users[i].WorkflowID < users[j].WorkflowID
	})
	return users, nil
}

func attaches(node workflow.Node, credentialID string) bool {
	for _, attached := range node.Credentials {
		if strings.TrimSpace(attached) == credentialID {
			return true
		}
	}
	return false
}

// refuseUnknownCredentials fails an activation whose document attaches a
// credential this tenant does not have — deleted since the workflow was
// saved, or never this tenant's. Without it the workflow activates and every
// run fails at its first authenticated call.
//
// Read inside the publishing transaction, so the answer is the state the
// activation commits against. Only enabled nodes count: a disabled node never
// runs, and refusing its dangling credential would make an imported workflow
// with a switched-off node unactivatable, which is the reason the compiler
// skips disabled nodes too. One query covers every node.
func refuseUnknownCredentials(tx *gorm.DB, tenant TenantScope, document workflow.Document) error {
	type attachment struct {
		index          int
		node           workflow.Node
		credentialType string
		credentialID   string
	}
	var attachments []attachment
	wanted := map[string]struct{}{}
	for index, node := range document.Nodes {
		if node.Disabled {
			continue
		}
		types := make([]string, 0, len(node.Credentials))
		for credentialType := range node.Credentials {
			types = append(types, credentialType)
		}
		sort.Strings(types)
		for _, credentialType := range types {
			credentialID := strings.TrimSpace(node.Credentials[credentialType])
			if credentialID == "" {
				continue
			}
			attachments = append(attachments, attachment{index, node, credentialType, credentialID})
			wanted[credentialID] = struct{}{}
		}
	}
	if len(attachments) == 0 {
		return nil
	}
	ids := make([]string, 0, len(wanted))
	for credentialID := range wanted {
		ids = append(ids, credentialID)
	}
	var found []string
	if err := tx.Model(&credentialModel{}).Where("tenant_id = ? AND id IN ?", tenant.ID, ids).
		Pluck("id", &found).Error; err != nil {
		return fmt.Errorf("check the credentials this workflow attaches: %w", err)
	}
	exists := make(map[string]struct{}, len(found))
	for _, credentialID := range found {
		exists[credentialID] = struct{}{}
	}
	refusal := &workflow.ValidationErrors{}
	for _, attached := range attachments {
		if _, ok := exists[attached.credentialID]; ok {
			continue
		}
		refusal.Issues = append(refusal.Issues, workflow.ValidationError{
			Code:   workflow.ErrorInvalidConfig,
			Path:   fmt.Sprintf("/nodes/%d/credentials/%s", attached.index, attached.credentialType),
			NodeID: attached.node.ID,
			Message: fmt.Sprintf("node %q (%s) attaches %s credential %q, which does not exist in this workspace: attach another credential to this node",
				attached.node.Name, attached.node.ID, attached.credentialType, attached.credentialID),
		})
	}
	if len(refusal.Issues) == 0 {
		return nil
	}
	return refusal
}
