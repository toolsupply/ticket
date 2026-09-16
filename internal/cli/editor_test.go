package cli

import (
	"reflect"
	"runtime"
	"testing"
)

func TestSelectedEditorPrecedence(t *testing.T) {
	t.Setenv("TICKET_EDITOR", "ticket-editor --wait")
	t.Setenv("VISUAL", "visual-editor")
	t.Setenv("EDITOR", "editor")
	if got := selectedEditor(); got != "ticket-editor --wait" {
		t.Fatalf("TICKET_EDITOR precedence: got %q", got)
	}
	t.Setenv("TICKET_EDITOR", "")
	if got := selectedEditor(); got != "visual-editor" {
		t.Fatalf("VISUAL precedence: got %q", got)
	}
	t.Setenv("VISUAL", "")
	if got := selectedEditor(); got != "editor" {
		t.Fatalf("EDITOR fallback: got %q", got)
	}
}

func TestSelectedEditorPlatformDefaults(t *testing.T) {
	if got := defaultEditor("windows"); got != "notepad.exe" {
		t.Fatalf("Windows default: got %q", got)
	}
	if got := defaultEditor("linux"); got != "vi" {
		t.Fatalf("Unix default: got %q", got)
	}
	t.Setenv("TICKET_EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	want := defaultEditor(runtime.GOOS)
	if got := selectedEditor(); got != want {
		t.Fatalf("host default: got %q want %q", got, want)
	}
}

func TestEditorInvocationParsesArgumentsWithoutShell(t *testing.T) {
	command, args, err := editorInvocation(`code --wait "profile name" '$(touch should-not-run)'`, "/tmp/draft.md", 7)
	if err != nil {
		t.Fatal(err)
	}
	wantCommand := "code"
	wantArgs := []string{"--wait", "profile name", "$(touch should-not-run)", "/tmp/draft.md"}
	if command != wantCommand || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("invocation: command=%q args=%q want command=%q args=%q", command, args, wantCommand, wantArgs)
	}
}

func TestEditorInvocationAddsLineOnlyForVi(t *testing.T) {
	_, args, err := editorInvocation("vim -f", "/tmp/draft.md", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-f", "+3", "/tmp/draft.md"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("vim arguments: got %q want %q", args, want)
	}
	_, args, err = editorInvocation("code --wait", "/tmp/draft.md", 3)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"--wait", "/tmp/draft.md"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("non-vim arguments: got %q want %q", args, want)
	}
	command, args, err := editorInvocation(`"C:\\Program Files\\Editor\\editor.exe" --wait`, `C:\draft.md`, 3)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--wait", `C:\draft.md`}
	if command != `C:\Program Files\Editor\editor.exe` || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("Windows path arguments: command=%q args=%q", command, args)
	}
}

func TestEditorInvocationRejectsUnterminatedQuote(t *testing.T) {
	if _, _, err := editorInvocation(`vim "unfinished`, "/tmp/draft.md", 1); err == nil {
		t.Fatal("unterminated editor quote accepted")
	}
}

func defaultEditor(goos string) string {
	if goos == "windows" {
		return "notepad.exe"
	}
	return "vi"
}
