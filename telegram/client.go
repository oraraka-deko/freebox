package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/tg"
)

// SendMessageOptions holds parameters for sending text or rich messages.
type SendMessageOptions struct {
	Target   string              `json:"target"` // username, peer, or "me"/"self"
	Text     string              `json:"text,omitempty"`
	HTML     bool                `json:"html,omitempty"`
	Rich     *RichMessagePayload `json:"rich,omitempty"`
	ReplyTo  int                 `json:"reply_to,omitempty"`
}

// ClientService provides a high-level API over an authenticated Telegram client session.
type ClientService struct {
	client  *telegram.Client
	api     *tg.Client
	account *Account
}

// NewClientService wraps a gotd telegram.Client and its account.
func NewClientService(client *telegram.Client, account *Account) *ClientService {
	return &ClientService{
		client:  client,
		api:     client.API(),
		account: account,
	}
}

// Client returns the underlying *telegram.Client.
func (cs *ClientService) Client() *telegram.Client {
	return cs.client
}

// API returns the underlying *tg.Client.
func (cs *ClientService) API() *tg.Client {
	if cs.api == nil && cs.client != nil {
		cs.api = cs.client.API()
	}
	return cs.api
}

// Account returns the active account metadata.
func (cs *ClientService) Account() *Account {
	return cs.account
}

// SendMessage sends a text, HTML, or structured rich message.
func (cs *ClientService) SendMessage(ctx context.Context, opts SendMessageOptions) (tg.UpdatesClass, error) {
	if cs.api == nil {
		return nil, errors.New("client API not initialized")
	}

	sender := message.NewSender(cs.api)
	target := strings.TrimSpace(opts.Target)

	var builder *message.Builder
	if target == "" || strings.EqualFold(target, "me") || strings.EqualFold(target, "self") {
		b := sender.Self()
		builder = &b.Builder
	} else {
		b := sender.Resolve(target)
		builder = &b.Builder
	}

	if opts.ReplyTo > 0 {
		builder = builder.Reply(opts.ReplyTo)
	}

	// Rich message
	if opts.Rich != nil {
		richDoc := BuildRichMessage(*opts.Rich)
		return builder.RichMessage(ctx, richDoc)
	}

	// HTML or plain text
	if opts.HTML {
		return builder.StyledText(ctx, html.String(nil, opts.Text))
	}

	return builder.Text(ctx, opts.Text)
}

// SendText sends plain text to a target.
func (cs *ClientService) SendText(ctx context.Context, target string, text string) (tg.UpdatesClass, error) {
	return cs.SendMessage(ctx, SendMessageOptions{
		Target: target,
		Text:   text,
	})
}

// SendHTML sends HTML formatted text to a target.
func (cs *ClientService) SendHTML(ctx context.Context, target string, htmlText string) (tg.UpdatesClass, error) {
	return cs.SendMessage(ctx, SendMessageOptions{
		Target: target,
		Text:   htmlText,
		HTML:   true,
	})
}

// SendRich sends a structured rich message to a target.
func (cs *ClientService) SendRich(ctx context.Context, target string, payload RichMessagePayload) (tg.UpdatesClass, error) {
	return cs.SendMessage(ctx, SendMessageOptions{
		Target: target,
		Rich:   &payload,
	})
}

// UploadURL streams a remote URL directly to Telegram without disk buffering.
func (cs *ClientService) UploadURL(ctx context.Context, target string, url string, caption string, htmlCaption bool, timeout time.Duration) (tg.UpdatesClass, error) {
	return UploadFromURL(ctx, cs.api, UploadURLOptions{
		Target:      target,
		URL:         url,
		Caption:     caption,
		HTMLCaption: htmlCaption,
		Timeout:     timeout,
	})
}

// UploadFile uploads a local disk file to Telegram.
func (cs *ClientService) UploadFile(ctx context.Context, target string, filePath string, caption string, htmlCaption bool) (tg.UpdatesClass, error) {
	return UploadLocalFile(ctx, cs.api, UploadFileOptions{
		Target:      target,
		FilePath:    filePath,
		Caption:     caption,
		HTMLCaption: htmlCaption,
	})
}

// DownloadMedia saves an attachment from a tg.Message to a local directory.
func (cs *ClientService) DownloadMedia(ctx context.Context, msg *tg.Message, outDir string) (*DownloadResult, error) {
	return SaveMessageMedia(ctx, cs.api, msg, outDir)
}

// DownloadRepliedMedia fetches and saves the media from the message that was replied to.
func (cs *ClientService) DownloadRepliedMedia(ctx context.Context, replyToMsgID int, outDir string) (*DownloadResult, error) {
	return SaveRepliedMessageMedia(ctx, cs.api, replyToMsgID, outDir)
}
