package model

import (
	"regexp"
	"strings"
)

// Tool-call classes. A parser sets ToolCall.Class for shell-style tools by
// looking at the command's leading words. The command text itself is never
// stored; only the class survives.
const (
	ClassRead  = "read"  // the command only inspects things
	ClassWrite = "write" // the command changes files, repositories or packages
	// An empty class means unknown: the findings treat it as not read-only.
)

var (
	reSplitCommands = regexp.MustCompile(`\s*(?:&&|\|\||;|\||\n)\s*`)
	reSafeRedirects = regexp.MustCompile(`\d?>&\d|&?>+\s*/dev/null|2>\s*\S+`)
	reEnvAssign     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

var readCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "less": true, "more": true,
	"grep": true, "rg": true, "egrep": true, "fgrep": true, "ug": true, "ugrep": true, "find": true, "fd": true,
	"wc": true, "echo": true, "printf": true, "pwd": true, "which": true, "whereis": true, "type": true,
	"stat": true, "file": true, "du": true, "df": true, "ps": true, "env": true, "printenv": true,
	"date": true, "uname": true, "hostname": true, "id": true, "whoami": true, "tree": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "awk": true, "jq": true, "yq": true,
	"diff": true, "cmp": true, "md5": true, "md5sum": true, "shasum": true, "sha256sum": true,
	"basename": true, "dirname": true, "realpath": true, "readlink": true, "true": true, "false": true,
	"test": true, "[": true, "column": true, "nl": true, "od": true, "xxd": true, "strings": true,
	"pytest": true, "gofmt": true, "tsc": true, "eslint": true, "ruff": true, "mypy": true,
	"sleep": true, "lsof": true, "netstat": true, "nproc": true, "sysctl": true, "man": true,
}

var writeCommands = map[string]bool{
	"rm": true, "mv": true, "cp": true, "mkdir": true, "rmdir": true, "touch": true, "chmod": true,
	"chown": true, "ln": true, "tee": true, "dd": true, "truncate": true, "install": true, "rsync": true,
	"pip": true, "pip3": true, "brew": true, "apt": true, "apt-get": true, "yum": true, "patch": true,
	"unzip": true, "tar": true, "wget": true,
}

// Second-word rules for tools whose safety depends on the subcommand.
var subcommandClass = map[string]map[string]string{
	"git": {
		"status": ClassRead, "log": ClassRead, "diff": ClassRead, "show": ClassRead, "branch": ClassRead,
		"remote": ClassRead, "rev-parse": ClassRead, "ls-files": ClassRead, "blame": ClassRead,
		"describe": ClassRead, "config": ClassRead, "fetch": ClassRead, "shortlog": ClassRead, "grep": ClassRead,
		"add": ClassWrite, "commit": ClassWrite, "push": ClassWrite, "pull": ClassWrite, "merge": ClassWrite,
		"rebase": ClassWrite, "checkout": ClassWrite, "switch": ClassWrite, "reset": ClassWrite,
		"revert": ClassWrite, "cherry-pick": ClassWrite, "stash": ClassWrite, "init": ClassWrite,
		"clone": ClassWrite, "rm": ClassWrite, "mv": ClassWrite, "tag": ClassWrite, "worktree": ClassWrite,
		"restore": ClassWrite, "clean": ClassWrite, "am": ClassWrite, "apply": ClassWrite,
	},
	"go": {
		"test": ClassRead, "vet": ClassRead, "build": ClassRead, "list": ClassRead, "version": ClassRead,
		"env": ClassRead, "doc": ClassRead, "run": ClassRead,
		"mod": ClassWrite, "get": ClassWrite, "install": ClassWrite, "generate": ClassWrite, "fmt": ClassWrite, "work": ClassWrite,
	},
	"npm": {
		"test": ClassRead, "ls": ClassRead, "view": ClassRead, "outdated": ClassRead, "audit": ClassRead, "why": ClassRead,
		"install": ClassWrite, "i": ClassWrite, "ci": ClassWrite, "add": ClassWrite, "uninstall": ClassWrite,
		"remove": ClassWrite, "publish": ClassWrite, "update": ClassWrite, "link": ClassWrite, "init": ClassWrite,
	},
	"cargo": {
		"test": ClassRead, "build": ClassRead, "check": ClassRead, "clippy": ClassRead, "doc": ClassRead, "tree": ClassRead, "metadata": ClassRead,
		"fmt": ClassWrite, "add": ClassWrite, "remove": ClassWrite, "publish": ClassWrite, "new": ClassWrite, "init": ClassWrite, "install": ClassWrite,
	},
	"dotnet": {"test": ClassRead, "build": ClassRead, "list": ClassRead, "add": ClassWrite, "remove": ClassWrite, "new": ClassWrite, "restore": ClassWrite, "publish": ClassWrite, "format": ClassWrite},
	"gh":     {"api": ClassRead, "pr": ClassRead, "issue": ClassRead, "repo": ClassRead, "run": ClassRead, "auth": ClassRead, "release": ClassRead, "search": ClassRead},
}

