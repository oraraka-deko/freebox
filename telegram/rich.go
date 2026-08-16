package telegram

import (
	"context"
	"strings"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/rich"
	"github.com/gotd/td/tg"
)

// RichTextStyle represents the inline styling format.
type RichTextStyle string

const (
	StylePlain     RichTextStyle = "plain"
	StyleBold      RichTextStyle = "bold"
	StyleItalic    RichTextStyle = "italic"
	StyleFixed     RichTextStyle = "fixed"
	StyleStrike    RichTextStyle = "strike"
	StyleUnderline RichTextStyle = "underline"
)

// RichTextSegment represents a fragment of styled text.
type RichTextSegment struct {
	Text  string        `json:"text"`
	Style RichTextStyle `json:"style,omitempty"`
}

// RichMessageBlock represents a structured block inside a rich message.
type RichMessageBlock struct {
	Type     string            `json:"type"` // title, header, paragraph, list, divider, footer
	Text     string            `json:"text,omitempty"`
	Segments []RichTextSegment `json:"segments,omitempty"`
	Items    []string          `json:"items,omitempty"`
}

// RichMessagePayload represents a complete structured rich message document.
type RichMessagePayload struct {
	Title      string             `json:"title,omitempty"`
	Header     string             `json:"header,omitempty"`
	Blocks     []RichMessageBlock `json:"blocks,omitempty"`
	ListItems  []string           `json:"list_items,omitempty"`
	Footer     string             `json:"footer,omitempty"`
	HasDivider bool               `json:"has_divider,omitempty"`
}

// FormatRichText converts styled segments to tg.RichTextClass.
func FormatRichText(segments []RichTextSegment, defaultText string) tg.RichTextClass {
	if len(segments) == 0 {
		return rich.Plain(defaultText)
	}

	texts := make([]tg.RichTextClass, 0, len(segments))
	for _, seg := range segments {
		t := tg.RichTextClass(rich.Plain(seg.Text))
		switch seg.Style {
		case StyleBold:
			texts = append(texts, rich.Bold(t))
		case StyleItalic:
			texts = append(texts, rich.Italic(t))
		case StyleFixed:
			texts = append(texts, rich.Fixed(t))
		case StyleStrike:
			texts = append(texts, rich.Strike(t))
		case StyleUnderline:
			texts = append(texts, rich.Underline(t))
		default:
			texts = append(texts, t)
		}
	}

	if len(texts) == 1 {
		return texts[0]
	}
	return rich.Concat(texts...)
}

// BuildRichMessage constructs a *tg.InputRichMessage object from the payload.
func BuildRichMessage(payload RichMessagePayload) *tg.InputRichMessage {
	var elements []tg.PageBlockClass

	if strings.TrimSpace(payload.Title) != "" {
		elements = append(elements, rich.Title(rich.Plain(strings.TrimSpace(payload.Title))))
	}

	if strings.TrimSpace(payload.Header) != "" {
		elements = append(elements, rich.Header(rich.Plain(strings.TrimSpace(payload.Header))))
	}

	for _, block := range payload.Blocks {
		switch strings.ToLower(block.Type) {
		case "title":
			elements = append(elements, rich.Title(FormatRichText(block.Segments, block.Text)))
		case "header":
			elements = append(elements, rich.Header(FormatRichText(block.Segments, block.Text)))
		case "paragraph", "text":
			elements = append(elements, rich.Paragraph(FormatRichText(block.Segments, block.Text)))
		case "list":
			items := make([]tg.PageListItemClass, 0, len(block.Items))
			for _, it := range block.Items {
				items = append(items, rich.ListItem(rich.Plain(it)))
			}
			if len(items) > 0 {
				elements = append(elements, rich.List(items...))
			}
		case "divider":
			elements = append(elements, rich.Divider())
		case "footer":
			elements = append(elements, rich.Footer(FormatRichText(block.Segments, block.Text)))
		}
	}

	if len(payload.ListItems) > 0 {
		items := make([]tg.PageListItemClass, 0, len(payload.ListItems))
		for _, it := range payload.ListItems {
			items = append(items, rich.ListItem(rich.Plain(it)))
		}
		elements = append(elements, rich.List(items...))
	}

	if payload.HasDivider {
		elements = append(elements, rich.Divider())
	}

	if strings.TrimSpace(payload.Footer) != "" {
		elements = append(elements, rich.Footer(rich.Plain(strings.TrimSpace(payload.Footer))))
	}

	return rich.New(elements...).Input()
}

// SendRichMessage resolves target and sends a structured rich message.
func SendRichMessage(ctx context.Context, api *tg.Client, target string, payload RichMessagePayload) (tg.UpdatesClass, error) {
	sender := message.NewSender(api)
	msg := BuildRichMessage(payload)

	target = strings.TrimSpace(target)
	if target == "" || strings.EqualFold(target, "me") || strings.EqualFold(target, "self") {
		return sender.Self().RichMessage(ctx, msg)
	}

	return sender.Resolve(target).RichMessage(ctx, msg)
}
