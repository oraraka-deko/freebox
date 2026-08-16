package clipboard

import (
	"testing"

	"freebox/vfs"
)

func TestClipboardCopyAndCut(t *testing.T) {
	cb := NewClipboard()
	fs := vfs.NewMemFS()

	var changeCount int
	cb.OnChange(func(items []Item) {
		changeCount++
	})

	// 1. Stage Copy
	cb.Copy(fs, "/docs/file1.txt", "/docs/file2.txt")
	if cb.Count() != 2 {
		t.Fatalf("expected 2 items, got %d", cb.Count())
	}
	if cb.IsEmpty() {
		t.Errorf("clipboard should not be empty")
	}

	plans, err := cb.PlanPaste(fs, "/destination")
	if err != nil {
		t.Fatalf("plan paste failed: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].DstPath != "/destination/file1.txt" || plans[1].DstPath != "/destination/file2.txt" {
		t.Errorf("unexpected destination paths: %+v", plans)
	}

	// 2. Stage Cut
	cb.Cut(fs, "/photos/img1.jpg")
	if cb.Count() != 1 {
		t.Fatalf("expected 1 item, got %d", cb.Count())
	}
	cutPlans, err := cb.PlanPaste(fs, "/vault")
	if err != nil || len(cutPlans) != 1 {
		t.Fatalf("cut plan failed: %v", err)
	}
	if cutPlans[0].Op != OpCut || cutPlans[0].DstPath != "/vault/img1.jpg" {
		t.Errorf("unexpected cut plan: %+v", cutPlans[0])
	}

	// 3. Clear
	cb.Clear()
	if !cb.IsEmpty() || cb.Count() != 0 {
		t.Errorf("clipboard should be empty after clear")
	}

	if changeCount < 3 {
		t.Errorf("expected at least 3 change notifications, got %d", changeCount)
	}
}

func TestClipboardStageCreateAndDelete(t *testing.T) {
	cb := NewClipboard()
	fs := vfs.NewMemFS()

	// Stage Create
	cb.StageCreate("/newdir/newfile.txt", []byte("staged-content"), false)
	items := cb.Items()
	if len(items) != 1 || items[0].Op != OpCreate || string(items[0].Data) != "staged-content" {
		t.Fatalf("unexpected staged create item: %+v", items)
	}

	// Stage Delete
	cb.StageDelete(fs, "/trash/old.log")
	items = cb.Items()
	if len(items) != 1 || items[0].Op != OpDelete || items[0].Path != "/trash/old.log" {
		t.Fatalf("unexpected staged delete item: %+v", items)
	}
}
