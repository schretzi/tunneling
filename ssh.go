package main

import (
	"time"

	"github.com/elliotchance/sshtunnel"

	"github.com/charmbracelet/log"
)

func startSSH(t *Tunnel) {
	var tunnelServer string
	if t.User != "" {
		tunnelServer = t.User + "@"
	}
	tunnelServer += t.TunnelHost + ":" + t.TunnelPort

	// log.Info(tunnelServer)
	remoteEndPoint := t.RemoteHost + ":" + t.RemotePort

	// log.Info(remoteEndPoint)
	ssh, err := sshtunnel.NewSSHTunnel(
		tunnelServer,
		sshtunnel.SSHAgent(),
		remoteEndPoint,
		t.LocalPort,
	)
	if err != nil {
		log.Error("Some error in ssh" + err.Error())
	}

	// ssh.Log = log.New(os.Stdout)

	go ssh.Start()
	log.Info(t.Name + ": SSH Connected on " + t.LocalPort)
	time.Sleep(100 * time.Millisecond)
}
