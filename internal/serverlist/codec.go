package serverlist

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"dnsbench/internal/model"
)

type Format string

const (
	FormatJSON Format = "json"
	FormatCSV  Format = "csv"
	FormatText Format = "text"
)

var csvHeader = []string{"id", "name", "operator", "protocol", "address", "port", "tls_hostname", "doh_url", "notes", "enabled"}

func DetectFormat(filename string) (Format, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json":
		return FormatJSON, nil
	case ".csv":
		return FormatCSV, nil
	case ".txt":
		return FormatText, nil
	}
	return "", fmt.Errorf("unsupported file extension %q (expected .json, .csv or .txt)", ext)
}

func EncodeJSON(servers []model.Server) ([]byte, error) {
	if servers == nil {
		servers = []model.Server{}
	}
	data, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DecodeJSON(data []byte) ([]model.Server, error) {
	var servers []model.Server
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("invalid server list JSON: %w", err)
	}
	return servers, nil
}

func EncodeCSV(servers []model.Server) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvHeader); err != nil {
		return nil, err
	}
	for _, s := range servers {
		port := ""
		if s.Port != 0 {
			port = strconv.Itoa(s.Port)
		}
		record := []string{
			s.ID, s.Name, s.Operator, string(s.Protocol), s.Address,
			port, s.TLSHostname, s.DoHURL, s.Notes, strconv.FormatBool(s.Enabled),
		}
		if err := w.Write(record); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeCSV(data []byte) ([]model.Server, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid server list CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("invalid server list CSV: missing header row")
	}
	header := records[0]
	if len(header) != len(csvHeader) {
		return nil, fmt.Errorf("invalid CSV header: expected %d columns, got %d", len(csvHeader), len(header))
	}
	for i, col := range header {
		if !strings.EqualFold(strings.TrimSpace(col), csvHeader[i]) {
			return nil, fmt.Errorf("invalid CSV header: column %d is %q, expected %q", i+1, col, csvHeader[i])
		}
	}
	servers := make([]model.Server, 0, len(records)-1)
	for i, record := range records[1:] {
		s, err := decodeCSVRecord(record)
		if err != nil {
			return nil, fmt.Errorf("CSV row %d: %w", i+2, err)
		}
		servers = append(servers, s)
	}
	return servers, nil
}

func decodeCSVRecord(record []string) (model.Server, error) {
	port := 0
	if p := strings.TrimSpace(record[5]); p != "" {
		parsed, err := strconv.Atoi(p)
		if err != nil {
			return model.Server{}, fmt.Errorf("invalid port %q", p)
		}
		port = parsed
	}
	enabled := true
	if e := strings.TrimSpace(record[9]); e != "" {
		parsed, err := strconv.ParseBool(e)
		if err != nil {
			return model.Server{}, fmt.Errorf("invalid enabled value %q", e)
		}
		enabled = parsed
	}
	return model.Server{
		ID:          strings.TrimSpace(record[0]),
		Name:        strings.TrimSpace(record[1]),
		Operator:    strings.TrimSpace(record[2]),
		Protocol:    model.Protocol(strings.ToLower(strings.TrimSpace(record[3]))),
		Address:     strings.TrimSpace(record[4]),
		Port:        port,
		TLSHostname: strings.TrimSpace(record[6]),
		DoHURL:      strings.TrimSpace(record[7]),
		Notes:       strings.TrimSpace(record[8]),
		Enabled:     enabled,
	}, nil
}

