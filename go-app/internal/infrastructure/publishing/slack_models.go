package publishing

// slack_models.go - Slack Webhook API data models
// Implements Slack Block Kit and Incoming Webhooks API structures

// SlackMessage represents a Slack webhook message payload
// Supports both Block Kit (blocks) and legacy attachments
type SlackMessage struct {
	// Text is fallback text for notifications (required)
	// Used when blocks are not supported or for notification preview
	Text string `json:"text"`

	// Blocks contain Block Kit layout blocks for rich formatting
	// Optional - if not provided, Text will be used
	Blocks []Block `json:"blocks,omitempty"`

	// ThreadTS is the message timestamp for threading
	// If provided, message will be posted as a reply in the thread
	ThreadTS string `json:"thread_ts,omitempty"`

	// Attachments are legacy color-coded message attachments
	// Used for color bars (critical=red, warning=orange, etc.)
	Attachments []Attachment `json:"attachments,omitempty"`

	// ------------------------------------------------------------------
	// Upstream slack_config presentation fields (TEMPLATES-EPIC slice 2)
	// ------------------------------------------------------------------
	//
	// Added so an operator's `slack_configs[].channel`/`username`/`icon_emoji`/
	// `icon_url` can actually reach Slack. They were parsed and validated by
	// routing.SlackConfig and then had nowhere to go: buildMessage could only
	// carry text/blocks/attachments, so a configured channel was silently
	// dropped (FU-INTEGRATION-FIELD-FIDELITY).
	//
	// All omitempty, so a message built without them serializes byte-identically
	// to before — a template-less deployment sees no wire change at all.
	Channel   string `json:"channel,omitempty"`
	Username  string `json:"username,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
}

// Block represents a Slack Block Kit block
// Supports: header, section, divider, context block types
type Block struct {
	// Type is the block type (header, section, divider, context)
	Type string `json:"type"`

	// Text is the main text element for header and section blocks
	// Can be plain_text or mrkdwn format
	Text *Text `json:"text,omitempty"`

	// Fields are multi-column fields for section blocks only
	// Displays 2-column layout (left field, right field)
	Fields []Field `json:"fields,omitempty"`

	// Elements are the small text/image items a "context" block displays
	// (review wave 5, finding R1). Block Kit REQUIRES 1-10 elements on a
	// context block — formatSlack builds one carrying the alert's fingerprint,
	// but this field did not exist, so buildBlock had nothing to populate it
	// from and every context block serialized as bare {"type":"context"}.
	// Slack's webhook validates the whole "blocks" array and answers
	// invalid_blocks / HTTP 400 for that, not a silent fallback to Text — the
	// same live-breakage class C1 was fixed to remove.
	Elements []Text `json:"elements,omitempty"`
}

// Text represents plain_text or mrkdwn text element
type Text struct {
	// Type is "plain_text" or "mrkdwn"
	Type string `json:"type"`

	// Text is the text content
	// plain_text: no formatting
	// mrkdwn: supports *bold*, _italic_, ~strike~, `code`, etc.
	Text string `json:"text"`
}

// Field represents a section field for 2-column layout
type Field struct {
	// Type is usually "mrkdwn" for formatted text
	Type string `json:"type"`

	// Text is the field content
	// Supports markdown formatting
	Text string `json:"text"`
}

// Attachment represents a legacy message attachment
// Used primarily for color-coding (side bar color)
type Attachment struct {
	// Color is hex color code (#FF0000 for red, #FFA500 for orange, etc.)
	Color string `json:"color"`

	// Text is attachment text content
	Text string `json:"text,omitempty"`

	// Fields are the legacy attachment's own 2-column fields (review wave 5,
	// finding R2 — same shape as Block.Fields). formatSlack's attachment
	// carries Status/Started/Namespace/AI-severity here, but this field did
	// not exist, so buildAttachment had nothing to populate it from and every
	// attachment shipped color-only. Cosmetic, not a rejection risk (legacy
	// attachments don't require fields), unlike R1's context block.
	Fields []Field `json:"fields,omitempty"`

	// ------------------------------------------------------------------
	// Upstream slack_config presentation fields (TEMPLATES-EPIC slice 2)
	// ------------------------------------------------------------------
	//
	// Slack's legacy attachment shape is what upstream Alertmanager renders its
	// `title`/`title_link`/`pretext`/`fallback` into, and these are the fields
	// the template formatter fills from the receiver's config. Without them the
	// rendered title had nowhere to live and never reached the wire.
	//
	// omitempty throughout: an attachment built by the fixed formatter (color +
	// fields only) serializes exactly as it did before this epic.
	Title     string `json:"title,omitempty"`
	TitleLink string `json:"title_link,omitempty"`
	Pretext   string `json:"pretext,omitempty"`
	Fallback  string `json:"fallback,omitempty"`
}

// SlackResponse represents Slack webhook API response
type SlackResponse struct {
	// OK indicates success (true) or failure (false)
	OK bool `json:"ok"`

	// TS is the message timestamp (Slack's unique message identifier)
	// Format: "1234567890.123456"
	// Used for threading (thread_ts) and message updates
	TS string `json:"ts,omitempty"`

	// Channel is the channel ID where message was posted
	Channel string `json:"channel,omitempty"`

	// Error contains error message if OK is false
	Error string `json:"error,omitempty"`
}

// NewHeaderBlock creates a header block with plain text
// Header blocks are bold and larger than regular text
func NewHeaderBlock(text string) Block {
	return Block{
		Type: "header",
		Text: &Text{
			Type: "plain_text",
			Text: text,
		},
	}
}

// NewSectionBlock creates a section block with markdown text
// Section blocks support rich formatting (bold, italic, links, etc.)
func NewSectionBlock(text string) Block {
	return Block{
		Type: "section",
		Text: &Text{
			Type: "mrkdwn",
			Text: text,
		},
	}
}

// NewSectionFields creates a section block with 2-column fields
// Fields are displayed side-by-side (left field | right field)
// Example: Status: firing | Namespace: prod
func NewSectionFields(fields ...string) Block {
	block := Block{
		Type:   "section",
		Fields: make([]Field, 0, len(fields)),
	}
	for _, f := range fields {
		block.Fields = append(block.Fields, Field{
			Type: "mrkdwn",
			Text: f,
		})
	}
	return block
}

// NewDividerBlock creates a visual divider (horizontal line)
// Used to separate sections in the message
func NewDividerBlock() Block {
	return Block{
		Type: "divider",
	}
}

// NewContextBlock creates a context block with small gray text
// Typically used for metadata (timestamps, authors, etc.)
func NewContextBlock(text string) Block {
	return Block{
		Type: "context",
		Text: &Text{
			Type: "mrkdwn",
			Text: text,
		},
	}
}

// NewAttachment creates a color-coded attachment
// Color is hex code (#FF0000 for red, #FFA500 for orange, etc.)
// Creates a colored vertical bar on the left side of the message
func NewAttachment(color, text string) Attachment {
	return Attachment{
		Color: color,
		Text:  text,
	}
}

// ColorCritical is red color for critical alerts (#FF0000)
const ColorCritical = "#FF0000"

// ColorWarning is orange color for warning alerts (#FFA500)
const ColorWarning = "#FFA500"

// ColorInfo is green color for info alerts (#36A64F)
const ColorInfo = "#36A64F"

// ColorNoise is gray color for noise alerts (#808080)
const ColorNoise = "#808080"

// ColorResolved is green color for resolved alerts (#36A64F)
const ColorResolved = "#36A64F"
