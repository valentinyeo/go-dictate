package main

import (
	"bytes"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrip(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
		count  int
	}{
		{"raw string", "package p\nvar s = `// literal /* opener`\n", "package p\nvar s = `// literal /* opener`\n", 0},
		{"struct tag", "package p\ntype T struct { X string `json:\"x//y/*z\"` }\n", "package p\ntype T struct { X string `json:\"x//y/*z\"` }\n", 0},
		{"build directive", "//go:build windows\n\npackage p\n", "//go:build windows\n\npackage p\n", 0},
		{"directives", "package p\n//go:generate echo test\n//go:embed file\n//go:linkname local remote\n//export Func\n//line source.go:20\nvar x int\n", "package p\n//go:generate echo test\n//go:embed file\n//go:linkname local remote\n//export Func\n//line source.go:20\nvar x int\n", 0},
		{"trailing", "package p\nvar x = 1  \t// remove\n", "package p\nvar x = 1\n", 1},
		{"whole line", "package p\n\t// remove\nvar x = 1\n", "package p\nvar x = 1\n", 1},
		{"block", "package p\n/* remove\nthis too */\nvar x = 1\n", "package p\nvar x = 1\n", 1},
		{"inline block", "package p\nvar x = 1/* remove */+2\n", "package p\nvar x = 1 +2\n", 1},
		{"separation", "package p\nvar/* remove */x int\n", "package p\nvar x int\n", 1},
		{"CRLF", "package p\r\n// remove\r\nvar x = 1\t// remove\r\n", "package p\r\nvar x = 1\r\n", 2},
		{"no final newline", "package p\nvar x = 1 // remove", "package p\nvar x = 1", 1},
		{"interpreted and rune", "package p\nvar s = \"\\\"// /*\"\nvar r = '\\'' // remove\n", "package p\nvar s = \"\\\"// /*\"\nvar r = '\\''\n", 1},
		{"cgo", "package p\n/*\n#include <stdlib.h>\n*/\nimport \"C\"\n// remove\n", "package p\n/*\n#include <stdlib.h>\n*/\nimport \"C\"\n", 1},
		{"line cgo", "package p\n// #include <stdlib.h>\n// #include <stdio.h>\nimport \"C\"\n", "package p\n// #include <stdlib.h>\n// #include <stdio.h>\nimport \"C\"\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comments, err := commentsIn("input.go", []byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if len(comments) != tc.count {
				t.Fatalf("got %d comments, want %d", len(comments), tc.count)
			}
			got := strip([]byte(tc.source), comments)
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if _, err := format.Source(got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{{"init"}, {"add", "planted.go"}} {
		if args[0] == "add" {
			if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte("package p\n// planted\nvar x = 1 // trailing\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, output)
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"--check"}, &out, &errOut); code != 1 {
		t.Fatalf("check exit = %d: %s", code, &errOut)
	}
	for _, want := range []string{"planted.go:2: // planted", "planted.go:3: // trailing", "2 code comment(s) found.\n"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
	if code := run(nil, &out, &errOut); code != 0 {
		t.Fatalf("strip exit = %d: %s", code, &errOut)
	}
	cleaned, err := os.ReadFile("planted.go")
	if err != nil || string(cleaned) != "package p\n\nvar x = 1\n" {
		t.Fatalf("stripped file = %q, err = %v", cleaned, err)
	}
	out.Reset()
	if code := run([]string{"--check"}, &out, &errOut); code != 0 || out.String() != "No code comments found\n" {
		t.Fatalf("clean check exit = %d: %s %s", code, &out, &errOut)
	}
}

func TestInvalidSource(t *testing.T) {
	if _, err := commentsIn("broken.go", []byte("package p\n/* unfinished")); err == nil {
		t.Fatal("unterminated comment accepted")
	}
}
