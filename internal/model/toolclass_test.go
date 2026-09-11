package model

import "testing"

func TestIsReadOnlyTool(t *testing.T) {
	cases := map[string]bool{
		"Read": true, "Grep": true, "Glob": true, "WebSearch": true,
		"Edit": false, "Write": false, "apply_patch": false,
		"Bash":                      false, // unclassified shell: not safe to call read-only
		"Bash(read)":                true,
		"Bash(write)":               false,
		"exec_command(read)":        true,
		"exec_command":              false,
		"mcp__server__read_file":    true,
		"mcp__server__list_things":  true,
		"mcp__server__delete_thing": false,
		"Agent":                     false,
		"":                          false,
		"SomethingNobodyHasHeardOf": false,
	}
	for name, want := range cases {
		if got := IsReadOnlyTool(name); got != want {
			t.Errorf("IsReadOnlyTool(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestAllReadOnly(t *testing.T) {
	if !AllReadOnly(map[string]int{}) {
		t.Error("a run with no tools is read-only")
	}
	if !AllReadOnly(map[string]int{"Read": 3, "Bash(read)": 1}) {
		t.Error("reads plus a classified read-only shell call is read-only")
	}
	if AllReadOnly(map[string]int{"Read": 3, "Bash": 1}) {
		t.Error("an unclassified shell call must break read-only")
	}
	if AllReadOnly(map[string]int{"Read": 3, "Edit": 1}) {
		t.Error("an edit must break read-only")
	}
	if !AllReadOnly(map[string]int{"Edit": 0}) {
		t.Error("a zero count is not a use")
	}
}
