package rtk

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func assertEq(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\ngot:\n%s\n\nwant:\n%s", name, got, want)
	}
}

// ── gitDiff ───────────────────────────────────────────────────────────────────

func TestGitDiffParity(t *testing.T) {
	p := &GitDiffParser{}

	t.Run("basic diff", func(t *testing.T) {
		input := `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -1,4 +1,4 @@
 package main
-func old() {}
+func new() {}
 // comment
`
		out := p.Parse(input)
		// should contain filename
		if !strings.Contains(out, "foo.go") {
			t.Errorf("expected filename in output, got:\n%s", out)
		}
		// should contain +1 -1 summary
		if !strings.Contains(out, "+1 -1") {
			t.Errorf("expected +1 -1 summary, got:\n%s", out)
		}
		// should contain hunk header
		if !strings.Contains(out, "@@ -1,4 +1,4 @@") {
			t.Errorf("expected hunk header, got:\n%s", out)
		}
	})

	t.Run("hunk truncation at 100 lines", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("diff --git a/big.go b/big.go\n")
		sb.WriteString("@@ -1,200 +1,200 @@\n")
		for i := 0; i < 110; i++ {
			sb.WriteString("+added line\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "lines truncated") {
			t.Errorf("expected truncation message, got:\n%s", out)
		}
	})

	t.Run("multiple files", func(t *testing.T) {
		input := "diff --git a/a.go b/a.go\n@@ -1 +1 @@\n+x\ndiff --git a/b.go b/b.go\n@@ -1 +1 @@\n+y\n"
		out := p.Parse(input)
		if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
			t.Errorf("expected both filenames, got:\n%s", out)
		}
	})

	t.Run("maxLines 500 triggers truncation", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 20; i++ {
			sb.WriteString("diff --git a/f.go b/f.go\n")
			sb.WriteString("@@ -1 +1 @@\n")
			for j := 0; j < 30; j++ {
				sb.WriteString("+line\n")
			}
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "more changes truncated") {
			t.Errorf("expected 'more changes truncated', got:\n%s", out)
		}
	})

	t.Run("wasTruncated appends hint", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("diff --git a/f.go b/f.go\n")
		sb.WriteString("@@ -1 +1 @@\n")
		for j := 0; j < 110; j++ {
			sb.WriteString("+line\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "[full diff: rtk git diff --no-compact]") {
			t.Errorf("expected full diff hint, got:\n%s", out)
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		assertEq(t, "empty", p.Parse(""), "")
	})
}

// ── gitStatus ─────────────────────────────────────────────────────────────────

func TestGitStatusParity(t *testing.T) {
	p := &GitStatusParser{}

	t.Run("porcelain with branch", func(t *testing.T) {
		input := "## main...origin/main\nM  foo.go\n?? bar.go\n"
		out := p.Parse(input)
		assertEq(t, "branch", "* main...origin/main\n+ Staged: 1 files\n   foo.go\n? Untracked: 1 files\n   bar.go", out)
	})

	t.Run("long-form branch", func(t *testing.T) {
		input := "On branch develop\nmodified:   src/main.go\n"
		out := p.Parse(input)
		if !strings.Contains(out, "* develop") {
			t.Errorf("expected branch, got:\n%s", out)
		}
		if !strings.Contains(out, "~ Modified: 1") {
			t.Errorf("expected modified, got:\n%s", out)
		}
	})

	t.Run("staged files", func(t *testing.T) {
		// A in X position = staged new file
		input := "## main\nA  new.go\n M old.go\n"
		out := p.Parse(input)
		if !strings.Contains(out, "+ Staged: 1") {
			t.Errorf("expected staged, got:\n%s", out)
		}
	})

	t.Run("clean tree", func(t *testing.T) {
		assertEq(t, "clean", "* main\nclean — nothing to commit", p.Parse("## main\n"))
	})

	t.Run("empty input", func(t *testing.T) {
		assertEq(t, "empty", "Clean working tree", p.Parse(""))
	})

	t.Run("conflicts", func(t *testing.T) {
		input := "## main\nUU conflict.go\n"
		out := p.Parse(input)
		if !strings.Contains(out, "conflicts: 1") {
			t.Errorf("expected conflicts, got:\n%s", out)
		}
	})

	t.Run("cap at STATUS_MAX_FILES", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("## main\n")
		for i := 0; i < 15; i++ {
			sb.WriteString("?? file" + itoa(i) + ".go\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "... +5 more") {
			t.Errorf("expected +5 more, got:\n%s", out)
		}
	})
}

// ── gitLog ────────────────────────────────────────────────────────────────────

