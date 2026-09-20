package nodes

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Telegram's server-owned bindings.
const (
	TelegramTriggerNodeType    = "kilasflow.telegramTrigger"
	TelegramTriggerExecutorID  = "core.telegramTrigger"
	TelegramTriggerLifecycleID = "telegram.webhook"
	// TelegramCredentialType is n8n's own name for it, so an imported node
	// asks for the credential the workflow was authored against.
	TelegramCredentialType = "telegramApi"
	// TelegramAPIHost is the only host this node ever calls. The Bot API puts
	// the token in the path, so there is no other host it *could* reach with a
	// credential, and pinning it means a compromised parameter cannot redirect
	// a bot token somewhere else.
	TelegramAPIHost = "api.telegram.org"
	// TelegramSecretHeader carries the per-registration secret on every update.
	TelegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token"
)

// telegramUpdates is n8n's Trigger On list, in n8n's order.
//
// The values are the Bot API's own update names, and the list is pinned to
// n8n's rather than to the Bot API's current one on purpose: an imported
// workflow carries these strings and has to find them here. An update type
// nobody selected still reaches the item — see telegramAccepts — so a Bot API
// that grows a new type does not break an active workflow.
var telegramUpdates = []node.PropertyOption{
	{Label: "*", Value: "*"},
	{Label: "Message", Value: "message"},
	{Label: "Edited Message", Value: "edited_message"},
	{Label: "Channel Post", Value: "channel_post"},
	{Label: "Edited Channel Post", Value: "edited_channel_post"},
	{Label: "Callback Query", Value: "callback_query"},
	{Label: "Inline Query", Value: "inline_query"},
	{Label: "Chosen Inline Result", Value: "chosen_inline_result"},
	{Label: "My Chat Member", Value: "my_chat_member"},
	{Label: "Chat Member", Value: "chat_member"},
	{Label: "Chat Join Request", Value: "chat_join_request"},
	{Label: "Poll", Value: "poll"},
	{Label: "Poll Answer", Value: "poll_answer"},
	{Label: "Pre-Checkout Query", Value: "pre_checkout_query"},
	{Label: "Shipping Query", Value: "shipping_query"},
	{Label: "Message Reaction", Value: "message_reaction"},
	{Label: "Message Reaction Count", Value: "message_reaction_count"},
	{Label: "Chat Boost", Value: "chat_boost"},
	{Label: "Removed Chat Boost", Value: "removed_chat_boost"},
	{Label: "Business Connection", Value: "business_connection"},
	{Label: "Business Message", Value: "business_message"},
	{Label: "Edited Business Message", Value: "edited_business_message"},
	{Label: "Deleted Business Messages", Value: "deleted_business_messages"},
	{Label: "Purchased Paid Media", Value: "purchased_paid_media"},
}

// telegramDefaultExclusions are the three update types the Bot API does not
// deliver unless they are asked for by name.
//
// This is the Bot API's rule, not n8n's decoration: `chat_member`,
// `message_reaction` and `message_reaction_count` are documented as excluded
// from the default set. Selecting `*` therefore means "the default set", and
// sending an explicit allowed_updates list that included these three would
// quietly subscribe a workflow to traffic its author did not ask for.
var telegramDefaultExclusions = map[string]bool{
	"chat_member": true, "message_reaction": true, "message_reaction_count": true,
}

// telegramImageSizes maps n8n's Image Size option onto a photo-size index.
var telegramImageSizes = []node.PropertyOption{
	{Label: "Small", Value: "small"},
	{Label: "Medium", Value: "medium"},
	{Label: "Large", Value: "large"},
	{Label: "Extra Large", Value: "extraLarge"},
	{Label: "Max", Value: "max"},
}