func EncodeText(servers []model.Server) ([]byte, error) {
	var b strings.Builder
	for _, s := range servers {
		line, err := encodeTextLine(s)
		if err != nil {
			return nil, err
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func encodeTextLine(s model.Server) (string, error) {
	var endpoint string
	switch s.Protocol {
	case model.ProtoDoH, model.ProtoDoH3:
		if s.DoHURL == "" {
			return "", fmt.Errorf("cannot encode %s server %q as text: missing URL", s.Protocol.Label(), s.DisplayName())
		}
		endpoint = s.DoHURL
		if s.Protocol == model.ProtoDoH3 {
			endpoint = "h3://" + strings.TrimPrefix(endpoint, "https://")
		}
		if s.Address != "" {
			endpoint += "@" + formatTextAddr(s.Address, s.Port, s.Protocol)
		}
	case model.ProtoDoT, model.ProtoDoQ:
		if s.TLSHostname == "" {
			return "", fmt.Errorf("cannot encode %s server %q as text: missing TLS hostname", s.Protocol.Label(), s.DisplayName())
		}
		scheme := "tls://"
		if s.Protocol == model.ProtoDoQ {
			scheme = "quic://"
		}
		endpoint = scheme + s.TLSHostname + "@" + formatTextAddr(s.Address, s.Port, s.Protocol)
	case model.ProtoUDP:
		endpoint = formatTextAddr(s.Address, s.Port, s.Protocol)
	default:
		return "", fmt.Errorf("cannot encode %s server %q as text", string(s.Protocol), s.DisplayName())
	}
	if s.Name == "" && s.Operator == "" {
		return endpoint, nil
	}
	if s.Operator == "" {
		return endpoint + " | " + s.Name, nil
	}
	return endpoint + " | " + s.Name + " | " + s.Operator, nil
}

func formatTextAddr(addr string, port int, proto model.Protocol) string {
	if port == 0 || port == proto.DefaultPort() {
		return addr
	}
	if strings.Contains(addr, ":") {
		return "[" + addr + "]:" + strconv.Itoa(port)
	}
	return addr + ":" + strconv.Itoa(port)
}

func DecodeText(data []byte) ([]model.Server, error) {
	var servers []model.Server
	sc := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s, err := parseTextLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		servers = append(servers, s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return servers, nil
}

func parseTextLine(line string) (model.Server, error) {
	fields := strings.Split(line, "|")
	s, err := parseTextEndpoint(strings.TrimSpace(fields[0]))
	if err != nil {
		return model.Server{}, err
	}
	if len(fields) > 1 {
		s.Name = strings.TrimSpace(fields[1])
	}
	if len(fields) > 2 {
		s.Operator = strings.TrimSpace(fields[2])
	}
	s.Enabled = true
	return s, nil
}

func parseTextEndpoint(endpoint string) (model.Server, error) {
	switch {
	case strings.HasPrefix(endpoint, "https://"), strings.HasPrefix(endpoint, "h3://"):
		proto := model.ProtoDoH
		rawURL := endpoint
		if strings.HasPrefix(endpoint, "h3://") {
			proto = model.ProtoDoH3
			rawURL = "https://" + strings.TrimPrefix(endpoint, "h3://")
		}
		rawURL, addr, port, err := splitBootstrap(rawURL)
		if err != nil {
			return model.Server{}, err
		}
		u, err := url.Parse(rawURL)
		if err != nil || u.Host == "" {
			return model.Server{}, fmt.Errorf("invalid %s URL %q", proto.Label(), endpoint)
		}
		return model.Server{Protocol: proto, DoHURL: rawURL, Address: addr, Port: port}, nil
	case strings.HasPrefix(endpoint, "tls://"), strings.HasPrefix(endpoint, "quic://"):
		proto, scheme := model.ProtoDoT, "tls://"
		if strings.HasPrefix(endpoint, "quic://") {
			proto, scheme = model.ProtoDoQ, "quic://"
		}
		rest := strings.TrimPrefix(endpoint, scheme)
		hostname, addrPart, found := strings.Cut(rest, "@")
		if !found || hostname == "" || addrPart == "" {
			return model.Server{}, fmt.Errorf("invalid %s entry %q: expected %shostname@address", proto.Label(), endpoint, scheme)
		}
		addr, port, err := parseTextAddr(addrPart)
		if err != nil {
			return model.Server{}, err
		}
		return model.Server{Protocol: proto, Address: addr, Port: port, TLSHostname: hostname}, nil
	default:
		addr, port, err := parseTextAddr(endpoint)
		if err != nil {
			return model.Server{}, err
		}
		return model.Server{Protocol: model.ProtoUDP, Address: addr, Port: port}, nil
	}
}

// splitBootstrap preserves URL userinfo unless the final @ suffix is an IP address.
func splitBootstrap(rawURL string) (string, string, int, error) {
	idx := strings.LastIndex(rawURL, "@")
	if idx < 0 {
		return rawURL, "", 0, nil
	}
	head, tail := rawURL[:idx], rawURL[idx+1:]
	addr, port, err := parseTextAddr(tail)
	if err != nil {
		return rawURL, "", 0, nil
	}
	if head == "" {
		return "", "", 0, fmt.Errorf("invalid entry %q: missing URL before the bootstrap address", rawURL)
	}
	return head, addr, port, nil
}

func parseTextAddr(s string) (string, int, error) {
	if strings.HasPrefix(s, "[") {
		closing := strings.Index(s, "]")
		if closing < 0 {
			return "", 0, fmt.Errorf("missing closing bracket in %q", s)
		}
		addr := s[1:closing]
		if _, err := netip.ParseAddr(addr); err != nil {
			return "", 0, fmt.Errorf("invalid IP address %q", addr)
		}
		rest := s[closing+1:]
		if rest == "" {
			return addr, 0, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("unexpected text after bracket in %q", s)
		}
		port, err := parseTextPort(rest[1:])
		if err != nil {
			return "", 0, err
		}
		return addr, port, nil
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return s, 0, nil
	}
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("invalid IP address %q", s)
	}
	addr := s[:idx]
	if _, err := netip.ParseAddr(addr); err != nil {
		return "", 0, fmt.Errorf("invalid IP address %q", addr)
	}
	port, err := parseTextPort(s[idx+1:])
	if err != nil {
		return "", 0, err
	}
	return addr, port, nil
}

func parseTextPort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return port, nil
}
