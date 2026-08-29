package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"freebox/api"
	"freebox/api/ws"
	"freebox/auth"
	"freebox/client"
	"freebox/engine"
	"freebox/storage"
	"freebox/telegram"
	"freebox/vfs"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	command := os.Args[1]
	switch command {
	case "server":
		runServer(os.Args[2:])
	case "login":
		runLogin(os.Args[2:])
	case "mount":
		runMount(os.Args[2:])
	case "ls":
		runLs(os.Args[2:])
	case "upload":
		runUpload(os.Args[2:])
	case "download":
		runDownload(os.Args[2:])
	case "transfer":
		runTransfer(os.Args[2:])
	case "stream":
		runStream(os.Args[2:])
	case "tasks", "task":
		runTasks(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
	}
}

func printUsage() {
	fmt.Println(`Freebox - Distributed File Management & Streaming Proxy Platform

Usage:
  freebox server [options]                      Start the Freebox Remote Orchestrator node
  freebox login [options]                       Authenticate client and store session token
  freebox mount <list|add|rm> [args]            Manage storage mounts (Local, SMB, FTP, WebDAV)
  freebox ls <mount>:<path>                     List directory entries on a mount
  freebox upload <local_file> <mount>:<path>    Upload a local file to a mount
  freebox download <mount>:<path> [dest_file]   Download a file from a mount
  freebox transfer <src_mount:path> <dst:path>  Transfer a file between mounts asynchronously
  freebox stream <mount>:<path> [options]       Register and stream a file via StreamProxy
  freebox task <list|watch> [id]                Inspect and watch running background tasks

Run 'freebox <command> --help' for command-specific flags.`)
}

// --- Server Command ---

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.String("port", ":8080", "Port to listen on (e.g. :8080)")
	dataDir := fs.String("data-dir", "./data", "Directory for database and state files")
	passphrase := fs.String("passphrase", "freebox-secret-passphrase", "Master passphrase for database encryption")
	adminUser := fs.String("admin-user", "admin", "Initial admin username")
	adminPass := fs.String("admin-pass", "admin123", "Initial admin password")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		log.Fatalf("Failed to create data dir: %v", err)
	}

	dbPath := filepath.Join(*dataDir, "freebox.db")
	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: *passphrase,
	})
	if err != nil {
		log.Fatalf("Failed to initialize encrypted database: %v", err)
	}
	defer db.Close()

	authMgr, err := auth.NewManager(db, auth.ManagerConfig{
		SessionTTL:       7 * 24 * time.Hour,
		DefaultAdminUser: *adminUser,
		DefaultAdminPass: *adminPass,
	})
	if err != nil {
		log.Fatalf("Failed to initialize auth manager: %v", err)
	}

	eng := engine.NewEngine(engine.DefaultEngineConfig())
	defer eng.Close()

	reg := vfs.NewRegistry(db)
	if err := reg.LoadAll(); err != nil {
		log.Printf("Warning: failed loading saved mounts: %v", err)
	}

	// Always ensure default local mount for convenient local file access
	if _, ok := reg.Get("local"); !ok {
		_ = reg.Mount("local", vfs.MountConfig{Type: "local", Path: "."}, false)
	}

	tgMgr, err := telegram.NewManager(db, telegram.AuthConfig{
		SessionBaseDir: filepath.Join(*dataDir, "telegram"),
	})
	if err != nil {
		log.Printf("Warning: failed initializing telegram manager: %v", err)
	}

	srv := api.NewServer(api.ServerConfig{
		Addr:        *port,
		AuthMgr:     authMgr,
		StorageDB:   db,
		Mounts:      reg,
		Engine:      eng,
		TelegramMgr: tgMgr,
	})

	log.Printf("=======================================================")
	log.Printf("Freebox Remote Orchestrator Node starting on %s", *port)
	log.Printf("Encrypted Database: %s", dbPath)
	log.Printf("Default Admin:      %s", *adminUser)
	log.Printf("REST API:           http://127.0.0.1%s/api", *port)
	log.Printf("WebSocket Gateway:  ws://127.0.0.1%s/api/ws", *port)
	log.Printf("Stream Playback:    http://127.0.0.1%s/stream/<name>", *port)
	log.Printf("=======================================================")

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("Server stopped: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down Freebox node...")
	_ = srv.Close()
}

// --- Client CLI Helpers ---

func getClientConfig() *client.Client {
	serverURL := os.Getenv("FREEBOX_SERVER")
	if serverURL == "" {
		serverURL = "http://127.0.0.1:8080"
	}
	c := client.NewClient(serverURL)
	token := os.Getenv("FREEBOX_TOKEN")
	if token != "" {
		c.SetToken(token)
	}
	return c
}

