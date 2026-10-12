package rtk

import (
	"strings"
	"testing"
)

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 7: "7", 42: "42", 12345: "12345", 1000000: "1000000"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestIsGrepLine(t *testing.T) {
	cases := map[string]bool{
		"main.go:12:func main()": true,
		"a.go:1:x":               true,
		"main.go:foo":            false,
		"no colon here":          false,
		"a:b:c:1":                false,
	}
	for in, want := range cases {
		if got := isGrepLine(in); got != want {
			t.Errorf("isGrepLine(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsPathLike(t *testing.T) {
	cases := map[string]bool{
		"/usr/local/bin": true,
		"./internal/rtk": true,
		"src/main.go":    true,
		"C:\\Users\\x":   true,
		"plain":          false,
		"":               false,
		"key:value":      false,
	}
	for in, want := range cases {
		if got := isPathLike(in); got != want {
			t.Errorf("isPathLike(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCountMatches(t *testing.T) {
	if got := countMatches("a.go:1:x\na.go:2:y\nb.go:3:z", reLsRow); got != 0 {
		t.Errorf("countMatches(reLsRow) = %d, want 0", got)
	}
	if got := countMatches("diff --git a/x\n@@ -1 +1 @@\n", reGitDiffHunk); got != 1 {
		t.Errorf("countMatches(reGitDiffHunk) = %d, want 1", got)
	}
}

func TestAutoDetectFilter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // Parser.Name(), "" means nil
	}{
		{"git diff", "diff --git a/main.go b/main.go\n@@ -1 +1 @@\n-x\n+y", "git-diff"},
		{"git status", "On branch main\nnothing to commit", "git-status"},
		{"ls", "total 4\n-rw-r--r-- 1 u u 10 a.go\n-rw-r--r-- 1 u u 10 b.go", "ls"},
		{"grep", "main.go:12:foo\nmain.go:13:bar", "grep"},
		{"tree", ".\n├── a.go\n└── b.go", "tree"},
		{"plain text", "just some text\nsecond line", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := autoDetectFilter(c.in)
			got := ""
			if p != nil {
				got = p.Name()
			}
			if got != c.want {
				t.Errorf("autoDetectFilter(%q) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestSmartTruncateParse(t *testing.T) {
	short := strings.Repeat("x\n", 10)
	if got := (&SmartTruncateParser{}).Parse(short); got != short {
		t.Error("short input should pass through unchanged")
	}

	lines := make([]string, 400)
	for i := range lines {
		lines[i] = "line"
	}
	out := (&SmartTruncateParser{}).Parse(strings.Join(lines, "\n"))
	if !strings.Contains(out, "... +220 lines truncated") {
		t.Errorf("missing truncation marker in output: %q", firstLine(out))
	}
	if got := len(strings.Split(out, "\n")); got != SMART_HEAD+1+SMART_TAIL {
		t.Errorf("truncated line count = %d, want %d", got, SMART_HEAD+1+SMART_TAIL)
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func TestInjectSystemPrompt(t *testing.T) {
	t.Run("kiro body", func(t *testing.T) {
		body := map[string]any{"conversationState": map[string]any{}, "systemPrompt": "base"}
		InjectSystemPrompt(body, "P")
		if body["systemPrompt"] != "base\n\nP" {
			t.Errorf("systemPrompt = %q, want %q", body["systemPrompt"], "base\n\nP")
		}
	})

	t.Run("openai append existing system", func(t *testing.T) {
		body := map[string]any{"messages": []any{
			map[string]any{"role": "system", "content": "sys"},
			map[string]any{"role": "user", "content": "hi"},
		}}
		InjectSystemPrompt(body, "P")
		msgs := body["messages"].([]any)
		if got := msgs[0].(map[string]any)["content"]; got != "sys\n\nP" {
			t.Errorf("system content = %q, want %q", got, "sys\n\nP")
		}
		if len(msgs) != 2 {
			t.Errorf("messages = %d, want 2", len(msgs))
		}
	})

	t.Run("openai prepend when missing", func(t *testing.T) {
		body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
		InjectSystemPrompt(body, "P")
		msgs := body["messages"].([]any)
		if len(msgs) != 2 {
			t.Fatalf("messages = %d, want 2", len(msgs))
		}
		first := msgs[0].(map[string]any)
		if first["role"] != "system" || first["content"] != "P" {
			t.Errorf("prepended = %+v, want system/P", first)
		}
	})

	t.Run("instructions field", func(t *testing.T) {
		body := map[string]any{"instructions": "base"}
		InjectSystemPrompt(body, "P")
		if body["instructions"] != "base\n\nP" {
			t.Errorf("instructions = %q, want %q", body["instructions"], "base\n\nP")
		}
	})

	t.Run("no-op on nil or empty prompt", func(t *testing.T) {
		body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
		InjectSystemPrompt(body, "")
		if len(body["messages"].([]any)) != 1 {
			t.Error("empty prompt should not inject")
		}
		InjectSystemPrompt(nil, "P") // must not panic
	})
}

func TestInjectCavemanAndPonytailLevels(t *testing.T) {
	newBody := func() map[string]any {
		return map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	}

	t.Run("unknown level is a no-op", func(t *testing.T) {
		body := newBody()
		InjectCaveman(body, "nope")
		InjectPonytail(body, "nope")
		if len(body["messages"].([]any)) != 1 {
			t.Error("unknown level should not inject")
		}
	})

	t.Run("caveman full injects system", func(t *testing.T) {
		body := newBody()
		InjectCaveman(body, CavemanFull)
		msgs := body["messages"].([]any)
		if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
			t.Fatalf("expected prepended system message, got %+v", msgs)
		}
		content := msgs[0].(map[string]any)["content"].(string)
		if content != CavemanPrompts[CavemanFull] {
			t.Error("injected caveman prompt does not match CavemanPrompts[full]")
		}
	})

	t.Run("ponytail ultra injects system", func(t *testing.T) {
		body := newBody()
		InjectPonytail(body, PonytailUltra)
		msgs := body["messages"].([]any)
		if len(msgs) != 2 || msgs[0].(map[string]any)["content"] != PonytailPrompts[PonytailUltra] {
			t.Errorf("expected ponytail ultra prompt prepended, got %+v", msgs)
		}
	})
}