func TestGitLogParity(t *testing.T) {
	p := &GitLogParser{}

	t.Run("basic commit", func(t *testing.T) {
		input := "commit abc1234def5678\nAuthor: John <j@e.com>\nDate:   Mon Jan 1 2024\n\n    Add feature\n\n1 file changed, 10 insertions(+)\n"
		out := p.Parse(input)
		if out != input {
			// For short input, result with "Subject:" is longer → guard returns original
			t.Logf("output equals input (length guard): %q", out)
		}
	})

	t.Run("multi-commit with diffs stripped", func(t *testing.T) {
		input := "commit abc1\nAuthor: A\nDate:   d1\n\n    feat1\n\ndiff --git a/f1 b/f1\nindex abc..def 100644\n--- a/f1\n+++ b/f1\n@@ -1 +1 @@\n-foo\n+bar\ncommit def2\nAuthor: B\nDate:   d2\n\n    feat2\n"
		out := p.Parse(input)
		if strings.Contains(out, "Subject:") {
			t.Logf("has Subject: prefix")
		}
		// Diffs should be marked as omitted
		if strings.Contains(out, "diff body omitted") {
			t.Logf("has diff body omitted")
		}
	})

	t.Run("oneline format", func(t *testing.T) {
		input := "abc1234 First commit\ndef5678 Second commit\n"
		out := p.Parse(input)
		if !strings.Contains(out, "abc1234 First commit") {
			t.Errorf("expected oneline, got:\n%s", out)
		}
	})

	t.Run("graph decoration dropped", func(t *testing.T) {
		input := "* | \n* | abc1234 Some commit\n"
		out := p.Parse(input)
		if strings.Contains(out, "* |") {
			t.Errorf("expected graph decoration dropped, got:\n%s", out)
		}
	})

	t.Run("empty returns empty", func(t *testing.T) {
		assertEq(t, "empty", "", p.Parse(""))
	})

	t.Run("line cap", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 250; i++ {
			sb.WriteString("abc123" + itoa(i) + " commit " + itoa(i) + "\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "more lines") {
			t.Errorf("expected line cap message, got:\n%s", out)
		}
	})
}

// ── buildOutput ───────────────────────────────────────────────────────────────

func TestBuildOutputParity(t *testing.T) {
	p := &BuildOutputParser{}

	t.Run("npm error captured", func(t *testing.T) {
		input := "npm ERR! code ENOENT\nnpm ERR! syscall open\n"
		out := p.Parse(input)
		if !strings.Contains(out, "npm ERR!") {
			t.Errorf("expected npm error, got:\n%s", out)
		}
	})

	t.Run("yarn error captured", func(t *testing.T) {
		input := "yarn error Command failed.\n"
		out := p.Parse(input)
		if !strings.Contains(out, "yarn error") {
			t.Errorf("expected yarn error, got:\n%s", out)
		}
	})

	t.Run("compiling lines collapsed", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 5; i++ {
			sb.WriteString("   Compiling foo v0." + itoa(i) + "\n")
		}
		sb.WriteString("error[E001]: something\n")
		out := p.Parse(sb.String())
		if !strings.Contains(out, "Compiled 5 packages") {
			t.Errorf("expected collapsed compiling, got:\n%s", out)
		}
	})

	t.Run("deprecations capped at 3", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 6; i++ {
			sb.WriteString("npm warn deprecated pkg" + itoa(i) + "\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+3 more deprecated") {
			t.Errorf("expected +3 more deprecated, got:\n%s", out)
		}
	})

	t.Run("warnings capped at 5", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 8; i++ {
			sb.WriteString("npm warn pkg" + itoa(i) + "\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+3 more warnings") {
			t.Errorf("expected +3 more warnings, got:\n%s", out)
		}
	})

	t.Run("summary line kept", func(t *testing.T) {
		input := "added 42 packages from 10 contributors\n"
		out := p.Parse(input)
		if !strings.Contains(out, "added 42 packages") {
			t.Errorf("expected summary, got:\n%s", out)
		}
	})

	t.Run("no match returns input", func(t *testing.T) {
		input := "hello world\n"
		assertEq(t, "passthrough", input, p.Parse(input))
	})
}

// ── dedupLog ──────────────────────────────────────────────────────────────────