func parseMountPath(target string) (string, string, error) {
	parts := strings.SplitN(target, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: expected <mount>:<path> (e.g. nas:/videos/movie.mp4), got %s", target)
	}
	return parts[0], parts[1], nil
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "http://127.0.0.1:8080", "Freebox server URL")
	user := fs.String("u", "admin", "Username")
	pass := fs.String("p", "admin123", "Password")
	_ = fs.Parse(args)

	c := client.NewClient(*server)
	session, err := c.Login(*user, *pass)
	if err != nil {
		log.Fatalf("Login failed: %v", err)
	}

	fmt.Printf("Login successful!\n")
	fmt.Printf("Username:   %s\n", session.Username)
	fmt.Printf("Role:       %s\n", session.Role)
	fmt.Printf("API Token:  %s\n", session.Token)
	fmt.Printf("Expires:    %s\n\n", session.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("To use with subsequent commands, set environment variables:\n")
	fmt.Printf("  export FREEBOX_SERVER=%s\n", *server)
	fmt.Printf("  export FREEBOX_TOKEN=%s\n", session.Token)
}

func runMount(args []string) {
	if len(args) == 0 || args[0] == "list" {
		c := getClientConfig()
		mounts, err := c.ListMounts()
		if err != nil {
			log.Fatalf("Failed to list mounts: %v", err)
		}
		fmt.Printf("%-15s %-10s %-10s %-30s\n", "NAME", "TYPE", "STATUS", "PATH/HOST")
		fmt.Println(strings.Repeat("-", 70))
		for _, m := range mounts {
			loc := m.Path
			if loc == "" {
				loc = m.Host
			}
			if loc == "" {
				loc = m.URL
			}
			fmt.Printf("%-15s %-10s %-10s %-30s\n", m.Name, m.Type, m.Status, loc)
		}
		return
	}

	sub := args[0]
	switch sub {
	case "add":
		if len(args) < 3 {
			log.Fatalf("Usage: freebox mount add <name> <type: local|mem|webdav> [--path /path] [--url http://...] [--persist]")
		}
		name := args[1]
		mType := args[2]
		fs := flag.NewFlagSet("mount-add", flag.ExitOnError)
		path := fs.String("path", "", "Root path for local mount")
		url := fs.String("url", "", "URL for WebDAV")
		persist := fs.Bool("persist", true, "Save mount to encrypted storage")
		_ = fs.Parse(args[3:])

		c := getClientConfig()
		err := c.Mount(name, vfs.MountConfig{
			Type: mType,
			Path: *path,
			URL:  *url,
		}, *persist)
		if err != nil {
			log.Fatalf("Mount failed: %v", err)
		}
		fmt.Printf("Mount %s (%s) registered successfully.\n", name, mType)

	case "rm", "delete", "unmount":
		if len(args) < 2 {
			log.Fatalf("Usage: freebox mount rm <name>")
		}
		name := args[1]
		c := getClientConfig()
		if err := c.Unmount(name); err != nil {
			log.Fatalf("Unmount failed: %v", err)
		}
		fmt.Printf("Mount %s removed.\n", name)
	}
}

func runLs(args []string) {
	if len(args) < 1 {
		log.Fatalf("Usage: freebox ls <mount>:<path>")
	}
	mount, path, err := parseMountPath(args[0])
	if err != nil {
		log.Fatal(err)
	}

	c := getClientConfig()
	entries, err := c.ReadDir(mount, path)
	if err != nil {
		log.Fatalf("ls failed: %v", err)
	}

	fmt.Printf("%-10s %-12s %-20s %s\n", "TYPE", "SIZE", "MODIFIED", "NAME")
	fmt.Println(strings.Repeat("-", 60))
	for _, entry := range entries {
		t := "FILE"
		if entry.IsDir {
			t = "DIR"
		}
		fmt.Printf("%-10s %-12d %-20s %s\n", t, entry.Size, entry.ModTime.Format("2006-01-02 15:04:05"), entry.Name)
	}
}

func runUpload(args []string) {
	if len(args) < 2 {
		log.Fatalf("Usage: freebox upload <local_file> <mount>:<remote_path>")
	}
	localPath := args[0]
	mount, remotePath, err := parseMountPath(args[1])
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Open(localPath)
	if err != nil {
		log.Fatalf("Failed to open local file: %v", err)
	}
	defer f.Close()

	c := getClientConfig()
	fmt.Printf("Uploading %s to %s:%s...\n", localPath, mount, remotePath)
	if err := c.Upload(mount, remotePath, f); err != nil {
		log.Fatalf("Upload failed: %v", err)
	}
	fmt.Println("Upload completed successfully!")
}

