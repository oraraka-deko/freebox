package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"freebox/proxy"
	"freebox/storage"
	"freebox/vfs"
)

func TestEngineImmediateOperations(t *testing.T) {
	eng := NewEngine(DefaultEngineConfig())
	defer eng.Close()

	fsA := vfs.NewMemFS()
	fsB := vfs.NewMemFS()

	// 1. Create file & dir (immediate)
	hCreate, err := eng.CreateFile(fsA, "/data/report.txt", []byte("engine-data"), false)
	if err != nil || hCreate.Status() != StatusCompleted {
		t.Fatalf("immediate create failed: %v", err)
	}

	// 2. Open file (immediate)
	rc, err := eng.Open(fsA, "/data/report.txt")
	if err != nil {
		t.Fatalf("immediate open failed: %v", err)
	}
	content, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(content) != "engine-data" {
		t.Errorf("unexpected content: %s", string(content))
	}

	// 3. Copy file (immediate)
	hCopy, err := eng.Copy(fsA, "/data/report.txt", fsB, "/backup/report.txt", false)
	if err != nil || hCopy.Status() != StatusCompleted {
		t.Fatalf("immediate copy failed: %v", err)
	}
	if !fsB.Exists("/backup/report.txt") {
		t.Errorf("copied file does not exist on fsB")
	}

	// 4. Move file (immediate)
	hMove, err := eng.Move(fsB, "/backup/report.txt", fsB, "/archive/report.txt", false)
	if err != nil || hMove.Status() != StatusCompleted {
		t.Fatalf("immediate move failed: %v", err)
	}
	if fsB.Exists("/backup/report.txt") || !fsB.Exists("/archive/report.txt") {
		t.Errorf("move failed in fsB")
	}

	// 5. Delete file (immediate)
	hDel, err := eng.Delete(fsA, "/data/report.txt", false)
	if err != nil || hDel.Status() != StatusCompleted {
		t.Fatalf("immediate delete failed: %v", err)
	}
	if fsA.Exists("/data/report.txt") {
		t.Errorf("deleted file still exists in fsA")
	}

	// 6. Proxy file (immediate)
	memSrc := proxy.NewMemSource([]byte("proxy-stream-content"))
	hProxy, err := eng.Proxy(memSrc, "sample.bin", false)
	if err != nil || hProxy.Status() != StatusCompleted {
		t.Fatalf("immediate proxy failed: %v", err)
	}
	streamURL, ok := hProxy.Result().(string)
	if !ok || !strings.Contains(streamURL, "/stream/sample.bin") {
		t.Errorf("invalid stream url: %v", streamURL)
	}
}

func TestEngineQueuedOperations(t *testing.T) {
	eng := NewEngine(DefaultEngineConfig())
	defer eng.Close()

	srcFS := vfs.NewMemFS()
	dstFS := vfs.NewMemFS()

	_ = srcFS.Write("/large.bin", make([]byte, 1024*1024)) // 1MB

	var progressCalled bool
	// 1. Submit queued copy task
	h, err := eng.Copy(srcFS, "/large.bin", dstFS, "/large.bin", true)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	h.OnProgress(func(p TaskProgress) {
		progressCalled = true
	})

	status := h.Wait()
	if status != StatusCompleted {
		t.Errorf("expected completed status, got %s", status)
	}

	if !dstFS.Exists("/large.bin") {
		t.Errorf("file was not copied to dstFS")
	}

	if !progressCalled {
		t.Errorf("expected progress callback to be triggered")
	}
}

func TestEngineTaskPauseResumeCancel(t *testing.T) {
	eng := NewEngine(DefaultEngineConfig())
	defer eng.Close()

	// Task that simulates work
	var executed atomicBool
	task := &Task{
		Type:        TaskTypeCustom,
		Description: "Simulated task",
		Params: TaskParams{
			Action: func(ctx context.Context, handle *TaskHandle) error {
				for i := 0; i < 5; i++ {
					if err := handle.CheckPause(); err != nil {
						return err
					}
					time.Sleep(10 * time.Millisecond)
				}
				executed.set(true)
				return nil
			},
		},
	}

	h, err := eng.Submit(task)
	if err != nil {
		t.Fatalf("submit error: %v", err)
	}

	// Test pause & resume
	time.Sleep(5 * time.Millisecond)
	_ = eng.PauseTask(h.ID())
	time.Sleep(20 * time.Millisecond)
	_ = eng.ResumeTask(h.ID())

	status := h.Wait()
	if status != StatusCompleted {
		t.Errorf("expected completed, got %s", status)
	}
	if !executed.get() {
		t.Errorf("expected custom task to execute")
	}
}

