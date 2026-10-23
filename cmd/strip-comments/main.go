package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type comment struct {
	start int
	end   int
	line  int
	text  string
}

func commentsIn(path string, source []byte) ([]comment, error) {
	files := token.NewFileSet()
	file := files.AddFile(path, -1, len(source))
	var scan scanner.Scanner
	var scanErr error
	scan.Init(file, source, func(pos token.Position, message string) {
		if scanErr == nil {
			scanErr = fmt.Errorf("%s: %s", pos, message)
		}
	}, scanner.ScanComments)
	var comments []comment
	for {
		pos, kind, _ := scan.Scan()
		if kind == token.EOF {
			break
		}
		if kind != token.COMMENT {
			continue
		}
		start := file.Offset(pos)
		end := start
		if bytes.HasPrefix(source[start:], []byte("//")) {
			end = bytes.IndexByte(source[start:], '\n')
			if end < 0 {
				end = len(source)
			} else {
				end += start
			}
		} else {
			close := bytes.Index(source[start+2:], []byte("*/"))
			if close < 0 {
				return nil, fmt.Errorf("%s: unterminated comment", path)
			}
			end = start + 2 + close + 2
		}
		text := string(source[start:end])
		if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "//export ") || strings.HasPrefix(text, "//line ") {
			continue
		}
		comments = append(comments, comment{start, end, file.PositionFor(pos, false).Line, text})
	}
	if scanErr != nil {
		return nil, scanErr
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return nil, err
	}
	for _, decl := range parsed.Decls {
		imports, ok := decl.(*ast.GenDecl)
		if !ok || imports.Tok != token.IMPORT {
			continue
		}
		for _, spec := range imports.Specs {
			imp := spec.(*ast.ImportSpec)
			if imp.Path.Value != `"C"` {
				continue
			}
			preamble := imp.Doc
			if preamble == nil && len(imports.Specs) == 1 {
				preamble = imports.Doc
			}
			if preamble == nil {
				continue
			}
			kept := comments[:0]
			for _, c := range comments {
				if c.start < int(preamble.Pos())-1 || c.start >= int(preamble.End())-1 {
					kept = append(kept, c)
				}
			}
			comments = kept
		}
	}
	return comments, nil
}

func strip(source []byte, comments []comment) []byte {
	removed := make([]bool, len(source))
	separators := make(map[int]bool)
	for _, c := range comments {
		for i := c.start; i < c.end; i++ {
			removed[i] = true
		}
		if c.start > 0 && c.end < len(source) && !bytes.ContainsAny(source[c.start:c.end], "\r\n") {
			separators[c.start] = true
		}
	}
	var result []byte
	for start := 0; start < len(source); {
		end := bytes.IndexByte(source[start:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += start + 1
		}
		var line []byte
		changed := false
		for i := start; i < end; i++ {
			if removed[i] {
				changed = true
				if source[i] == '\n' || source[i] == '\r' {
					line = append(line, source[i])
				} else if separators[i] {
					line = append(line, ' ')
				}
			} else {
				line = append(line, source[i])
			}
		}
		if changed {
			body := bytes.TrimRight(line, " \t\r\n")
			if len(bytes.TrimSpace(body)) == 0 {
				start = end
				continue
			}
			eol := line[len(bytes.TrimRight(line, "\r\n")):]
			line = append(body, eol...)
		}
		result = append(result, line...)
		start = end
	}
	return result
}

func run(args []string, out, errOut io.Writer) int {
	check := false
	if len(args) == 1 && args[0] == "--check" {
		check = true
	} else if len(args) != 0 {
		fmt.Fprintln(errOut, "usage: strip-comments [--check]")
		return 2
	}
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	command := exec.Command("git", "ls-files", "-z", "--", "*.go")
	command.Dir = strings.TrimSpace(string(root))
	tracked, err := command.Output()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	count := 0
	for _, path := range bytes.Split(tracked, []byte{0}) {
		if len(path) == 0 {
			continue
		}
		name := string(path)
		full := filepath.Join(command.Dir, name)
		source, err := os.ReadFile(full)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		comments, err := commentsIn(name, source)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		count += len(comments)
		if check {
			for _, c := range comments {
				fmt.Fprintf(out, "%s:%d: %s\n", name, c.line, c.text)
			}
			continue
		}
		if len(comments) == 0 {
			continue
		}
		cleaned, err := format.Source(strip(source, comments))
		if err != nil {
			fmt.Fprintf(errOut, "%s: %v\n", name, err)
			return 2
		}
		info, err := os.Stat(full)
		if err == nil {
			err = os.WriteFile(full, cleaned, info.Mode().Perm())
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	if check && count > 0 {
		fmt.Fprintf(out, "%d code comment(s) found.\n", count)
		return 1
	}
	if check {
		fmt.Fprintln(out, "No code comments found")
	} else {
		fmt.Fprintf(out, "Removed %d code comment(s).\n", count)
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
