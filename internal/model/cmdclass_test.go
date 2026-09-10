package model

import "testing"

func TestClassifyCommand(t *testing.T) {
	cases := map[string]string{
		"ls -la":                                 ClassRead,
		"git status && git log --oneline | head": ClassRead,
		"cd /tmp && go test ./...":               ClassRead,
		"grep -rn foo . 2>/dev/null":             ClassRead,
		"go test ./... 2>&1 | tail -20":          ClassRead,
		"gh pr view 12 --json title":             ClassRead,
		"gh api repos/x/y/readme":                ClassRead,
		"sed -n '1,20p' main.go":                 ClassRead,
		"FOO=1 pytest -q":                        ClassRead,
		"rm -rf dist":                            ClassWrite,
		"git commit -m x":                        ClassWrite,
		"echo hi > out.txt":                      ClassWrite,
		"sed -i '' 's/a/b/' f.go":                ClassWrite,
		"gofmt -w .":                             ClassWrite,
		"go mod tidy":                            ClassWrite,
		"npm install":                            ClassWrite,
		"gh repo create magna-nz/x --private":    ClassWrite,
		"gh api -X POST repos/x/y/issues":        ClassWrite,
		"ls && rm x":                             ClassWrite,
		"python3 script.py":                      "",
		"make":                                   "",
		"ls | python3 -c 'print(1)'":             "",
		"":                                       "",
	}
	for cmd, want := range cases {
		if got := ClassifyCommand(cmd); got != want {
			t.Errorf("ClassifyCommand(%q) = %q, want %q", cmd, got, want)
		}
	}
}
