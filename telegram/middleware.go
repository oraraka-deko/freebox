package telegram

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tdp"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// PrettyMiddlewareOptions configures pretty-printing middleware behavior.
type PrettyMiddlewareOptions struct {
	Writer io.Writer
	Logger *zap.Logger
}

// PrettyOption is a functional option for configuring PrettyMiddleware.
type PrettyOption func(*PrettyMiddlewareOptions)

// WithPrettyWriter sets custom output writer for pretty print logs.
func WithPrettyWriter(w io.Writer) PrettyOption {
	return func(o *PrettyMiddlewareOptions) {
		o.Writer = w
	}
}

// WithPrettyLogger sets a zap.Logger for logging structured RPC calls.
func WithPrettyLogger(lg *zap.Logger) PrettyOption {
	return func(o *PrettyMiddlewareOptions) {
		o.Logger = lg
	}
}

// PrettyMiddleware returns a telegram.MiddlewareFunc that formats and logs TDLib/gotd TL RPC requests and responses.
func PrettyMiddleware(opts ...PrettyOption) telegram.MiddlewareFunc {
	cfg := PrettyMiddlewareOptions{
		Writer: os.Stdout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			reqFormatted := FormatObject(input)
			if cfg.Logger != nil {
				cfg.Logger.Debug("Telegram RPC Request", zap.String("req", reqFormatted))
			} else if cfg.Writer != nil {
				_, _ = fmt.Fprintf(cfg.Writer, "→ %s\n", reqFormatted)
			}

			start := time.Now()
			if err := next.Invoke(ctx, input, output); err != nil {
				duration := time.Since(start).Round(time.Millisecond)
				if cfg.Logger != nil {
					cfg.Logger.Error("Telegram RPC Error", zap.Duration("duration", duration), zap.Error(err))
				} else if cfg.Writer != nil {
					_, _ = fmt.Fprintf(cfg.Writer, "← (%s) ERROR: %v\n", duration, err)
				}
				return err
			}

			duration := time.Since(start).Round(time.Millisecond)
			resFormatted := FormatObject(output)
			if cfg.Logger != nil {
				cfg.Logger.Debug("Telegram RPC Response", zap.Duration("duration", duration), zap.String("res", resFormatted))
			} else if cfg.Writer != nil {
				_, _ = fmt.Fprintf(cfg.Writer, "← (%s) %s\n", duration, resFormatted)
			}

			return nil
		}
	}
}

// FormatObject pretty-prints any TL object or Box value using tdp.Format.
func FormatObject(input interface{}) string {
	if input == nil {
		return "<nil>"
	}

	o, ok := input.(tdp.Object)
	if !ok {
		// Handle tg.*Box values or pointer wrappers via reflection
		rv := reflect.Indirect(reflect.ValueOf(input))
		if rv.IsValid() && rv.Kind() == reflect.Struct {
			for i := 0; i < rv.NumField(); i++ {
				if v, ok := rv.Field(i).Interface().(tdp.Object); ok {
					return FormatObject(v)
				}
			}
		}

		return fmt.Sprintf("%T (not tdp.Object)", input)
	}

	return tdp.Format(o)
}
