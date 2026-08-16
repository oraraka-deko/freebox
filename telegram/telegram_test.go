package telegram

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"freebox/storage"
)

func TestAccountStorage(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: "test-passphrase",
	})
	if err != nil {
		t.Fatalf("failed to open storage db: %v", err)
	}
	defer db.Close()

	mgr, err := NewSessionStorageManager(db, tempDir)
	if err != nil {
		t.Fatalf("failed to create session storage manager: %v", err)
	}

	acc := &Account{
		ID:         123456789,
		Phone:      "+1234567890",
		Username:   "testuser",
		FirstName:  "John",
		LastName:   "Doe",
		IsBot:      false,
		SessionDir: mgr.GetAccountDir(123456789, "+1234567890"),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// Test SaveAccount
	if err := mgr.SaveAccount(acc); err != nil {
		t.Fatalf("failed to save account: %v", err)
	}

	// Test GetAccount
	fetched, err := mgr.GetAccount(123456789)
	if err != nil {
		t.Fatalf("failed to get account by id: %v", err)
	}
	if fetched.Username != "testuser" || fetched.Phone != "+1234567890" {
		t.Errorf("unexpected account data: %+v", fetched)
	}

	// Test GetAccountByPhone
	byPhone, err := mgr.GetAccountByPhone("+1234567890")
	if err != nil {
		t.Fatalf("failed to get account by phone: %v", err)
	}
	if byPhone.ID != 123456789 {
		t.Errorf("unexpected account id: %d", byPhone.ID)
	}

	// Test DisplayName
	expectedDisplayName := "John Doe (@testuser)"
	if acc.DisplayName() != expectedDisplayName {
		t.Errorf("expected display name %q, got %q", expectedDisplayName, acc.DisplayName())
	}

	// Test ListAccounts
	list, err := mgr.ListAccounts()
	if err != nil {
		t.Fatalf("failed to list accounts: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 account, got %d", len(list))
	}

	// Test DeleteAccount
	if err := mgr.DeleteAccount(123456789); err != nil {
		t.Fatalf("failed to delete account: %v", err)
	}

	_, err = mgr.GetAccount(123456789)
	if err == nil {
		t.Errorf("expected error after deletion, got nil")
	}
}

func TestFlowCoordinator(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: "test-passphrase",
	})
	if err != nil {
		t.Fatalf("failed to open storage db: %v", err)
	}
	defer db.Close()

	mgr, err := NewManager(db, AuthConfig{
		AppID:          12345,
		AppHash:        "mockhash0123456789abcdef",
		SessionBaseDir: tempDir,
	})
	if err != nil {
		t.Fatalf("failed to init manager: %v", err)
	}

	fc := mgr.FlowCoordinator()
	if fc == nil {
		t.Fatal("expected non-nil flow coordinator")
	}
	defer fc.Close()

	// Test non-existent session
	_, err = fc.GetFlowStatus("+999999999")
	if err == nil {
		t.Errorf("expected ErrFlowNotFound, got nil")
	}

	// Test phone number validation
	_, err = fc.StartPhoneAuth(context.Background(), "")
	if err == nil {
		t.Errorf("expected error for empty phone, got nil")
	}
}

func TestSanitizePhone(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"+1 (234) 567-8900", "phone-12345678900"},
		{"+44 7911 123456", "phone-447911123456"},
		{"invalid", "account"},
	}

	for _, c := range cases {
		res := SanitizePhone(c.input)
		if res != c.expected {
			t.Errorf("SanitizePhone(%q) = %q; expected %q", c.input, res, c.expected)
		}
	}
}

func TestRichMessageBuilder(t *testing.T) {
	payload := RichMessagePayload{
		Title:  "Test Title",
		Header: "Test Header",
		Blocks: []RichMessageBlock{
			{
				Type: "paragraph",
				Segments: []RichTextSegment{
					{Text: "This is ", Style: StylePlain},
					{Text: "bold", Style: StyleBold},
					{Text: " and ", Style: StylePlain},
					{Text: "italic", Style: StyleItalic},
					{Text: " and ", Style: StylePlain},
					{Text: "code", Style: StyleFixed},
				},
			},
			{
				Type:  "list",
				Items: []string{"Item 1", "Item 2"},
			},
			{
				Type: "divider",
			},
			{
				Type: "footer",
				Text: "Sample Footer",
			},
		},
		ListItems:  []string{"Extra Item"},
		HasDivider: true,
		Footer:     "End of Message",
	}

	richMsg := BuildRichMessage(payload)
	if richMsg == nil {
		t.Fatal("expected non-nil rich message")
	}
	if len(richMsg.Blocks) == 0 {
		t.Errorf("expected non-empty blocks in rich message")
	}
}

func TestPrettyMiddleware(t *testing.T) {
	// Test FormatObject with string, struct, nil
	if res := FormatObject(nil); res != "<nil>" {
		t.Errorf("expected <nil>, got %s", res)
	}

	type sampleStruct struct {
		Name string
	}
	formatted := FormatObject(sampleStruct{Name: "Freebox"})
	if formatted == "" {
		t.Errorf("expected non-empty formatted object string")
	}

	// Test PrettyMiddleware creation
	mw := PrettyMiddleware()
	if mw == nil {
		t.Fatal("expected non-nil middleware function")
	}
}

func TestUpdatesManager(t *testing.T) {
	um := NewUpdatesManager(nil)
	if um == nil {
		t.Fatal("expected non-nil updates manager")
	}
	if um.Gaps == nil {
		t.Fatal("expected non-nil gaps manager")
	}

	msgReceived := false
	um.OnMessage(func(ctx context.Context, e storageDBEntities, msg *storageDBMessage) error {
		msgReceived = true
		return nil
	})

	_ = msgReceived
}

type (
	storageDBEntities = tg.Entities
	storageDBMessage  = tg.Message
)

func TestUploadValidation(t *testing.T) {
	// Test empty URL
	_, err := UploadFromURL(context.Background(), nil, UploadURLOptions{
		URL: "",
	})
	if err == nil {
		t.Error("expected error for empty URL, got nil")
	}

	// Test non-existent local file
	_, err = UploadLocalFile(context.Background(), nil, UploadFileOptions{
		FilePath: "non_existent_file_xyz_123.dat",
	})
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

