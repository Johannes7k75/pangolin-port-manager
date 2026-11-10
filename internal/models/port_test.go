package models

import (
	"testing"
)

func TestPortRule_Validate(t *testing.T) {
	tests := []struct {
		name    string
		rule    PortRule
		wantErr bool
	}{
		{
			name: "valid tcp rule",
			rule: PortRule{
				Name:     "Minecraft Server",
				Port:     25565,
				Protocol: ProtocolTCP,
			},
			wantErr: false,
		},
		{
			name: "valid udp rule uppercase protocol",
			rule: PortRule{
				Name:     "Voice Server",
				Port:     9987,
				Protocol: "UDP",
			},
			wantErr: false,
		},
		{
			name: "empty name",
			rule: PortRule{
				Name:     "   ",
				Port:     25565,
				Protocol: ProtocolTCP,
			},
			wantErr: true,
		},
		{
			name: "port 0",
			rule: PortRule{
				Name:     "Invalid Port",
				Port:     0,
				Protocol: ProtocolTCP,
			},
			wantErr: true,
		},
		{
			name: "port 70000",
			rule: PortRule{
				Name:     "Too High Port",
				Port:     70000,
				Protocol: ProtocolTCP,
			},
			wantErr: true,
		},
		{
			name: "invalid protocol",
			rule: PortRule{
				Name:     "HTTP",
				Port:     8080,
				Protocol: "sctp",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPortRule_CheckReserved(t *testing.T) {
	rule := PortRule{Port: 443}
	reserved, desc := rule.CheckReserved()
	if !reserved || desc == "" {
		t.Errorf("Expected port 443 to be reserved")
	}

	ruleNonReserved := PortRule{Port: 25565}
	reserved, _ = ruleNonReserved.CheckReserved()
	if reserved {
		t.Errorf("Expected port 25565 not to be reserved")
	}
}

func TestGenerateID(t *testing.T) {
	id1 := GenerateID()
	id2 := GenerateID()
	if id1 == "" || id2 == "" {
		t.Fatal("GenerateID returned empty string")
	}
	if id1 == id2 {
		t.Fatal("GenerateID produced collision")
	}
	if len(id1) != 36 {
		t.Fatalf("Expected UUID length 36, got %d (%s)", len(id1), id1)
	}
}
