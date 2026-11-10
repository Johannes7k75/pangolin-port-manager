package models

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PortProtocol string

const (
	ProtocolTCP PortProtocol = "tcp"
	ProtocolUDP PortProtocol = "udp"
)

// Standard Pangolin & Traefik reserved ports that shouldn't be overridden accidentally
var reservedPorts = map[int]string{
	80:    "HTTP (Traefik/Gerbil)",
	443:   "HTTPS (Traefik/Gerbil)",
	51820: "WireGuard (Gerbil)",
	21820: "Relay (Gerbil)",
	3001:  "Pangolin Dashboard/API",
	3003:  "Gerbil API",
}

type PortRule struct {
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Port               int          `json:"port"`
	Protocol           PortProtocol `json:"protocol"`
	Enabled            bool         `json:"enabled"`
	PangolinResourceID string       `json:"pangolin_resource_id,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

func (p *PortRule) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return errors.New("name cannot be empty")
	}

	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("port %d is out of valid range (1 - 65535)", p.Port)
	}

	p.Protocol = PortProtocol(strings.ToLower(strings.TrimSpace(string(p.Protocol))))
	if p.Protocol != ProtocolTCP && p.Protocol != ProtocolUDP {
		return fmt.Errorf("unsupported protocol %q: must be 'tcp' or 'udp'", p.Protocol)
	}

	return nil
}

// CheckReserved checks if the port is a standard Pangolin core port
func (p *PortRule) CheckReserved() (bool, string) {
	desc, ok := reservedPorts[p.Port]
	return ok, desc
}

// GenerateID produces a standard UUID v4 string using crypto/rand
func GenerateID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