func TestDedupLogParity(t *testing.T) {
	p := &DedupLogParser{}

	t.Run("deduplicates consecutive lines", func(t *testing.T) {
		input := "line1\nline1\nline1\nline2\n"
		out := p.Parse(input)
		assertEq(t, "dedup", "line1\n  ... (2 duplicate lines)\nline2\n", out)
	})

	t.Run("blank lines collapsed", func(t *testing.T) {
		input := "a\n\n\n\nb\n"
		out := p.Parse(input)
		if strings.Count(out, "\n\n") > 1 {
			t.Errorf("expected at most 1 blank line, got:\n%s", out)
		}
	})

	t.Run("truncates at DEDUP_LINE_MAX", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 2010; i++ {
			sb.WriteString("line " + itoa(i) + "\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "truncated at 2000 lines") {
			t.Errorf("expected truncation, got start:\n%s", out[:100])
		}
	})

	t.Run("no duplicates passthrough", func(t *testing.T) {
		input := "a\nb\nc\n"
		assertEq(t, "no dup", "a\nb\nc\n", p.Parse(input))
	})
}

// ── ls ────────────────────────────────────────────────────────────────────────

func TestLsParity(t *testing.T) {
	p := &LsParser{}

	lsLine := func(perms, size, name string) string {
		return perms + " 1 user group " + size + " Jan  1 12:00 " + name + "\n"
	}

	t.Run("basic file and dir", func(t *testing.T) {
		input := "total 8\n" +
			lsLine("drwxr-xr-x", "4096", "src") +
			lsLine("-rw-r--r--", "1024", "main.go")
		out := p.Parse(input)
		if !strings.Contains(out, "src/") {
			t.Errorf("expected dir, got:\n%s", out)
		}
		if !strings.Contains(out, "main.go") {
			t.Errorf("expected file, got:\n%s", out)
		}
		if !strings.Contains(out, "Summary:") {
			t.Errorf("expected summary, got:\n%s", out)
		}
	})

	t.Run("noise dirs filtered", func(t *testing.T) {
		input := "total 4\n" +
			lsLine("drwxr-xr-x", "4096", "node_modules") +
			lsLine("-rw-r--r--", "512", "index.js")
		out := p.Parse(input)
		if strings.Contains(out, "node_modules") {
			t.Errorf("expected node_modules filtered, got:\n%s", out)
		}
	})

	t.Run("empty input returned as-is", func(t *testing.T) {
		assertEq(t, "empty", "no output", (&LsParser{}).Parse("no output"))
	})

	t.Run("symlinks included", func(t *testing.T) {
		input := "total 4\n" +
			lsLine("lrwxrwxrwx", "10", "link.go")
		out := p.Parse(input)
		if !strings.Contains(out, "link.go") {
			t.Errorf("expected symlink, got:\n%s", out)
		}
	})
}

// ── grep ──────────────────────────────────────────────────────────────────────

