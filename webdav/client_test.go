package webdav

import (
	"context"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/webdav"
)

func setupWebDAVServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "webdav_test")
	if err != nil {
		t.Fatalf("mk temp dir failed: %v", err)
	}

	handler := &webdav.Handler{
		Prefix:     "/",
		FileSystem: webdav.Dir(tempDir),
		LockSystem: webdav.NewMemLS(),
	}

	server := httptest.NewServer(handler)
	return server, tempDir
}

func TestMkDirAndExists(t *testing.T) {
	server, tempDir := setupWebDAVServer(t)
	defer os.RemoveAll(tempDir)
	defer server.Close()

	client := NewClient(server.URL, "", "", nil)
	ctx := context.Background()

	testpaths := []string{"testdir", "testdir/subdir", "testdir/子目录", "/testdir/测试路径/测试路径2"}
	for _, p := range testpaths {
		exists, err := client.Exists(ctx, p)
		if err != nil {
			t.Fatalf("Call Exists Err: %v", err)
		}
		if exists {
			t.Fatalf("Dir should not exist")
		}

		if err := client.MkDir(ctx, p); err != nil {
			t.Fatalf("Call MkDir Err: %v", err)
		}

		exists, err = client.Exists(ctx, p)
		if err != nil {
			t.Fatalf("Call Exists Err: %v", err)
		}
		if !exists {
			t.Fatalf("Dir should exist")
		}
	}

}

func TestWriteFile(t *testing.T) {
	server, tempDir := setupWebDAVServer(t)
	defer os.RemoveAll(tempDir)
	defer server.Close()

	client := NewClient(server.URL, "", "", nil)
	ctx := context.Background()

	testCases := []struct {
		remotePath string
		content    string
	}{
		{
			remotePath: "hello.txt",
			content:    "Hello webdav",
		},
		{
			remotePath: "//nested/dir/test.txt",
			content:    "Nested file",
		},
		{
			remotePath: "nested/dir/test.txt",
			content:    "Nested file",
		},
		{
			remotePath: "empty.txt",
			content:    "",
		},
		{
			remotePath: "unicode.txt",
			content:    "测试",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.remotePath, func(t *testing.T) {
			dir := path.Dir(tc.remotePath)
			if dir != "." {
				if err := client.MkDir(ctx, dir); err != nil {
					t.Fatalf("创建目录 %s 失败: %v", dir, err)
				}
			}

			if err := client.WriteFile(ctx, tc.remotePath, strings.NewReader(tc.content)); err != nil {
				t.Fatalf("写入文件 %s 失败: %v", tc.remotePath, err)
			}

			localPath := filepath.Join(tempDir, tc.remotePath)
			data, err := os.ReadFile(localPath)
			if err != nil {
				t.Fatalf("读取文件 %s 失败: %v", localPath, err)
			}
			if string(data) != tc.content {
				t.Fatalf("文件内容不匹配: got %s, want %s", string(data), tc.content)
			}

			appended := tc.content + " Overwritten."
			if err := client.WriteFile(ctx, tc.remotePath, strings.NewReader(appended)); err != nil {
				t.Fatalf("覆盖写入文件 %s 失败: %v", tc.remotePath, err)
			}
			data, err = os.ReadFile(localPath)
			if err != nil {
				t.Fatalf("读取覆盖后的文件 %s 失败: %v", localPath, err)
			}
			if string(data) != appended {
				t.Fatalf("文件覆盖后的内容不匹配: got %s, want %s", string(data), appended)
			}
		})
	}
}

func TestDeleteMoveCopyStat(t *testing.T) {
	server, tempDir := setupWebDAVServer(t)
	defer os.RemoveAll(tempDir)
	defer server.Close()

	client := NewClient(server.URL, "", "", nil)
	ctx := context.Background()

	// Write file
	if err := client.WriteFile(ctx, "test1.txt", strings.NewReader("hello test 1")); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Stat
	info, err := client.Stat(ctx, "test1.txt")
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size != int64(len("hello test 1")) {
		t.Fatalf("Stat size mismatch: got %d", info.Size)
	}

	// Copy
	if err := client.Copy(ctx, "test1.txt", "test1_copy.txt"); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}
	if exists, _ := client.Exists(ctx, "test1_copy.txt"); !exists {
		t.Fatalf("test1_copy.txt should exist")
	}

	// Move
	if err := client.Move(ctx, "test1_copy.txt", "test1_moved.txt"); err != nil {
		t.Fatalf("Move failed: %v", err)
	}
	if exists, _ := client.Exists(ctx, "test1_copy.txt"); exists {
		t.Fatalf("test1_copy.txt should not exist after move")
	}
	if exists, _ := client.Exists(ctx, "test1_moved.txt"); !exists {
		t.Fatalf("test1_moved.txt should exist after move")
	}

	// Delete
	if err := client.Delete(ctx, "test1_moved.txt"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if exists, _ := client.Exists(ctx, "test1_moved.txt"); exists {
		t.Fatalf("test1_moved.txt should not exist after delete")
	}
}
