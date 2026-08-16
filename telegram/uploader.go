package telegram

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/telegram/uploader/source"
	"github.com/gotd/td/tg"
)

// UploadURLOptions holds parameters for streaming a remote URL file into Telegram.
type UploadURLOptions struct {
	Target      string
	URL         string
	Caption     string
	HTMLCaption bool
	Timeout     time.Duration
	HTTPClient  *http.Client
}

// UploadFileOptions holds parameters for uploading a local file into Telegram.
type UploadFileOptions struct {
	Target      string
	FilePath    string
	Caption     string
	HTMLCaption bool
}

// UploadFromURL streams a file from an HTTP/HTTPS URL directly into Telegram without disk buffering.
func UploadFromURL(ctx context.Context, api *tg.Client, opts UploadURLOptions) (tg.UpdatesClass, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, errors.New("URL cannot be empty")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: timeout,
		}
	}

	src := source.NewHTTPSource().WithClient(httpClient)
	u := uploader.NewUploader(api)

	f, err := u.FromSource(ctx, src, opts.URL)
	if err != nil {
		return nil, err
	}

	sender := message.NewSender(api).WithUploader(u)

	caption := opts.Caption
	if caption == "" {
		caption = "Uploaded from " + opts.URL
	}

	var doc message.MediaOption
	if opts.HTMLCaption {
		doc = message.UploadedDocument(f, html.String(nil, caption))
	} else {
		doc = message.UploadedDocument(f, styling.Plain(caption))
	}

	target := strings.TrimSpace(opts.Target)
	if target == "" || strings.EqualFold(target, "me") || strings.EqualFold(target, "self") {
		return sender.Self().Media(ctx, doc)
	}

	return sender.Resolve(target).Media(ctx, doc)
}

// UploadLocalFile reads a local file from disk and uploads it to Telegram.
func UploadLocalFile(ctx context.Context, api *tg.Client, opts UploadFileOptions) (tg.UpdatesClass, error) {
	if strings.TrimSpace(opts.FilePath) == "" {
		return nil, errors.New("file path cannot be empty")
	}

	if _, err := os.Stat(opts.FilePath); err != nil {
		return nil, err
	}

	u := uploader.NewUploader(api)
	f, err := u.FromPath(ctx, opts.FilePath)
	if err != nil {
		return nil, err
	}

	sender := message.NewSender(api).WithUploader(u)

	caption := opts.Caption
	var doc message.MediaOption
	if caption != "" {
		if opts.HTMLCaption {
			doc = message.UploadedDocument(f, html.String(nil, caption))
		} else {
			doc = message.UploadedDocument(f, styling.Plain(caption))
		}
	} else {
		doc = message.UploadedDocument(f)
	}

	target := strings.TrimSpace(opts.Target)
	if target == "" || strings.EqualFold(target, "me") || strings.EqualFold(target, "self") {
		return sender.Self().Media(ctx, doc)
	}

	return sender.Resolve(target).Media(ctx, doc)
}
