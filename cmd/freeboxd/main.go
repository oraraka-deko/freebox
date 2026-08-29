package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"freebox/api"
	"freebox/ipc"
)

func main() {
	dbPath := flag.String("db", "./data/freebox.db", "path to the encrypted database file")
	passphrase := flag.String("passphrase", "freebox-secret-passphrase", "database encryption passphrase")
	workers := flag.Int("workers", 4, "max concurrent task workers")
	streamPort := flag.String("stream-port", ":8090", "internal stream proxy port")
	socketPath := flag.String("socket", "./freebox.sock", "Unix domain socket path for the IPC server")
	tcpAddr := flag.String("tcp-addr", "", "optional TCP loopback address for the IPC server, e.g. 127.0.0.1:9191")
	apiAddr := flag.String("api-addr", "", "optional address to also start the legacy REST/WebSocket API server, e.g. :8080")
	flag.Parse()

	inst, err := ipc.NewInstance(ipc.InstanceConfig{
		DBPath:     *dbPath,
		Passphrase: *passphrase,
		MaxWorkers: *workers,
		StreamPort: *streamPort,
	})
	if err != nil {
		log.Fatalf("freeboxd: failed to initialize instance: %v", err)
	}
	defer inst.Close()

	if *apiAddr != "" {
		if err := startLegacyAPIServer(inst, *apiAddr); err != nil {
			log.Fatalf("freeboxd: failed to start API server: %v", err)
		}
	}

	server := ipc.NewServer(inst, *socketPath, *tcpAddr)
	if err := server.Start(); err != nil {
		log.Fatalf("freeboxd: failed to start IPC server: %v", err)
	}
	defer server.Close()

	log.Printf("freeboxd: listening on socket=%q tcp=%q", *socketPath, *tcpAddr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("freeboxd: shutting down")
}

// startLegacyAPIServer optionally starts the pre-existing REST/WebSocket API
// server alongside the IPC socket, for clients that haven't migrated yet.
func startLegacyAPIServer(inst *ipc.Instance, addr string) error {
	srv := api.NewServer(api.ServerConfig{
		Addr:        addr,
		AuthMgr:     inst.AuthMgr,
		StorageDB:   inst.DB,
		Mounts:      inst.Mounts,
		Engine:      inst.Engine,
		StreamServ:  inst.Engine.StreamServer(),
		MetaMgr:     inst.MetaMgr,
		RemotesMgr:  inst.RemotesMgr,
		ThumbMgr:    inst.ThumbMgr,
		CertMgr:     inst.CertMgr,
	//	TelegramMgr: inst.TelegramMgr,
	})
	inst.APIServer = srv
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("freeboxd: API server stopped: %v", err)
		}
	}()
	return nil
}
