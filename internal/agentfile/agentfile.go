// Package agentfile reads Claude Code sub-agent definition files so that
// advice can be checked against what a project already says.
//
// Without this, a finding can tell someone to set a model that their agent
// file already sets, which is worse than saying nothing: it looks like the
// tool did not read the project. With it, the advice can name the real
// situation, which is usually that the call site is overriding the file.
//
// Only the YAML frontmatter is read. The body of an agent file is its system
// prompt, which is prompt text, so it is never read and never stored.
package agentfile

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Agent is the frontmatter of one sub-agent definition file.
type Agent struct {
	Name  string
	Model string // as written: an alias, a full id, "inherit", or ""
	Tools string
	Path  string // absolute path to the file
	Scope Scope
}

// Scope says where a definition came from. A project file shadows a user one
// of the same name.
type Scope string

const (
	Project Scope = "project" // <project>/.claude/agents
	User    Scope = "user"    // ~/.claude/agents
)

// SetsModel reports whether the file pins a model. "inherit" is not a pin: it
// asks for whatever the caller is using.
func (a Agent) SetsModel() bool {
	m := strings.ToLower(strings.TrimSpace(a.Model))
	return m != "" && m != "inherit"
}

// Set is every agent definition found, keyed by lower-case name. A nil Set is
// usable and simply knows nothing.
type Set map[string]Agent

// Load reads user-level definitions from ~/.claude/agents and project-level
// definitions from <root>/.claude/agents for each root given. Project files
// win. Unreadable files and directories are skipped: knowing nothing about an
// agent is always better than failing a report over it.
func Load(projectRoots ...string) Set {
	set := Set{}
	if home, err := os.UserHomeDir(); err == nil {
		set.loadDir(filepath.Join(home, ".claude", "agents"), User)
	}
	for _, root := range projectRoots {
		if root == "" {
			continue
		}
		set.loadDir(filepath.Join(root, ".claude", "agents"), Project)
	}
	return set
}

func (s Set) loadDir(dir string, scope Scope) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		a, ok := parse(filepath.Join(dir, e.Name()))
		if !ok {
			continue
		}
		a.Scope = scope
		key := strings.ToLower(a.Name)
		// A project definition shadows a user one; among equals, first wins.
		if existing, seen := s[key]; seen && existing.Scope == Project && scope != Project {
			continue
		}
		s[key] = a
	}
}

// Lookup finds a definition by sub-agent type name, case-insensitively.
func (s Set) Lookup(name string) (Agent, bool) {
	a, ok := s[strings.ToLower(strings.TrimSpace(name))]
	return a, ok
}

// parse reads the frontmatter block of an agent file. The name defaults to the
// file's base name, which is what Claude Code falls back to.
func parse(path string) (Agent, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Agent{}, false
	}
	defer f.Close()

	a := Agent{
		Name: strings.TrimSuffix(filepath.Base(path), ".md"),
		Path: path,
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)

	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return Agent{}, false // no frontmatter: not an agent definition
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			if value != "" {
				a.Name = value
			}
		case "model":
			a.Model = value
		case "tools":
			a.Tools = value
		}
	}
	return a, true
}
