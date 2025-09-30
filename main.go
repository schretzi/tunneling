package main

import (
	"context"
	"sync"
	"os"
	"net"
	"fmt"
	
	"github.com/cedws/iapc/iap"
	"github.com/spf13/viper"

	"github.com/charmbracelet/log"

	"golang.org/x/crypto/ssh/agent"
)

type Tunnel struct {
	Name       string
	Kind       string `mapstructure:"kind"`
	RemoteHost string `mapstructure:"remoteHost"`
	RemotePort string `mapstructure:"remotePort"`
	TunnelHost string `mapstructure:"tunnelHost"`
	TunnelPort string `mapstructure:"tunnelPort"`
	LocalPort  string `mapstructure:"localPort"`
	Project    string `mapstructure:"project"`
	Zone       string `mapstructure:"zone"`
	Nic        string `mapstructure:"nic"`
	User       string `mapstructure:"user"`

	conn   *iap.Conn
	status string
}

var tunnels map[string]Tunnel

func main() {
	identities, err := GetSSHAgentIdentities()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	if len(identities) == 0 {
		log.Fatal("SSH agent is running but holds no identities (keys).")
	}

	ctx := context.Background()

	tunnels = make(map[string]Tunnel)

	readConfig()

	var wg sync.WaitGroup
	startTunnels(ctx, &wg)

	wg.Wait()
}

func readConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME/.config/tunneling")

	viper.ReadInConfig()
	viper.UnmarshalKey("tunnels", &tunnels)
}

func startTunnels(ctx context.Context, wg *sync.WaitGroup) {
	for k, t := range tunnels {
		t.Name = k
		wg.Add(1)
		if t.Kind == "gcp" {
			go func() {
				defer wg.Done()
				startIAP(ctx, &t)
				log.Info(t.Name + ": Stopped GCP IAP")
			}()
		} else if t.Kind == "ssh" {
			go func() {
				defer wg.Done()
				startSSH(&t)
			}()
		}
	}
}

func GetSSHAgentIdentities() ([]*agent.Key, error) {
	socketPath := os.Getenv("SSH_AUTH_SOCK")
	if socketPath == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK environment variable not set, ssh-agent may not be running")
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ssh-agent socket: %w", err)
	}
	defer conn.Close()

	agentClient := agent.NewClient(conn)

	identities, err := agentClient.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list identities from ssh-agent: %w", err)
	}

	return identities, nil
}