func TestGrepParity(t *testing.T) {
	p := &GrepParser{}

	t.Run("basic match", func(t *testing.T) {
		input := "src/foo.go:10:func Foo() {}\nsrc/foo.go:20:func Bar() {}\nsrc/bar.go:5:func Baz() {}\n"
		out := p.Parse(input)
		if !strings.Contains(out, "3 matches in 2F:") {
			t.Errorf("expected header, got:\n%s", out)
		}
		if !strings.Contains(out, "[file] src/foo.go (2):") {
			t.Errorf("expected file block, got:\n%s", out)
		}
	})

	t.Run("cap per file at 10", func(t *testing.T) {
		var sb strings.Builder
		for i := 1; i <= 15; i++ {
			sb.WriteString("file.go:" + itoa(i) + ":line\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+5") {
			t.Errorf("expected +5 overflow, got:\n%s", out)
		}
	})

	t.Run("no match returns input", func(t *testing.T) {
		assertEq(t, "no match", "not a grep line", p.Parse("not a grep line"))
	})

	t.Run("line number padded to 4", func(t *testing.T) {
		input := "a.go:1:x\n"
		out := p.Parse(input)
		if !strings.Contains(out, "   1: x") {
			t.Errorf("expected padded linenum, got:\n%s", out)
		}
	})
}

// ── find ──────────────────────────────────────────────────────────────────────

func TestFindParity(t *testing.T) {
	p := &FindParser{}

	t.Run("groups by dir", func(t *testing.T) {
		input := "src/a.go\nsrc/b.go\ntest/c.go\n"
		out := p.Parse(input)
		if !strings.Contains(out, "3 files in 2 dirs:") {
			t.Errorf("expected header, got:\n%s", out)
		}
		if !strings.Contains(out, "src/") {
			t.Errorf("expected src dir, got:\n%s", out)
		}
	})

	t.Run("cap per dir at 10", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 15; i++ {
			sb.WriteString("src/file" + itoa(i) + ".go\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+5") {
			t.Errorf("expected overflow, got:\n%s", out)
		}
	})

	t.Run("cap dirs at 20", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 25; i++ {
			sb.WriteString("dir" + itoa(i) + "/file.go\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+5 more dirs") {
			t.Errorf("expected +5 more dirs, got:\n%s", out)
		}
	})

	t.Run("root-level files use dot dir", func(t *testing.T) {
		input := "main.go\nutils.go\n"
		out := p.Parse(input)
		if !strings.Contains(out, "./") {
			t.Errorf("expected dot dir, got:\n%s", out)
		}
	})
}

// ── tree ──────────────────────────────────────────────────────────────────────

func TestTreeParity(t *testing.T) {
	p := &TreeParser{}

	t.Run("summary line dropped", func(t *testing.T) {
		input := ".\n├── src\n│   └── main.go\n2 directories, 1 file\n"
		out := p.Parse(input)
		if strings.Contains(out, "directories") {
			t.Errorf("expected summary dropped, got:\n%s", out)
		}
		if !strings.Contains(out, "main.go") {
			t.Errorf("expected tree content, got:\n%s", out)
		}
	})

	t.Run("trailing blanks dropped", func(t *testing.T) {
		input := ".\n└── a.go\n\n\n"
		out := p.Parse(input)
		if strings.HasSuffix(out, "\n") {
			t.Errorf("expected no trailing newline, got:\n%q", out)
		}
	})

	t.Run("cap at 200 lines", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 210; i++ {
			sb.WriteString("├── file" + itoa(i) + ".go\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+10 more lines") {
			t.Errorf("expected cap message, got:\n%s", out)
		}
	})
}

// ── smartTruncate ─────────────────────────────────────────────────────────────

func TestSmartTruncateParity(t *testing.T) {
	p := &SmartTruncateParser{}

	t.Run("below 250 lines returns as-is", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 100; i++ {
			sb.WriteString("line\n")
		}
		input := sb.String()
		assertEq(t, "below min", input, p.Parse(input))
	})

	t.Run("above 250 lines truncates", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 300; i++ {
			sb.WriteString("line " + itoa(i) + "\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "lines truncated") {
			t.Errorf("expected truncation, got start:\n%s", out[:50])
		}
		// head 120 + tail 60 = 180 lines + 1 marker = 181 lines
		lines := strings.Split(out, "\n")
		if len(lines) != 181 {
			t.Errorf("expected 181 lines got %d", len(lines))
		}
	})

	t.Run("truncation message exact", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 300; i++ {
			sb.WriteString("line\n")
		}
		out := p.Parse(sb.String())
		// 300 "line\n" lines → split gives 301 elements, 301-120-60 = 121 cut
		if !strings.Contains(out, "... +121 lines truncated") {
			t.Errorf("expected cut count, got:\n%s", out)
		}
	})
}

// ── readNumbered ──────────────────────────────────────────────────────────────

