package router

import (
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

type Connection struct {
	ID      string
	BaseURL string
	APIKey  string
}

func connectionFromRouter(c *pluginv1.RouterConnection) Connection {
	return Connection{
		ID:      c.GetId(),
		BaseURL: c.GetBaseUrl(),
		APIKey:  c.GetApiKey(),
	}
}