func TestEngineClipboardWorkflow(t *testing.T) {
	eng := NewEngine(DefaultEngineConfig())
	defer eng.Close()

	srcFS := vfs.NewMemFS()
	dstFS := vfs.NewMemFS()

	_ = srcFS.Write("/source/song.mp3", []byte("audio-bytes"))
	_ = srcFS.Write("/source/video.mp4", []byte("video-bytes"))

	// 1. Stage Copy to Clipboard
	eng.ClipboardCopy(srcFS, "/source/song.mp3", "/source/video.mp4")
	if eng.Clipboard().Count() != 2 {
		t.Fatalf("expected 2 items in clipboard, got %d", eng.Clipboard().Count())
	}

	// 2. Paste at destination (immediate)
	handles, err := eng.ClipboardPaste(dstFS, "/library", false)
	if err != nil {
		t.Fatalf("clipboard paste failed: %v", err)
	}
	if len(handles) != 2 {
		t.Fatalf("expected 2 handles, got %d", len(handles))
	}

	if !dstFS.Exists("/library/song.mp3") || !dstFS.Exists("/library/video.mp4") {
		t.Errorf("pasted files not found on dstFS")
	}

	// 3. Stage Cut to Clipboard
	eng.ClipboardCut(srcFS, "/source/song.mp3")
	handles, err = eng.ClipboardPaste(dstFS, "/moved", false)
	if err != nil {
		t.Fatalf("clipboard cut-paste failed: %v", err)
	}
	if len(handles) != 1 {
		t.Fatalf("expected 1 handle, got %d", len(handles))
	}
	if !dstFS.Exists("/moved/song.mp3") {
		t.Errorf("cut file not found on dstFS")
	}
	if srcFS.Exists("/source/song.mp3") {
		t.Errorf("cut file should have been removed from srcFS")
	}
	if !eng.Clipboard().IsEmpty() {
		t.Errorf("clipboard should be empty after cut-paste")
	}
}

func TestEngineLocalTransfers(t *testing.T) {
	tempDir := t.TempDir()
	eng := NewEngine(DefaultEngineConfig())
	defer eng.Close()

	remoteFS := vfs.NewMemFS()
	localFile := filepath.Join(tempDir, "local.txt")
	_ = os.WriteFile(localFile, []byte("local-to-remote-payload"), 0644)

	// Push local to remote
	hPush, err := eng.PushLocal(localFile, remoteFS, "/cloud/local.txt", false)
	if err != nil || hPush.Status() != StatusCompleted {
		t.Fatalf("push local failed: %v", err)
	}
	if !remoteFS.Exists("/cloud/local.txt") {
		t.Errorf("file not pushed to remoteFS")
	}

	// Pull remote to local
	downloadPath := filepath.Join(tempDir, "downloaded.txt")
	hPull, err := eng.PullLocal(remoteFS, "/cloud/local.txt", downloadPath, false)
	if err != nil || hPull.Status() != StatusCompleted {
		t.Fatalf("pull local failed: %v", err)
	}
	data, err := os.ReadFile(downloadPath)
	if err != nil || string(data) != "local-to-remote-payload" {
		t.Errorf("downloaded content mismatch: %v, content=%s", err, string(data))
	}
}

type atomicBool struct {
	v bool
}

func (a *atomicBool) set(val bool) {
	a.v = val
}

func (a *atomicBool) get() bool {
	return a.v
}

func TestEngineDBPersistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tasks.db")
	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: "test-passphrase",
	})
	if err != nil {
		t.Fatalf("failed to open storage db: %v", err)
	}
	defer db.Close()

	eng := NewEngine(EngineConfig{
		MaxWorkers: 2,
		DB:         db,
	})
	defer eng.Close()

	mem := vfs.NewMemFS()
	h, err := eng.CreateFile(mem, "/task_test.txt", []byte("data"), true)
	if err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	h.Wait()

	// Verify task history
	history, err := eng.ListHistory()
	if err != nil {
		t.Fatalf("ListHistory failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 task in history, got %d", len(history))
	}
	if history[0].ID != h.ID() || history[0].Status != StatusCompleted {
		t.Errorf("history mismatch: %+v", history[0])
	}

	// Verify delete task
	err = eng.DeleteTask(h.ID())
	if err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}
	historyAfter, _ := eng.ListHistory()
	if len(historyAfter) != 0 {
		t.Errorf("expected empty history after deletion, got %d", len(historyAfter))
	}
}

