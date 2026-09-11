package model

import "strings"

// readOnlyTools only ever look at a project. A run that used nothing else
// changed nothing, so it can move to a cheaper model without risking a bad
// edit.
var readOnlyTools = map[string]bool{
	"read": true, "grep": true, "glob": true, "ls": true,
	"webfetch": true, "websearch": true, "notebookread": true,
	"todoread": true, "toolsearch": true, "skill": true,
}

// mutatingTools change a project. A bare shell tool with no class belongs
// here too: without a classified command line there is no way to tell a
// `git status` from an `rm -rf`, and the safe reading is that it changed
// something.
var mutatingTools = map[string]bool{
	"edit": true, "write": true, "multiedit": true, "notebookedit": true,
	"bash": true, "apply_patch": true, "exec_command": true,
	"local_shell": true, "shell": true, "task": true, "agent": true,
}

// mcpReadVerbs mark an mcp__ tool as read-only by name.
var mcpReadVerbs = []string{"read", "get", "list", "search", "find", "fetch"}

// IsReadOnlyTool reports whether one tool name, as the store records it,
// only looked at the project. Shell tools carry the class the parser worked
// out from the command line, as a "(read)" or "(write)" suffix; a shell tool
// with no suffix was not classifiable and counts as mutating.
//
// This is the one definition. Both the ledger and the findings use it, so
// "read-only" cannot come to mean two different things in one report.
func IsReadOnlyTool(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "":
		return false
	case strings.HasSuffix(n, "("+ClassRead+")"):
		return true
	case strings.HasSuffix(n, "("+ClassWrite+")"):
		return false
	case mutatingTools[n]:
		return false
	case readOnlyTools[n]:
		return true
	case strings.HasPrefix(n, "mcp__"):
		for _, verb := range mcpReadVerbs {
			if strings.Contains(n, verb) {
				return true
			}
		}
		return false
	}
	return false
}

// AllReadOnly reports whether every tool used in a run only looked at the
// project. A run that called no tools at all counts as read-only.
func AllReadOnly(counts map[string]int) bool {
	for name, n := range counts {
		if n > 0 && !IsReadOnlyTool(name) {
			return false
		}
	}
	return true
}
