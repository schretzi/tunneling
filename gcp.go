package main

import (
	"context"
	"io"
	"net"

	"github.com/cedws/iapc/iap"

	"golang.org/x/oauth2/google"

	"github.com/charmbracelet/log"
)

func startIAP(ctx context.Context, t *Tunnel) {
	listener, err := net.Listen("tcp", ":"+t.LocalPort)
	if err != nil {
		log.Fatal(err)
	}

	log.Info(t.Name + ": GCP IAP Listening on " + t.LocalPort)
	t.status = "Listening"

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go startGcp(ctx, conn, t)

	}
}

func startGcp(ctx context.Context, conn net.Conn, t *Tunnel) {
	tokenSource, err := google.DefaultTokenSource(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	opts := []iap.DialOption{
		iap.WithProject(t.Project),
		iap.WithInstance(t.RemoteHost, t.Zone, t.Nic),
		iap.WithPort(t.RemotePort),
		iap.WithTokenSource(&tokenSource),
	}

	t.conn, err = iap.Dial(ctx, opts...)
	if err != nil {
		log.Fatal(err)
	}

	log.Info(t.Name + ": Connected to " + t.Name + " on " + t.RemotePort)

	go func() {
		if _, err := io.Copy(conn, t.conn); err != nil {
			log.Debug(err)
		}
	}()

	if _, err := io.Copy(t.conn, conn); err != nil {
		log.Debug(err)
	}
	log.Info(t.Name + ": Disconnected")
}
