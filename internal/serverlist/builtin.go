package serverlist

import (
	_ "embed"
	"fmt"

	"dnsbench/internal/model"
)

//go:embed builtin_servers.json
var builtinJSON []byte

var builtinServers []model.Server

func init() {
	servers, err := DecodeJSON(builtinJSON)
	if err != nil {
		panic(fmt.Sprintf("built-in server list is invalid: %v", err))
	}
	seen := make(map[string]bool, len(servers))
	for _, s := range servers {
		if s.ID == "" {
			panic(fmt.Sprintf("built-in server %q has an empty ID", s.DisplayName()))
		}
		if seen[s.ID] {
			panic(fmt.Sprintf("built-in server list has duplicate ID %q", s.ID))
		}
		seen[s.ID] = true
	}
	builtinServers = servers
}

func Builtin() []model.Server {
	out := make([]model.Server, len(builtinServers))
	copy(out, builtinServers)
	return out
}
