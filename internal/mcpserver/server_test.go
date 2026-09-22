package mcpserver

import (
	"sort"
	"testing"

	"github.com/alex-mextner/open-remote-commander/internal/executor"
)

func TestToolSurfaceMatchesExecutor(t *testing.T) {
	want := executor.SupportedTools()
	got := make([]string, 0, len(remoteToolSet))
	for name := range remoteToolSet {
		got = append(got, name)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("remote MCP tools=%v executor tools=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remote MCP tools=%v executor tools=%v", got, want)
		}
	}

	defs := make(map[string]bool, len(toolDefinitions()))
	for _, def := range toolDefinitions() {
		name, ok := def["name"].(string)
		if !ok || name == "" {
			t.Fatalf("invalid tool definition name: %#v", def["name"])
		}
		defs[name] = true
	}
	if !defs["list_devices"] {
		t.Fatal("local list_devices tool is missing")
	}
	for _, name := range want {
		if !defs[name] {
			t.Errorf("toolDefinitions missing executor tool %q", name)
		}
	}
	if len(defs) != len(want)+1 {
		t.Errorf("toolDefinitions count=%d, want %d", len(defs), len(want)+1)
	}
}

func TestRemoteToolAllowlistRejectsNonTools(t *testing.T) {
	for _, name := range []string{"", "list_devices", "Ping", "ping ", "delete_file"} {
		if knownRemoteTool(name) {
			t.Errorf("knownRemoteTool(%q)=true, want false", name)
		}
	}
}

func TestEveryToolHasExplicitSafetyClassification(t *testing.T) {
	for _, spec := range toolSpecs {
		if spec.safety == safetyUnspecified {
			t.Errorf("%s has unspecified safety", spec.name)
		}
	}
}
func TestCriticalToolSafety(t *testing.T) {
	byName := make(map[string]toolSafety, len(toolSpecs))
	for _, spec := range toolSpecs {
		byName[spec.name] = spec.safety
	}
	for _, name := range []string{"write_file", "edit_block", "move_file", "start_process", "interact_with_process", "force_terminate", "kill_process"} {
		if byName[name] != safetyDestructive {
			t.Errorf("%s safety=%v, want destructive", name, byName[name])
		}
	}
	for _, name := range []string{"list_devices", "ping", "read_file", "read_multiple_files", "list_processes", "get_config"} {
		if byName[name] != safetyReadOnly {
			t.Errorf("%s safety=%v, want read-only", name, byName[name])
		}
	}
	if byName["create_directory"] != safetyMutating {
		t.Errorf("create_directory safety=%v, want mutating", byName["create_directory"])
	}
}
