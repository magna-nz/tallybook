package claude

import "testing"

// Fast mode bills at a premium, so the speed the harness recorded has to
// survive parsing. It arrives inside message.usage, not beside it.
func TestSpeedIsReadFromUsage(t *testing.T) {
	cases := []struct {
		name  string
		usage string
		want  string
	}{
		{"fast", `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5,"speed":"fast"}`, "fast"},
		{"standard", `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5,"speed":"standard"}`, "standard"},
		{"null reads as empty", `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5,"speed":null}`, ""},
		{"absent reads as empty", usage1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeTranscript(t, "proj/s1.jsonl",
				asst("a1", "msg_1", "claude-opus-5", `{"type":"text","text":"ok"}`, c.usage))
			tr, err := Parse(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(tr.Turns) != 1 {
				t.Fatalf("want 1 turn, got %d", len(tr.Turns))
			}
			if got := tr.Turns[0].Speed; got != c.want {
				t.Errorf("Speed = %q, want %q", got, c.want)
			}
		})
	}
}

// A tool-using reply spans several records sharing one message id. The
// speed is taken from the same record the usage came from, so it must not
// be lost or overwritten by the later parts of the same turn.
func TestSpeedSurvivesMergedRecords(t *testing.T) {
	fast := `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5,"speed":"fast"}`
	p := writeTranscript(t, "proj/s1.jsonl",
		asst("a1", "msg_1", "claude-opus-5", `{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}`, fast),
		asst("a2", "msg_1", "claude-opus-5", `{"type":"text","text":"done"}`, usage1),
	)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 1 {
		t.Fatalf("want 1 merged turn, got %d", len(tr.Turns))
	}
	if got := tr.Turns[0].Speed; got != "fast" {
		t.Errorf("Speed = %q, want %q", got, "fast")
	}
}

// A speed field that is not a string must never cost the turn its tokens.
// Typing it as a plain string made the whole assistant record fail to
// unmarshal, and the parser drops such records - so a harness that ever
// reshaped this field would have deleted turns outright, silently, which is
// far worse than the half-price bug the field exists to fix.
func TestNonStringSpeedKeepsTheTurn(t *testing.T) {
	for _, c := range []struct{ name, literal string }{
		{"number", `2`},
		{"object", `{"tier":"fast"}`},
		{"bool", `true`},
		{"array", `["fast"]`},
		{"null", `null`},
	} {
		t.Run(c.name, func(t *testing.T) {
			u := `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5,"speed":` + c.literal + `}`
			p := writeTranscript(t, "proj/s1.jsonl",
				asst("a1", "msg_1", "claude-opus-5", `{"type":"text","text":"ok"}`, u))
			tr, err := Parse(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(tr.Turns) != 1 {
				t.Fatalf("turn dropped: got %d turns, want 1", len(tr.Turns))
			}
			if got := tr.Turns[0].Speed; got != "" {
				t.Errorf("Speed = %q, want \"\" for an unusable shape", got)
			}
			if tr.Turns[0].Usage.Input != 10 || tr.Turns[0].Usage.Output != 5 {
				t.Errorf("tokens lost: %+v", tr.Turns[0].Usage)
			}
		})
	}
}

// The records sharing a message id repeat the usage block, but the parser
// keeps the first one that carries usage. If that record happened to omit
// the speed, a fast turn was priced as standard.
func TestSpeedTakenFromLaterRecordWhenFirstOmitsIt(t *testing.T) {
	noSpeed := `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5}`
	withSpeed := `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5,"speed":"fast"}`
	p := writeTranscript(t, "proj/s1.jsonl",
		asst("a1", "msg_1", "claude-opus-5", `{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}`, noSpeed),
		asst("a2", "msg_1", "claude-opus-5", `{"type":"text","text":"done"}`, withSpeed),
	)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 1 {
		t.Fatalf("want 1 merged turn, got %d", len(tr.Turns))
	}
	if got := tr.Turns[0].Speed; got != "fast" {
		t.Errorf("Speed = %q, want %q", got, "fast")
	}
	// The usage itself must still come from the first record only.
	if tr.Turns[0].Usage.Input != 10 {
		t.Errorf("usage double-counted: %+v", tr.Turns[0].Usage)
	}
}
