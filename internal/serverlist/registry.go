package serverlist

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"dnsbench/internal/model"
)

func Merge(lists ...[]model.Server) []model.Server {
	seen := make(map[string]bool)
	var merged []model.Server
	for _, list := range lists {
		for _, s := range list {
			key := s.Key()
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, s)
		}
	}
	return merged
}

type FilterOptions struct {
	Protocols []model.Protocol
	Operators []string
	IDs       []string
	Sources   []model.Source
	IPv4Only  bool
}

func Filter(servers []model.Server, opts FilterOptions) []model.Server {
	var out []model.Server
	for _, s := range servers {
		if matchesFilter(s, opts) {
			out = append(out, s)
		}
	}
	return out
}

func matchesFilter(s model.Server, opts FilterOptions) bool {
	if len(opts.Protocols) > 0 && !slices.Contains(opts.Protocols, s.Protocol) {
		return false
	}
	if len(opts.Operators) > 0 && !containsFold(opts.Operators, s.Operator) {
		return false
	}
	if len(opts.IDs) > 0 && !slices.Contains(opts.IDs, s.ID) {
		return false
	}
	if len(opts.Sources) > 0 && !slices.Contains(opts.Sources, s.Source) {
		return false
	}
	if opts.IPv4Only && s.IsIPv6() {
		return false
	}
	return true
}

func containsFold(list []string, v string) bool {
	return slices.ContainsFunc(list, func(item string) bool {
		return strings.EqualFold(item, v)
	})
}

func ValidateAndPrepare(s *model.Server) error {
	switch s.Protocol {
	case model.ProtoUDP, model.ProtoTCP, model.ProtoDoT, model.ProtoDoQ:
		if _, err := netip.ParseAddr(s.Address); err != nil {
			return fmt.Errorf("%s server requires a valid IP address, got %q", s.Protocol.Label(), s.Address)
		}
	case model.ProtoDoH, model.ProtoDoH3:
		u, err := url.Parse(s.DoHURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("%s server requires a valid https URL, got %q", s.Protocol.Label(), s.DoHURL)
		}
		if s.Address != "" {
			if _, err := netip.ParseAddr(s.Address); err != nil {
				return fmt.Errorf("%s server bootstrap address must be an IP, got %q", s.Protocol.Label(), s.Address)
			}
		}
	default:
		return fmt.Errorf("unknown protocol %q (expected udp, tcp, dot, doh, doh3 or doq)", string(s.Protocol))
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("port %d is out of range 0-65535", s.Port)
	}
	if s.ID == "" {
		s.ID = generateID(*s)
	}
	s.Enabled = true
	return nil
}

func generateID(s model.Server) string {
	base := s.Name
	if base == "" {
		if s.Protocol.UsesURL() {
			base = strings.TrimPrefix(s.DoHURL, "https://")
		} else {
			base = s.Address
		}
	}
	return slugify(base + " " + string(s.Protocol))
}

func slugify(s string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(s) {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			if pending && b.Len() > 0 {
				b.WriteByte('-')
			}
			pending = false
			b.WriteRune(r)
		} else {
			pending = true
		}
	}
	return b.String()
}