func runDownload(args []string) {
	if len(args) < 1 {
		log.Fatalf("Usage: freebox download <mount>:<remote_path> [dest_file]")
	}
	mount, remotePath, err := parseMountPath(args[0])
	if err != nil {
		log.Fatal(err)
	}

	destFile := filepath.Base(remotePath)
	if len(args) >= 2 {
		destFile = args[1]
	}

	c := getClientConfig()
	rc, err := c.Download(mount, remotePath)
	if err != nil {
		log.Fatalf("Download failed: %v", err)
	}
	defer rc.Close()

	out, err := os.Create(destFile)
	if err != nil {
		log.Fatalf("Failed to create destination file: %v", err)
	}
	defer out.Close()

	written, err := io.Copy(out, rc)
	if err != nil {
		log.Fatalf("Download stream failed: %v", err)
	}
	fmt.Printf("Downloaded %d bytes to %s\n", written, destFile)
}

func runTransfer(args []string) {
	if len(args) < 2 {
		log.Fatalf("Usage: freebox transfer <src_mount>:<src_path> <dst_mount>:<dst_path> [--move]")
	}
	srcMount, srcPath, err := parseMountPath(args[0])
	if err != nil {
		log.Fatal(err)
	}
	dstMount, dstPath, err := parseMountPath(args[1])
	if err != nil {
		log.Fatal(err)
	}

	move := false
	if len(args) >= 3 && args[2] == "--move" {
		move = true
	}

	c := getClientConfig()
	task, err := c.Transfer(srcMount, srcPath, dstMount, dstPath, move)
	if err != nil {
		log.Fatalf("Transfer task initiation failed: %v", err)
	}

	fmt.Printf("Transfer task submitted: [ID: %s]\n", task.ID)
	fmt.Printf("Description: %s\n", task.Description)
	fmt.Printf("To monitor progress in real-time, run: freebox task watch %s\n", task.ID)
}

func runStream(args []string) {
	if len(args) < 1 {
		log.Fatalf("Usage: freebox stream <mount>:<path> [--name custom_name]")
	}
	mount, p, err := parseMountPath(args[0])
	if err != nil {
		log.Fatal(err)
	}

	streamName := filepath.Base(p)
	fs := flag.NewFlagSet("stream", flag.ExitOnError)
	fs.StringVar(&streamName, "name", streamName, "Custom stream proxy name")
	_ = fs.Parse(args[1:])

	c := getClientConfig()
	if err := c.RegisterStream(streamName, mount, p, ""); err != nil {
		log.Fatalf("Stream registration failed: %v", err)
	}

	playURL := c.StreamURL(streamName)
	fmt.Printf("Stream registered successfully!\n")
	fmt.Printf("Playback URL: %s\n", playURL)
}

func runTasks(args []string) {
	if len(args) == 0 || args[0] == "list" {
		c := getClientConfig()
		tasks, err := c.ListTasks()
		if err != nil {
			log.Fatalf("Failed to list tasks: %v", err)
		}
		fmt.Printf("%-25s %-10s %-12s %-8s %s\n", "TASK ID", "TYPE", "STATUS", "PROGRESS", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 80))
		for _, t := range tasks {
			fmt.Printf("%-25s %-10s %-12s %-7.1f%% %s\n", t.ID, t.Type, t.Status, t.Progress.Percent, t.Description)
		}
		return
	}

	sub := args[0]
	if sub == "watch" {
		if len(args) < 2 {
			log.Fatalf("Usage: freebox task watch <task_id>")
		}
		taskID := args[1]
		c := getClientConfig()

		wsChan, err := c.ConnectWebSocket()
		if err != nil {
			log.Fatalf("Failed connecting WebSocket: %v", err)
		}
		_ = c.SubscribeWS("tasks:" + taskID)

		fmt.Printf("Watching task %s (Press Ctrl+C to exit)...\n", taskID)
		for event := range wsChan {
			if event.Type == "task_progress" {
				if dataMap, ok := event.Data.(map[string]interface{}); ok {
					percent, _ := dataMap["percent"].(float64)
					status, _ := dataMap["status"].(string)
					bytesProc, _ := dataMap["bytes_processed"].(float64)
					speed, _ := dataMap["speed_bytes_sec"].(float64)
					fmt.Printf("\r[Status: %-10s] Progress: %6.1f%% (%d KB) Speed: %.2f KB/s",
						status, percent, int64(bytesProc)/1024, speed/1024)
					if status == "COMPLETED" || status == "FAILED" || status == "CANCELED" {
						fmt.Printf("\nTask reached terminal state: %s\n", status)
						return
					}
				}
			}
		}
	}
}

var (
	_ = ws.Event{}
)