func telegramTrigger() node.Definition {
	return node.Definition{
		Type:        TelegramTriggerNodeType,
		Version:     workflow.V(1),
		DisplayName: "Telegram Trigger",
		Description: "Starts a workflow when a Telegram bot receives an update.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:send"},
		IconColor:   "#2aabee",
		Subtitle:    "{{ $parameter.path }}",
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: TelegramCredentialType, Required: true}},
		// The binding declaration lives with the node, so extraction never
		// needs to know this type's name.
		Webhook: &node.WebhookDeclaration{
			Name: "default", PathParameter: "path", Method: http.MethodPost,
		},
		LifecycleID: TelegramTriggerLifecycleID,
		ExecutorID:  TelegramTriggerExecutorID,
		Parameters: []node.PropertyDefinition{
			{
				Key: "path", Label: "Path", Kind: node.PropertyString, Required: true,
				Description: "A label for this endpoint. The public URL uses an opaque route minted on activation.",
			},
			{
				Key: "oneTriggerPerBot", Label: "One trigger per bot", Kind: node.PropertyNotice,
				Description: "Telegram allows one webhook per bot, so activating a second workflow with the same credential takes the first one's deliveries.",
			},
			{
				Key: "updates", Label: "Trigger On", Kind: node.PropertyMultiOptions, Required: true,
				Default: []any{"message"}, Options: telegramUpdates,
				Description: "Which updates to subscribe to. `*` is the Bot API's default set, which excludes Chat Member, Message Reaction and Message Reaction Count.",
			},
			{
				Key: "attachmentsAreSeparate", Label: "Attachments arrive separately", Kind: node.PropertyNotice,
				Description: "Every uploaded attachment triggers its own update, even when several were sent together. Group them by `media_group_id`.",
			},
			{
				Key: "delivery", Label: "Delivery", Kind: node.PropertyOptions, Default: "webhook",
				Options: []node.PropertyOption{
					{Label: "Webhook", Value: "webhook"},
					{Label: "Polling (development)", Value: "polling"},
				},
				Description: "Webhook needs a public HTTPS address. Polling calls getUpdates from this process instead, which is how you test on a laptop — it runs in one process only and is not for production.",
			},
			{
				Key: "additionalFields", Label: "Additional Fields", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "download", Label: "Download Images/Files", Kind: node.PropertyBoolean, Default: false,
						Description: "Fetch the update's photo or file and attach it to the item, instead of leaving a file_id that has to be resolved later.",
					},
					{
						Key: "imageSize", Label: "Image Size", Kind: node.PropertyOptions, Default: "large",
						Options:     telegramImageSizes,
						VisibleWhen: []node.VisibilityCondition{{Key: "download", Equals: true}},
						Description: "Which of the photo sizes Telegram offers to download.",
					},
					{
						Key: "chatIds", Label: "Restrict to Chat IDs", Kind: node.PropertyString,
						Description: "Comma-separated chat ids. An update from any other chat is answered and ignored, without starting a run.",
					},
					{
						Key: "userIds", Label: "Restrict to User IDs", Kind: node.PropertyString,
						Description: "Comma-separated user ids. An update from any other user is answered and ignored, without starting a run.",
					},
				},
			},
			{
				Key: "deliveryIdHeader", Label: "Delivery ID header", Kind: node.PropertyString,
				Description: "Header carrying the sender's own identifier for a delivery. Telegram sends none, so this is normally empty.",
			},
		},
		SharedSettings: sharedSettings(),
		Validate:       validateTelegramTrigger,
	}
}

func validateTelegramTrigger(n workflow.Node) error {
	if path, _ := n.Parameters["path"].(string); strings.TrimSpace(path) == "" {
		return fmt.Errorf("a Telegram trigger needs a path label")
	}
	if len(telegramSelectedUpdates(n.Parameters)) == 0 {
		return fmt.Errorf("a Telegram trigger needs at least one update type")
	}
	return nil
}

// telegramSelectedUpdates reads the Trigger On selection.
func telegramSelectedUpdates(parameters map[string]any) []string {
	switch typed := parameters["updates"].(type) {
	case []string:
		return typed
	case []any:
		selected := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok && text != "" {
				selected = append(selected, text)
			}
		}
		return selected
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	default:
		return nil
	}
}

// TelegramAllowedUpdates is the `allowed_updates` argument for setWebhook.
//
// A selection containing `*` sends nothing at all, which is what asks the Bot
// API for its own default set. Building an explicit list from every option
// instead would subscribe the workflow to the three types the API deliberately
// withholds — and a user who picked `*` did not ask for those.
func TelegramAllowedUpdates(parameters map[string]any) []string {
	selected := telegramSelectedUpdates(parameters)
	allowed := make([]string, 0, len(selected))
	for _, update := range selected {
		if update == "*" {
			return nil
		}
		if telegramDefaultExclusions[update] {
			// Explicitly chosen, so explicitly requested: the exclusion is
			// about what `*` means, not about what may be asked for.
			allowed = append(allowed, update)
			continue
		}
		allowed = append(allowed, update)
	}
	return allowed
}

// TelegramSecret derives the per-registration `secret_token`.
//
// Derived rather than generated and stored, because there is nowhere to store
// it: a binding's parameters are the workflow document's, and activation cannot
// write to them. Deriving it from the bot token and the route gives both sides
// the same value with no new storage, and rotates it whenever either changes.
//
// The alphabet is the Bot API's: it accepts only A-Z, a-z, 0-9, `_` and `-`,
// 1 to 256 characters, so a raw base64 secret would be rejected at
// registration — and the failure would look like a bad token.
func TelegramSecret(botToken, route string) string {
	mac := hmac.New(sha256.New, []byte(botToken))
	mac.Write([]byte("kilasflow-telegram-webhook:" + route))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// TelegramVerifier refuses a delivery whose secret header does not match.
//
// Telegram returns the registration's secret on every update, so this is a
// comparison rather than a signature — and there is therefore no excuse for
// skipping it. A missing header is a refusal: a check that accepts an absent
// secret verifies nothing.
func TelegramVerifier(delivery webhook.Delivery) error {
	fields, err := delivery.Fields(TelegramCredentialType)
	if err != nil {
		return err
	}
	expected := TelegramSecret(fields["accessToken"], delivery.Binding.Route)
	provided := delivery.Request.Header.Get(TelegramSecretHeader)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return fmt.Errorf("this endpoint requires a matching %s header", TelegramSecretHeader)
	}
	return nil
}

