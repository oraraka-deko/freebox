package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
)

var (
	ErrNoMediaFound    = errors.New("message contains no downloadable media")
	ErrMessageNotFound = errors.New("telegram message not found")
)

// DownloadResult contains details of the downloaded media.
type DownloadResult struct {
	FileName  string `json:"file_name"`
	FilePath  string `json:"file_path"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
}

// ExtractMessageFile extracts the downloadable file metadata and location from a message.
func ExtractMessageFile(msg *tg.Message) (messages.File, bool) {
	if msg == nil {
		return messages.File{}, false
	}
	return messages.Elem{Msg: msg}.File()
}

// DownloadLocation downloads a Telegram file location to the target path.
func DownloadLocation(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, destPath string) (int64, error) {
	if loc == nil {
		return 0, errors.New("nil file location")
	}

	dir := filepath.Dir(destPath)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return 0, fmt.Errorf("failed to create destination directory: %w", err)
		}
	}

	d := downloader.NewDownloader()
	if _, err := d.Download(api, loc).ToPath(ctx, destPath); err != nil {
		return 0, err
	}

	var size int64
	if fi, err := os.Stat(destPath); err == nil {
		size = fi.Size()
	}
	return size, nil
}

// SaveMessageMedia extracts media from a tg.Message and downloads it to the output directory.
func SaveMessageMedia(ctx context.Context, api *tg.Client, msg *tg.Message, outDir string) (*DownloadResult, error) {
	file, ok := ExtractMessageFile(msg)
	if !ok {
		return nil, ErrNoMediaFound
	}

	fileName := strings.TrimSpace(file.Name)
	if fileName == "" {
		fileName = fmt.Sprintf("telegram_media_%d", msg.ID)
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	destPath := filepath.Join(outDir, fileName)
	if _, err := downloader.NewDownloader().Download(api, file.Location).ToPath(ctx, destPath); err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}

	var size int64
	if fi, err := os.Stat(destPath); err == nil {
		size = fi.Size()
	}

	return &DownloadResult{
		FileName:  fileName,
		FilePath:  destPath,
		MIMEType:  file.MIMEType,
		SizeBytes: size,
	}, nil
}

// FetchMessageByID fetches a specific message by ID from peer or dialog.
func FetchMessageByID(ctx context.Context, api *tg.Client, msgID int) (*tg.Message, error) {
	res, err := api.MessagesGetMessages(ctx, []tg.InputMessageClass{
		&tg.InputMessageID{ID: msgID},
	})
	if err != nil {
		return nil, err
	}

	var raw []tg.MessageClass
	switch m := res.(type) {
	case *tg.MessagesMessages:
		raw = m.Messages
	case *tg.MessagesMessagesSlice:
		raw = m.Messages
	case *tg.MessagesChannelMessages:
		raw = m.Messages
	default:
		return nil, ErrMessageNotFound
	}

	if len(raw) == 0 {
		return nil, ErrMessageNotFound
	}

	msg, ok := raw[0].(*tg.Message)
	if !ok {
		return nil, ErrMessageNotFound
	}

	return msg, nil
}

// SaveRepliedMessageMedia fetches the parent message that was replied to and downloads its media.
func SaveRepliedMessageMedia(ctx context.Context, api *tg.Client, replyToMsgID int, outDir string) (*DownloadResult, error) {
	if replyToMsgID == 0 {
		return nil, errors.New("invalid reply message ID")
	}

	replied, err := FetchMessageByID(ctx, api, replyToMsgID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch replied message: %w", err)
	}

	return SaveMessageMedia(ctx, api, replied, outDir)
}
