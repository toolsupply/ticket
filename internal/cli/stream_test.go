package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runJSONStream(t *testing.T, input string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	argv := append([]string{"-j", "-i"}, args...)
	code = runInteractiveIO(argv, &out, strings.NewReader(input), &errOut)
	return out.String(), errOut.String(), code
}

func decodeJSONStreamResponses(t *testing.T, output string) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	var responses []map[string]any
	for {
		var response map[string]any
		if err := decoder.Decode(&response); err != nil {
			if err == io.EOF {
				return responses
			}
			t.Fatalf("decode streaming response: %v\noutput=%q", err, output)
		}
		responses = append(responses, response)
	}
}

func streamErrorCode(response map[string]any) string {
	errorObject, _ := response["error"].(map[string]any)
	code, _ := errorObject["code"].(string)
	return code
}

func streamRequest(t *testing.T, args []string, stdin *string) string {
	t.Helper()
	request := map[string]any{"args": args}
	if stdin != nil {
		request["stdin"] = *stdin
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestJSONStreamFlagSpellingsAndOrder(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	want := mustCLI(t, "version")

	tests := []struct {
		name string
		args []string
	}{
		{"short_json_then_short_interactive", []string{"-j", "-i"}},
		{"short_interactive_then_short_json", []string{"-i", "-j"}},
		{"long_json_then_long_interactive", []string{"--json", "--interactive"}},
		{"long_interactive_then_long_json", []string{"--interactive", "--json"}},
		{"short_json_then_long_interactive", []string{"-j", "--interactive"}},
		{"long_interactive_then_short_json", []string{"--interactive", "-j"}},
		{"long_json_then_short_interactive", []string{"--json", "-i"}},
		{"short_interactive_then_long_json", []string{"-i", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, stderr, code := runCLIHumanStdin(t, `{"args":["version"]}`+"\n", tt.args...)
			if code != 0 || stderr != "" || out != want {
				t.Fatalf("Run(%v): exit=%d stdout=%q stderr=%q; want stdout=%q", tt.args, code, out, stderr, want)
			}
		})
	}
}

func TestInteractiveFlagNamesRemainOneShotCommandValues(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")

	for _, title := range []string{"-i", "--interactive"} {
		t.Run(title, func(t *testing.T) {
			out, code := runCLI(t, "create", "--title", title)
			if code != 0 {
				t.Fatalf("one-shot create with title %q: exit=%d output=%q", title, code, out)
			}
			id := exactlyOneJSONObject(t, out)["id"].(string)
			shown := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
			if shown["title"] != title {
				t.Fatalf("one-shot create title=%v want %q", shown["title"], title)
			}
		})
	}
}

func TestJSONStreamOneRequestOneResponseAndRecovery(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Stream target", "Read this explicitly."))["id"].(string)
	t.Setenv("TICKET_CURRENT", "invalid-environment-current")
	input := strings.Join([]string{
		`{"args":["show","` + id + `"]}`,
		`{"args":`,
		``,
		`{"args":["show"]}`,
		`{"args":["version"]}`,
	}, "\n")

	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("JSON stream: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 4 || strings.Count(out, "\n") != 4 {
		t.Fatalf("responses=%d want 4: %q", len(responses), out)
	}
	if responses[0]["id"] != id {
		t.Fatalf("show response: %v", responses[0])
	}
	if streamErrorCode(responses[1]) != "invalid_json" || streamErrorCode(responses[2]) != "invalid_argument" {
		t.Fatalf("recoverable errors: %v", responses)
	}
	if responses[3]["version"] != Version {
		t.Fatalf("final unterminated request was not executed: %v", responses[3])
	}
	oneShot := mustCLI(t, "version")
	if lines := strings.Split(out, "\n"); lines[3]+"\n" != oneShot {
		t.Fatalf("stream changed one-shot JSON response: stream=%q one-shot=%q", lines[3]+"\n", oneShot)
	}
}

func TestJSONStreamInfoResponseAndRecovery(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	if err := os.WriteFile(filepath.Join(dir, "tickets", "config.json"), []byte(`{"format_version":1,"name":"Stream repository"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		streamRequest(t, []string{"info"}, nil),
		streamRequest(t, []string{"version"}, nil),
	}, "\n") + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("info JSON stream: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 2 || strings.Count(out, "\n") != 2 {
		t.Fatalf("info stream responses=%d output=%q", len(responses), out)
	}
	info := responses[0]
	if info["path"] != filepath.Join(dir, "tickets") || info["name"] != "Stream repository" || info["format_version"] != float64(1) || info["storage_version"] != float64(1) || info["scope"] != nil {
		t.Fatalf("info stream response: %v", info)
	}
	if responses[1]["version"] != Version {
		t.Fatalf("stream was not usable after info: %v", responses)
	}
}

func TestJSONStreamRejectsStdinConsumersWithoutConsumingRequests(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	input := strings.Join([]string{
		`{"args":["create","Payload path","--input","-"]}`,
		`{"args":["create","Trailing dash","-"]}`,
		`{"args":["create","Stream-created","Explicit objective."]}`,
		`{"args":["version"]}`,
	}, "\n") + "\n"

	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("JSON stdin ownership: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 4 || streamErrorCode(responses[0]) != "invalid_argument" || streamErrorCode(responses[1]) != "invalid_argument" {
		t.Fatalf("stdin rejection responses: %v", responses)
	}
	if responses[2]["id"] == nil || responses[3]["version"] != Version {
		t.Fatalf("requests after stdin rejection were not executed: %v", responses)
	}
}

func TestJSONStreamPerRequestStdinSupportsAllForms(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	t.Setenv("TICKET_ACTOR", "agent")

	runOne := func(args []string, stdin *string) map[string]any {
		out, stderr, code := runJSONStream(t, streamRequest(t, args, stdin)+"\n")
		if code != 0 || stderr != "" {
			t.Fatalf("JSON request %v: exit=%d stdout=%q stderr=%q", args, code, out, stderr)
		}
		responses := decodeJSONStreamResponses(t, out)
		if len(responses) != 1 {
			t.Fatalf("JSON request %v responses: %v", args, responses)
		}
		return responses[0]
	}

	createInput := `{"title":"Stream structured","sections":{"objective":"Read request stdin."}}`
	first := runOne([]string{"create", "--input", "-"}, &createInput)["id"].(string)
	objective := "Read the trailing objective.\n"
	second := runOne([]string{"create", "Stream trailing", "-"}, &objective)["id"].(string)
	third := runOne([]string{"create", "Stream close"}, nil)["id"].(string)

	updateInput := `{"set":{"title":"Stream updated"}}`
	updated := runOne([]string{"update", first, "--input", "-"}, &updateInput)
	if updated["id"] != first || updated["changed"] != true {
		t.Fatalf("stream update: %v", updated)
	}
	claimed := runOne([]string{"claim", first}, nil)
	if claimed["assignee"] != "agent" {
		t.Fatalf("stream claim: %v", claimed)
	}
	releaseInput := `{"handoff":"Released from stream."}`
	released := runOne([]string{"release", first, "--input", "-"}, &releaseInput)
	if released["id"] != first || released["assignee"] != nil {
		t.Fatalf("stream release: %v", released)
	}
	rejectInput := `{"outcome":"duplicate"}`
	rejected := runOne([]string{"reject", second, "--input", "-"}, &rejectInput)
	if rejected["id"] != second || rejected["state"] != "rejected" {
		t.Fatalf("stream reject: %v", rejected)
	}
	closeInput := `{"outcome":"completed by stream"}`
	closed := runOne([]string{"close", third, "--input", "-"}, &closeInput)
	if closed["id"] != third || closed["state"] != "completed" {
		t.Fatalf("stream close: %v", closed)
	}
}

func TestJSONStreamStdinPresenceValidationAndRecovery(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	empty := ""
	input := strings.Join([]string{
		streamRequest(t, []string{"create", "Missing stdin", "--input", "-"}, nil),
		streamRequest(t, []string{"version"}, &empty),
		streamRequest(t, []string{"create", "Filename", "--input", "payload.json"}, &empty),
		streamRequest(t, []string{"create", "Editor", "--edit"}, &empty),
		streamRequest(t, []string{"version"}, nil),
	}, "\n")
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("stdin presence validation: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 5 {
		t.Fatalf("responses=%d want 5: %v", len(responses), responses)
	}
	for i := 0; i < 4; i++ {
		if streamErrorCode(responses[i]) != "invalid_argument" {
			t.Fatalf("response %d: %v", i, responses[i])
		}
	}
	if responses[4]["version"] != Version {
		t.Fatalf("stream did not recover after stdin validation errors: %v", responses[4])
	}
}

func TestJSONStreamEmptyArgsRejectsStdinAndRecovers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	empty := ""
	input := strings.Join([]string{
		streamRequest(t, []string{}, &empty),
		streamRequest(t, []string{"version"}, nil),
	}, "\n") + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("empty args stdin: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 2 || streamErrorCode(responses[0]) != "invalid_argument" || responses[1]["version"] != Version {
		t.Fatalf("empty args stdin recovery: %v", responses)
	}
}

func TestJSONStreamStdinMustBeString(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	input := "{\"args\":[\"version\"],\"stdin\":null}\n" + streamRequest(t, []string{"version"}, nil) + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("stdin type validation: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 2 || streamErrorCode(responses[0]) != "invalid_json" || responses[1]["version"] != Version {
		t.Fatalf("stdin type recovery: %v", responses)
	}
}

func TestJSONStreamEscapedAndOversizedRequestInput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")

	// Escaping the backslashes in the nested command JSON makes the encoded
	// request exceed the old 2 MiB frame limit while decoded input remains valid.
	escapedObjective := strings.Repeat(`\\`, 600000)
	escapedBody := `{"title":"Escaped input","sections":{"objective":"` + escapedObjective + `"}}`
	escapedRequest := streamRequest(t, []string{"create", "--input", "-"}, &escapedBody)
	oversizedBody := `{"title":"Oversized input","sections":{"objective":"` + strings.Repeat("x", maxInputBytes) + `"}}`
	input := escapedRequest + "\n" +
		streamRequest(t, []string{"create", "--input", "-"}, &oversizedBody) + "\n" +
		streamRequest(t, []string{"version"}, nil) + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("escaped/oversized input: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 3 || responses[0]["id"] == nil || streamErrorCode(responses[1]) != "file_too_large" || responses[2]["version"] != Version {
		t.Fatalf("escaped/oversized recovery: %v", responses)
	}
}

func TestJSONStreamRejectsMalformedFramesAndContinues(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	input := strings.Join([]string{
		`{"unknown":true}`,
		`{"args":"version"}`,
		`{"args":null}`,
		`{"args":["version"]} {"args":[]}`,
		`{"args":["version"],"args":[]}`,
		`{"args":["version"]}`,
	}, "\n") + "\n"

	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("malformed JSON stream: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 6 {
		t.Fatalf("responses=%d want 6: %v", len(responses), responses)
	}
	for i := 0; i < 5; i++ {
		if streamErrorCode(responses[i]) != "invalid_json" {
			t.Fatalf("malformed response %d: %v", i, responses[i])
		}
	}
	if responses[5]["version"] != Version {
		t.Fatalf("stream did not recover after malformed frames: %v", responses[5])
	}
}

func TestJSONStreamOversizedFrameIsRecoverable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	request := `{"args":["version"]}`
	exactLimit := request + strings.Repeat(" ", maxJSONStreamFrameBytes-len(request))
	input := exactLimit + "\n" + strings.Repeat("x", maxJSONStreamFrameBytes+1) + "\n" + request + "\n"

	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("oversized JSON stream: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 3 || responses[0]["version"] != Version || streamErrorCode(responses[1]) != "file_too_large" || responses[2]["version"] != Version {
		t.Fatalf("oversized frame recovery: %v", responses)
	}
}

func TestJSONStreamStartupErrorsUseProtocolOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	out, stderr, code := runJSONStream(t, "", "version")
	if code != 2 || stderr != "" {
		t.Fatalf("stream startup error: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 1 || streamErrorCode(responses[0]) != "invalid_argument" {
		t.Fatalf("startup response: %v", responses)
	}
}

func TestJSONStreamStartupFailureDoesNotConsumeFrames(t *testing.T) {
	t.Chdir(t.TempDir())
	input := streamRequest(t, []string{"version"}, nil) + "\n" + streamRequest(t, []string{"version"}, nil) + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code == 0 || stderr != "" {
		t.Fatalf("stream startup failure: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 1 || streamErrorCode(responses[0]) == "" {
		t.Fatalf("startup failure responses: %v", responses)
	}
}

func TestJSONStreamVersionProbeReadiesRecoverableStream(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	input := streamRequest(t, []string{"version"}, nil) + "\n" +
		streamRequest(t, []string{"version"}, nil) + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("version probe: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 2 || responses[0]["version"] != Version || responses[1]["version"] != Version {
		t.Fatalf("version probe responses: %v", responses)
	}
}

func TestJSONStreamRecoverableErrorAfterVersionProbe(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	input := streamRequest(t, []string{"version"}, nil) + "\n" +
		streamRequest(t, []string{"not-a-command"}, nil) + "\n" +
		streamRequest(t, []string{"version"}, nil) + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("recoverable stream error: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 3 || responses[0]["version"] != Version || streamErrorCode(responses[1]) != "invalid_argument" || responses[2]["version"] != Version {
		t.Fatalf("recoverable stream responses: %v", responses)
	}
}

func TestJSONStreamSCMFailureReportsAppliedMutationAndRecovers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	installFakeSCM(t, filepath.Join(dir, "bin"), "scm-push-retry")
	t.Setenv("SCM_LOG", filepath.Join(dir, "scm.log"))
	t.Setenv("SCM_STATE", filepath.Join(dir, "scm-committed"))
	t.Setenv("FAIL_PUSH", "1")
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "sync")

	input := streamRequest(t, []string{"create", "Stream SCM failure", "Inspect the persisted ticket."}, nil) + "\n" +
		streamRequest(t, []string{"version"}, nil) + "\n"
	out, stderr, code := runJSONStream(t, input)
	if code != 0 || stderr != "" {
		t.Fatalf("stream SCM failure: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	responses := decodeJSONStreamResponses(t, out)
	if len(responses) != 2 || streamErrorCode(responses[0]) != "io_error" || responses[1]["version"] != Version {
		t.Fatalf("stream SCM failure recovery: %v", responses)
	}
	errorObject := responses[0]["error"].(map[string]any)
	details := errorObject["details"].(map[string]any)
	id, _ := details["id"].(string)
	if details["mutation_applied"] != true || id == "" {
		t.Fatalf("stream SCM failure details: %v", errorObject)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["id"] != id || view["state"] != "open" {
		t.Fatalf("created ticket after streamed SCM failure: %v", view)
	}
}