// gh sub-subcommands that write, checked when the gh rule above says read.
var ghWriteVerbs = map[string]bool{"create": true, "merge": true, "close": true, "edit": true, "delete": true, "comment": true, "review": true, "fork": true, "clone": true, "sync": true, "checkout": true, "login": true, "logout": true, "upload": true, "download": true}

// ClassifyCommand decides whether a shell command line only reads. It is a
// heuristic over the first word of every pipeline segment: all segments
// recognised as read-only gives ClassRead; any segment recognised as a
// write, or any file redirection, gives ClassWrite; anything unrecognised
// gives "" (unknown), which callers must treat as not read-only.
func ClassifyCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if reSafeRedirects.ReplaceAllString(cmd, "") != cmd {
		cmd = reSafeRedirects.ReplaceAllString(cmd, "")
	}
	if strings.Contains(cmd, ">") {
		return ClassWrite
	}
	class := ClassRead
	for _, seg := range reSplitCommands.Split(cmd, -1) {
		words := strings.Fields(seg)
		// Skip prefixes that do not decide anything.
		for len(words) > 0 && (reEnvAssign.MatchString(words[0]) || words[0] == "sudo" || words[0] == "time" || words[0] == "env" || words[0] == "xargs" || words[0] == "nohup" || words[0] == "command" || words[0] == "builtin") {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		first := strings.ToLower(words[0])
		if i := strings.LastIndex(first, "/"); i >= 0 {
			first = first[i+1:]
		}
		switch {
		case first == "cd" || first == "export" || first == "set" || first == "unset" || first == "source" || first == ".":
			continue // navigation and shell state, not project changes
		case first == "sed":
			if hasFlag(words[1:], "-i") {
				return ClassWrite
			}
			continue
		case first == "gofmt" && hasFlag(words[1:], "-w"):
			return ClassWrite
		case writeCommands[first]:
			return ClassWrite
		case readCommands[first]:
			continue
		}
		if sub, ok := subcommandClass[first]; ok && len(words) > 1 {
			c := sub[strings.ToLower(words[1])]
			if first == "gh" && c == ClassRead && len(words) > 2 && ghWriteVerbs[strings.ToLower(words[2])] {
				c = ClassWrite
			}
			if first == "gh" && c == ClassRead && words[1] == "api" && hasFlag(words[2:], "-X") && !hasFlag(words[2:], "GET") {
				c = ClassWrite
			}
			switch c {
			case ClassWrite:
				return ClassWrite
			case ClassRead:
				continue
			}
		}
		class = "" // unknown segment: cannot call the whole line read-only
	}
	return class
}

func hasFlag(words []string, flag string) bool {
	for _, w := range words {
		if w == flag || (len(flag) == 2 && strings.HasPrefix(w, flag) && !strings.HasPrefix(w, "--")) {
			return true
		}
	}
	return false
}
