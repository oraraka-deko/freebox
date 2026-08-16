package telegram

import (
	"context"
	"strings"

	"github.com/gotd/log/logzap"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/updates"
	updhook "github.com/gotd/td/telegram/updates/hook"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// MessageHandler is a callback for incoming messages.
type MessageHandler func(ctx context.Context, e tg.Entities, msg *tg.Message) error

// UpdatesManager wraps gotd updates dispatcher, gap recovery engine, and middlewares.
type UpdatesManager struct {
	Dispatcher  tg.UpdateDispatcher
	Gaps        *updates.Manager
	Logger      *zap.Logger
	Middlewares []telegram.Middleware
}

// NewUpdatesManager creates an UpdatesManager with gap recovery and dispatcher hooks.
func NewUpdatesManager(log *zap.Logger) *UpdatesManager {
	if log == nil {
		log = zap.NewNop()
	}

	d := tg.NewUpdateDispatcher()
	gaps := updates.New(updates.Config{
		Handler: d,
		Logger:  logzap.New(log.Named("gaps")),
	})

	mws := []telegram.Middleware{
		updhook.UpdateHook(gaps.Handle),
		updhook.AffectedHook(gaps),
	}

	return &UpdatesManager{
		Dispatcher:  d,
		Gaps:        gaps,
		Logger:      log,
		Middlewares: mws,
	}
}

// OnMessage registers a handler for new private and group messages.
func (um *UpdatesManager) OnMessage(handler MessageHandler) {
	um.Dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}
		return handler(ctx, e, msg)
	})
}

// OnChannelMessage registers a handler for new channel and supergroup messages.
func (um *UpdatesManager) OnChannelMessage(handler MessageHandler) {
	um.Dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}
		return handler(ctx, e, msg)
	})
}

// RegisterAutoSaveBot registers an auto-save handler where replying "save" to any media message
// downloads the attachment into the designated output directory.
func (um *UpdatesManager) RegisterAutoSaveBot(api *tg.Client, outDir string) {
	sender := message.NewSender(api)

	um.Dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		msg, ok := u.Message.(*tg.Message)
		if !ok || msg.Out {
			return nil
		}
		if strings.ToLower(strings.TrimSpace(msg.Message)) != "save" {
			return nil
		}

		reply, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
		if !ok || reply.ReplyToMsgID == 0 {
			_, err := sender.Reply(e, u).Text(ctx, "Reply 'save' to a media message.")
			return err
		}

		res, err := SaveRepliedMessageMedia(ctx, api, reply.ReplyToMsgID, outDir)
		if err != nil {
			_, replyErr := sender.Reply(e, u).Text(ctx, "Error saving media: "+err.Error())
			return replyErr
		}

		_, replyErr := sender.Reply(e, u).Text(ctx, "Saved "+res.FileName)
		return replyErr
	})
}

// RunGaps starts gap recovery for the authenticated user ID.
func (um *UpdatesManager) RunGaps(ctx context.Context, api *tg.Client, userID int64, onStart func(ctx context.Context)) error {
	return um.Gaps.Run(ctx, api, userID, updates.AuthOptions{
		OnStart: onStart,
	})
}