// TelegramFilter drops an update the trigger was restricted away from.
//
// It answers 200 rather than an error. The update arrived correctly and the
// workflow chose not to act on it, so telling Telegram anything else would
// either invite a retry or leak which chats a workflow watches.
func TelegramFilter(delivery webhook.Delivery) (bool, string) {
	update, ok := delivery.Body.(map[string]any)
	if !ok {
		return true, ""
	}
	chats := telegramIDSet(delivery.Binding.Parameters, "chatIds")
	users := telegramIDSet(delivery.Binding.Parameters, "userIds")
	if len(chats) == 0 && len(users) == 0 {
		return true, ""
	}

	if len(chats) > 0 {
		id, found := telegramChatID(update)
		if !found || !chats[id] {
			return false, fmt.Sprintf("chat %s is not in this trigger's allowed chat ids", telegramOrUnknown(id, found))
		}
	}
	if len(users) > 0 {
		id, found := telegramUserID(update)
		if !found || !users[id] {
			return false, fmt.Sprintf("user %s is not in this trigger's allowed user ids", telegramOrUnknown(id, found))
		}
	}
	return true, ""
}

func telegramOrUnknown(id string, found bool) string {
	if !found {
		return "(none in this update)"
	}
	return id
}

// telegramIDSet reads a comma-separated restriction list from the collection.
func telegramIDSet(parameters map[string]any, key string) map[string]bool {
	additional, _ := parameters["additionalFields"].(map[string]any)
	raw, _ := additional[key].(string)
	ids := map[string]bool{}
	for _, entry := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			ids[trimmed] = true
		}
	}
	return ids
}

// telegramChatID finds the chat an update belongs to.
//
// Every update type carries it somewhere different, and an update type this
// build has never seen carries it somewhere unknown — so a restriction that
// cannot find one drops the update rather than letting it through. A filter
// that fails open is not a filter.
func telegramChatID(update map[string]any) (string, bool) {
	for _, path := range []string{
		"message.chat.id", "edited_message.chat.id",
		"channel_post.chat.id", "edited_channel_post.chat.id",
		"callback_query.message.chat.id",
		"my_chat_member.chat.id", "chat_member.chat.id", "chat_join_request.chat.id",
		"message_reaction.chat.id", "message_reaction_count.chat.id",
		"chat_boost.chat.id", "removed_chat_boost.chat.id",
		"business_message.chat.id", "edited_business_message.chat.id",
		"deleted_business_messages.chat.id",
	} {
		if value, found := telegramNumberish(update, path); found {
			return value, true
		}
	}
	return "", false
}

// telegramUserID finds who sent an update.
func telegramUserID(update map[string]any) (string, bool) {
	for _, path := range []string{
		"message.from.id", "edited_message.from.id",
		"channel_post.from.id", "edited_channel_post.from.id",
		"callback_query.from.id", "inline_query.from.id", "chosen_inline_result.from.id",
		"my_chat_member.from.id", "chat_member.from.id", "chat_join_request.from.id",
		"poll_answer.user.id", "pre_checkout_query.from.id", "shipping_query.from.id",
		"message_reaction.user.id",
		"business_message.from.id", "edited_business_message.from.id",
		"purchased_paid_media.from.id",
	} {
		if value, found := telegramNumberish(update, path); found {
			return value, true
		}
	}
	return "", false
}

// telegramNumberish reads a dotted path and renders it as an id string.
//
// Telegram ids are JSON numbers and can exceed what a float64 renders exactly
// in the shorthand form, so they are formatted without an exponent and without
// a fraction — a chat id written as `1.234567891e+09` matches nothing.
func telegramNumberish(update map[string]any, path string) (string, bool) {
	current := any(update)
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		next, present := object[segment]
		if !present {
			return "", false
		}
		current = next
	}
	switch typed := current.(type) {
	case string:
		return typed, typed != ""
	case float64:
		return fmt.Sprintf("%.0f", typed), true
	case int64:
		return fmt.Sprintf("%d", typed), true
	default:
		return "", false
	}
}

// TelegramTriggerKind is how the HTTP boundary handles a Telegram delivery.
func TelegramTriggerKind() webhook.TriggerKind {
	return webhook.TriggerKind{
		// The update object *is* the item: an imported workflow reads
		// `$json.message.text`, not `$json.body.message.text`.
		Shape:  webhook.ShapeBodyAsItem,
		Verify: TelegramVerifier,
		Accept: TelegramFilter,
	}
}
