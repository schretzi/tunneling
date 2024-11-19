package main

import (
	"context"
	"sync"

	"github.com/cedws/iapc/iap"
	"github.com/spf13/viper"

	"github.com/charmbracelet/log"
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
