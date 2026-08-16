package telegram

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"golang.org/x/term"
	"rsc.io/qr"
)

// TerminalAuthenticator implements auth.UserAuthenticator with terminal prompt and 2FA hint display.
type TerminalAuthenticator struct {
	PhoneNumber string
	Client      *telegram.Client
}

// Phone prompts for the phone number if not pre-filled.
func (t TerminalAuthenticator) Phone(ctx context.Context) (string, error) {
	if t.PhoneNumber != "" {
		return strings.TrimSpace(t.PhoneNumber), nil
	}

	fmt.Print("Enter Telegram Phone Number (international format, e.g. +1234567890): ")
	reader := bufio.NewReader(os.Stdin)
	phone, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(phone), nil
}

// Password prompts for the 2FA password and shows the hint if available.
func (t TerminalAuthenticator) Password(ctx context.Context) (string, error) {
	hint := ""
	if t.Client != nil {
		pwdInfo, err := t.Client.API().AccountGetPassword(ctx)
		if err == nil && pwdInfo != nil && pwdInfo.Hint != "" {
			hint = pwdInfo.Hint
		}
	}

	if hint != "" {
		fmt.Printf("Enter 2FA Password (Hint: %q): ", hint)
	} else {
		fmt.Print("Enter 2FA Password: ")
	}

	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		// Fallback to standard reader if terminal raw mode is unavailable
		reader := bufio.NewReader(os.Stdin)
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	return strings.TrimSpace(string(bytePassword)), nil
}

// AcceptTermsOfService automatically approves Telegram TOS.
func (t TerminalAuthenticator) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

// SignUp handles user registration if the account is new.
func (t TerminalAuthenticator) SignUp(ctx context.Context) (auth.UserInfo, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("First name: ")
	first, _ := reader.ReadString('\n')
	fmt.Print("Last name (optional): ")
	last, _ := reader.ReadString('\n')

	return auth.UserInfo{
		FirstName: strings.TrimSpace(first),
		LastName:  strings.TrimSpace(last),
	}, nil
}

// Code prompts the user to enter the Telegram verification code received via SMS/app.
func (t TerminalAuthenticator) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	codeType := "SMS / Telegram app"
	if sentCode != nil && sentCode.Type != nil {
		codeType = fmt.Sprintf("%T", sentCode.Type)
		if strings.Contains(codeType, "App") {
			codeType = "Telegram App"
		} else if strings.Contains(codeType, "Sms") {
			codeType = "SMS"
		}
	}

	fmt.Printf("Enter the verification code sent via %s: ", codeType)
	reader := bufio.NewReader(os.Stdin)
	code, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(code), nil
}

// DisplayQRCode prints an ASCII representation of the QR code to standard output.
func DisplayQRCode(token string) error {
	q, err := qr.Encode(token, qr.L)
	if err != nil {
		return err
	}

	fmt.Println("\nScan the QR code below with your Telegram App (Settings -> Devices -> Link Desktop Device):")
	fmt.Println()

	// Convert QR matrix to compact ASCII terminal art using half blocks
	w := q.Size
	// Add border
	for y := 0; y < w; y += 2 {
		for x := 0; x < w; x++ {
			top := q.Black(x, y)
			bottom := false
			if y+1 < w {
				bottom = q.Black(x, y+1)
			}

			if top && bottom {
				fmt.Print("█")
			} else if top && !bottom {
				fmt.Print("▀")
			} else if !top && bottom {
				fmt.Print("▄")
			} else {
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}
	fmt.Println()
	return nil
}

// InteractiveQRAuth runs QR-based authentication with 2FA password fallback.
func InteractiveQRAuth(ctx context.Context, client *telegram.Client, loggedIn qrlogin.LoggedIn) error {
	showQR := func(ctx context.Context, token qrlogin.Token) error {
		return DisplayQRCode(token.URL())
	}

	flow := auth.NewFlow(TerminalAuthenticator{Client: client}, auth.SendCodeOptions{})

	fmt.Println("Initializing QR code login...")
	if _, err := client.QR().Auth(ctx, loggedIn, showQR); err != nil {
		if !tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
			return err
		}
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}
	}
	return nil
}