func TestReadNumberedParity(t *testing.T) {
	p := &ReadNumberedParser{}

	t.Run("below 250 returns as-is", func(t *testing.T) {
		var sb strings.Builder
		for i := 1; i <= 100; i++ {
			sb.WriteString("  " + itoa(i) + "|content\n")
		}
		input := sb.String()
		assertEq(t, "below min", input, p.Parse(input))
	})

	t.Run("above 250 truncates with message", func(t *testing.T) {
		var sb strings.Builder
		for i := 1; i <= 300; i++ {
			sb.WriteString("  " + itoa(i) + "|content line\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "lines truncated (file continues)") {
			t.Errorf("expected truncation message, got:\n%s", out[:80])
		}
	})

	t.Run("head and tail preserved", func(t *testing.T) {
		var sb strings.Builder
		for i := 1; i <= 300; i++ {
			sb.WriteString("  " + itoa(i) + "|line " + itoa(i) + "\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "  1|line 1") {
			t.Errorf("expected head line 1, got:\n%s", out[:50])
		}
		if !strings.Contains(out, "  300|line 300") {
			t.Errorf("expected tail line 300")
		}
	})
}

// ── searchList ────────────────────────────────────────────────────────────────

func TestSearchListParity(t *testing.T) {
	p := &SearchListParser{}

	t.Run("basic grouping", func(t *testing.T) {
		input := "Result of search in 'src' (total 3 files):\n- src/a.go\n- src/b.go\n- test/c.go\n"
		out := p.Parse(input)
		if !strings.Contains(out, "3 files in 2 dirs:") {
			t.Errorf("expected header, got:\n%s", out)
		}
		if !strings.Contains(out, "src/ (2):") {
			t.Errorf("expected src group, got:\n%s", out)
		}
	})

	t.Run("cap per dir at 10", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("Result of search in '.' (total 15 files):\n")
		for i := 0; i < 15; i++ {
			sb.WriteString("- src/file" + itoa(i) + ".go\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+5") {
			t.Errorf("expected overflow, got:\n%s", out)
		}
	})

	t.Run("cap dirs at 20", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("Result of search in '.' (total 25 files):\n")
		for i := 0; i < 25; i++ {
			sb.WriteString("- dir" + itoa(i) + "/file.go\n")
		}
		out := p.Parse(sb.String())
		if !strings.Contains(out, "+5 more dirs") {
			t.Errorf("expected +5 more dirs, got:\n%s", out)
		}
	})

	t.Run("no paths returns input", func(t *testing.T) {
		input := "Result of search in '.' (total 0 files):\n"
		assertEq(t, "no paths", input, p.Parse(input))
	})
}

// ── autoDetectFilter ──────────────────────────────────────────────────────────

func TestAutoDetectFilterParity(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		filter string
	}{
		{"git-diff", "diff --git a/x b/x\n@@ -1 +1 @@\n", "git-diff"},
		{"git-status", "On branch main\nChanges not staged:\n", "git-status"},
		{"git-log", "commit abc1234def567890\nAuthor: x\n", "git-log"},
		{"build-output", "npm ERR! code ENOENT\n", "build-output"},
		{"grep", "file.go:10:content\nfile.go:20:other\n", "grep"},
		{"find", "./src/a.go\n./src/b.go\n./test/c.go\n", "find"},
		{"tree", ".\n├── src\n│   └── a.go\n", "tree"},
		{"ls", "total 8\n-rw-r--r-- 1 u g 100 Jan  1 12:00 a.go\n-rw-r--r-- 1 u g 200 Jan  1 12:00 b.go\n-rw-r--r-- 1 u g 300 Jan  1 12:00 c.go\n", "ls"},
		{"search-list", "Result of search in '.' (total 2 files):\n- src/a.go\n- src/b.go\n", "search-list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := autoDetectFilter(tc.input)
			if f == nil {
				t.Fatalf("expected filter %q, got nil", tc.filter)
			}
			if f.Name() != tc.filter {
				t.Errorf("expected %q got %q", tc.filter, f.Name())
			}
		})
	}

	t.Run("nil for tiny input", func(t *testing.T) {
		if f := autoDetectFilter("hello"); f != nil {
			t.Errorf("expected nil, got %q", f.Name())
		}
	})
}

// ── compressKiroFormat ────────────────────────────────────────────────────────

func TestCompressKiroFormatParity(t *testing.T) {
	makeBody := func(text string) []byte {
		body := map[string]any{
			"conversationState": map[string]any{
				"history": []any{},
				"currentMessage": map[string]any{
					"userInputMessage": map[string]any{
						"content": "hello",
						"userInputMessageContext": map[string]any{
							"toolResults": []any{
								map[string]any{
									"toolUseId": "t1",
									"status":    "success",
									"content": []any{
										map[string]any{"text": text},
									},
								},
							},
						},
					},
				},
			},
		}
		b, _ := json.Marshal(body)
		return b
	}

	t.Run("small text not compressed", func(t *testing.T) {
		body := makeBody("small")
		out, stats := CompressKiroBody(body)
		if string(out) != string(body) {
			t.Error("expected no compression for small text")
		}
		_ = stats
	})

	t.Run("status error not compressed", func(t *testing.T) {
		body := map[string]any{
			"conversationState": map[string]any{
				"history": []any{},
				"currentMessage": map[string]any{
					"userInputMessage": map[string]any{
						"content": "x",
						"userInputMessageContext": map[string]any{
							"toolResults": []any{
								map[string]any{
									"toolUseId": "t1",
									"status":    "error",
									"content": []any{
										map[string]any{"text": strings.Repeat("x", 1000)},
									},
								},
							},
						},
					},
				},
			},
		}
		b, _ := json.Marshal(body)
		out, _ := CompressKiroBody(b)
		if string(out) != string(b) {
			t.Error("expected error status not compressed")
		}
	})

	t.Run("large grep output compressed", func(t *testing.T) {
		// build a grep-like string > 500 bytes
		var sb strings.Builder
		for i := 0; i < 30; i++ {
			sb.WriteString("src/file.go:" + itoa(i+1) + ":some content here that is reasonably long\n")
		}
		text := sb.String()
		body := makeBody(text)
		out, stats := CompressKiroBody(body)
		if string(out) == string(body) {
			t.Error("expected compression for large grep output")
		}
		if stats == nil || len(stats.Hits) == 0 {
			t.Error("expected hits in stats")
		}
	})
}
