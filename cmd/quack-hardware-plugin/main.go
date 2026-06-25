package main

import (
	"quack/internal/hardware"

	goplugin "github.com/hashicorp/go-plugin"
)

func main() {
	service := hardware.NewLocalService(hardware.NewUVCProvider())
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: hardware.Handshake,
		Plugins:         hardware.PluginMap(service),
	})
}
