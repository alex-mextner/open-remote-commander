package protocol

import (
	"encoding/json"
	"testing"
)

func TestFrameValidate(t *testing.T) {
	tests := []struct {
		name    string
		frame   Frame
		wantErr bool
	}{
		{"call", Frame{Type: TypeCall, ID: "1", Tool: "ping", Args: json.RawMessage(`{}`)}, false},
		{"call missing args", Frame{Type: TypeCall, ID: "1", Tool: "ping"}, true},
		{"result ok", Frame{Type: TypeResult, ID: "1", OK: true}, false},
		{"result error missing body", Frame{Type: TypeResult, ID: "1", OK: false}, true},
		{"ping", Frame{Type: TypePing}, false},
		{"unknown", Frame{Type: "wat"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.frame.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}
