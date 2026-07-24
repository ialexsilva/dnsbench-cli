package transport

import (
	"fmt"

	"dnsbench/internal/model"
)

var _ Factory = New

func New(s model.Server, o Options) (Querier, error) {
	switch s.Protocol {
	case model.ProtoUDP:
		if err := requireAddress(s); err != nil {
			return nil, err
		}
		return newUDPQuerier(s, o), nil
	case model.ProtoTCP:
		if err := requireAddress(s); err != nil {
			return nil, err
		}
		return newTCPQuerier(s, o), nil
	case model.ProtoDoT:
		if err := requireAddress(s); err != nil {
			return nil, err
		}
		return newDoTQuerier(s, o), nil
	case model.ProtoDoH:
		if s.DoHURL == "" {
			return nil, fmt.Errorf("transport: server %q uses DoH but has no DoH URL", s.DisplayName())
		}
		return newDoHQuerier(s, o), nil
	default:
		return nil, fmt.Errorf("transport: unknown protocol %q for server %q", s.Protocol, s.DisplayName())
	}
}

func requireAddress(s model.Server) error {
	if s.Address == "" {
		return fmt.Errorf("transport: server %q uses %s but has no address", s.DisplayName(), s.Protocol.Label())
	}
	return nil
}
